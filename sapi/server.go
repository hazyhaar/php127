// Package sapi implémente le serveur HTTP SAPI (Web Worker Pool, superglobales
// HTTP dans la Zone de requête et cycle de vie avec z.Reset()).
package sapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// DefaultZoneCapacity est la capacité par défaut (4 Mio) d'une Zone de requête SAPI.
const DefaultZoneCapacity uint32 = 4 << 20

// Runner est l'interface découplée d'exécution d'une requête PHP dans une Zone.
type Runner interface {
	RunRequest(z *zone.Zone, req *RequestContext, out io.Writer) error
}

// HandlerFunc adapte une fonction en Runner.
type HandlerFunc func(z *zone.Zone, req *RequestContext, out io.Writer) error

func (f HandlerFunc) RunRequest(z *zone.Zone, req *RequestContext, out io.Writer) error {
	return f(z, req, out)
}

// ProgramExecutor exécute un *bytecode.Program précompilé dans une Zone de requête.
type ProgramExecutor func(z *zone.Zone, prog *bytecode.Program, req *RequestContext, out io.Writer) error

// NewProgramRunner construit un Runner pour un *bytecode.Program précompilé.
func NewProgramRunner(prog *bytecode.Program, exec ProgramExecutor) Runner {
	return HandlerFunc(func(z *zone.Zone, req *RequestContext, out io.Writer) error {
		return exec(z, prog, req, out)
	})
}

// Options configure le serveur SAPI et son pool de Zones de requête.
type Options struct {
	ZoneCapacity uint32
	MaxWorkers   int
	ServerConfig ServerConfig
	AfterReset   func(z *zone.Zone)
}

type workerSlot struct {
	z   *zone.Zone
	buf bytes.Buffer
}

// Server est un http.Handler qui gère un pool de Zones réutilisables, peuple les
// superglobales HTTP par requête et garantit z.Reset() en fin de requête.
type Server struct {
	runner       Runner
	opts         Options
	boundedCh    chan *workerSlot
	syncPool     sync.Pool
	requestsDone atomic.Uint64
	zonesCreated atomic.Uint64
}

// NewServer construit un Server SAPI avec la configuration fournie.
// Si opts.MaxWorkers > 0, un pool borné de opts.MaxWorkers Zones pré-allouées est utilisé ;
// sinon, un sync.Pool dynamique est utilisé.
func NewServer(runner Runner, opts Options) *Server {
	if opts.ZoneCapacity == 0 {
		opts.ZoneCapacity = DefaultZoneCapacity
	}
	s := &Server{
		runner: runner,
		opts:   opts,
	}
	if opts.MaxWorkers > 0 {
		s.boundedCh = make(chan *workerSlot, opts.MaxWorkers)
		for range opts.MaxWorkers {
			s.zonesCreated.Add(1)
			s.boundedCh <- &workerSlot{z: zone.New(opts.ZoneCapacity)}
		}
	} else {
		s.syncPool.New = func() any {
			s.zonesCreated.Add(1)
			return &workerSlot{z: zone.New(opts.ZoneCapacity)}
		}
	}
	return s
}

func (s *Server) acquireSlot() *workerSlot {
	if s.boundedCh != nil {
		return <-s.boundedCh
	}
	return s.syncPool.Get().(*workerSlot)
}

func (s *Server) releaseSlot(slot *workerSlot) {
	if s.boundedCh != nil {
		s.boundedCh <- slot
		return
	}
	s.syncPool.Put(slot)
}

// RequestsHandled retourne le nombre total de requêtes ayant complété leur cycle z.Reset().
func (s *Server) RequestsHandled() uint64 { return s.requestsDone.Load() }

// ZonesCreated retourne le nombre total d'arènes Zone instanciées par le pool.
func (s *Server) ZonesCreated() uint64 { return s.zonesCreated.Load() }

// ServeHTTP acquiert une Zone du pool, peuple les superglobales HTTP, exécute le
// Runner, émet les en-têtes et le corps HTTP, puis exécute z.Reset() avant de
// restituer la Zone au pool.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	slot := s.acquireSlot()
	z := slot.z
	slot.buf.Reset()

	reqCtx := &RequestContext{
		Request:    r,
		StatusCode: http.StatusOK,
	}
	BindContext(z, reqCtx)

	defer func() {
		if rec := recover(); rec != nil && !reqCtx.HeadersSent {
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		stdlib.CleanupUploadedFiles(z)
		BindContext(z, nil)
		z.Reset(func(item zone.CleanupItem) {
			types.DispatchObjectDestructor(z, item)
		})
		if s.opts.AfterReset != nil {
			s.opts.AfterReset(z)
		}
		s.requestsDone.Add(1)
		s.releaseSlot(slot)
	}()

	if err := PopulateSuperglobals(z, r, s.opts.ServerConfig, reqCtx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var runErr error
	if s.runner != nil {
		runErr = s.runner.RunRequest(z, reqCtx, &slot.buf)
	}

	// Publication des en-têtes collectés dans w.Header()
	for _, h := range reqCtx.Headers {
		if strings.EqualFold(h.Name, "Set-Cookie") {
			w.Header().Add("Set-Cookie", h.Value)
		} else {
			w.Header().Add(h.Name, h.Value)
		}
	}
	reqCtx.HeadersSent = true

	status := reqCtx.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	if runErr != nil && status == http.StatusOK {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
	if slot.buf.Len() > 0 {
		_, _ = w.Write(slot.buf.Bytes())
	} else if runErr != nil {
		_, _ = io.WriteString(w, runErr.Error())
	}
}
