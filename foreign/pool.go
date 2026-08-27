// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/foreign: Pont de fallback hybride sécurisé pour fonctions PHP externes non implémentées

package foreign

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/security"
	"code.hazyhaar.fr/devhoros/php127/types"
)

var (
	ErrForeignPoolClosed      = errors.New("foreign: le pool de workers externes est fermé")
	ErrForeignExecutionFailed = errors.New("foreign: échec d'exécution dans le worker externe")
)

type WorkerPool interface {
	Call(funcName string, args []types.Value) (types.Value, error)
	Close() error
}

// MockWorkerPool pour les tests et l'émulation déterministe de fonctions spécifiques
type MockWorkerPool struct {
	mu       sync.RWMutex
	handlers map[string]func(args []types.Value) (types.Value, error)
}

func NewMockWorkerPool() *MockWorkerPool {
	return &MockWorkerPool{
		handlers: make(map[string]func(args []types.Value) (types.Value, error)),
	}
}

func (m *MockWorkerPool) RegisterHandler(name string, fn func(args []types.Value) (types.Value, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[name] = fn
}

func (m *MockWorkerPool) Call(funcName string, args []types.Value) (types.Value, error) {
	m.mu.RLock()
	fn, ok := m.handlers[funcName]
	m.mu.RUnlock()

	if ok && fn != nil {
		return fn(args)
	}

	// Émulation générique par défaut
	return types.NewString(fmt.Sprintf("foreign_result(%s, %d args)", funcName, len(args))), nil
}

func (m *MockWorkerPool) Close() error {
	return nil
}

// UnixSocketWorkerPool pour la communication avec un pool de workers PHP externes via socket Unix
type UnixSocketWorkerPool struct {
	SocketPath string
	Timeout    time.Duration
	mu         sync.Mutex
	conn       net.Conn
	closed     bool
}

type ForeignRequest struct {
	Function string        `json:"func"`
	Args     []interface{} `json:"args"`
}

type ForeignResponse struct {
	Success bool        `json:"success"`
	Result  interface{} `json:"result"`
	Error   string      `json:"error,omitempty"`
}

func NewUnixSocketWorkerPool(socketPath string, timeout time.Duration) *UnixSocketWorkerPool {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &UnixSocketWorkerPool{
		SocketPath: socketPath,
		Timeout:    timeout,
	}
}

func (u *UnixSocketWorkerPool) Call(funcName string, args []types.Value) (types.Value, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if u.closed {
		return types.NewNull(), ErrForeignPoolClosed
	}

	conn, err := net.DialTimeout("unix", u.SocketPath, u.Timeout)
	if err != nil {
		return types.NewNull(), fmt.Errorf("foreign: échec connexion socket %s: %w", u.SocketPath, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(u.Timeout))

	rawArgs := make([]interface{}, len(args))
	for i, a := range args {
		rawArgs[i] = a.String()
	}

	req := ForeignRequest{
		Function: funcName,
		Args:     rawArgs,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return types.NewNull(), err
	}
	reqBytes = append(reqBytes, '\n')

	if _, err := conn.Write(reqBytes); err != nil {
		return types.NewNull(), err
	}

	decoder := json.NewDecoder(conn)
	var resp ForeignResponse
	if err := decoder.Decode(&resp); err != nil {
		return types.NewNull(), err
	}

	if !resp.Success {
		return types.NewNull(), fmt.Errorf("foreign worker error: %s", resp.Error)
	}

	// Enregistrement de l'événement dans le hub de télémétrie
	security.GetTelemetryHub().RecordEvent(security.EventForeignFallbackExecuted, "foreign_pool", funcName, 0, 0)

	switch v := resp.Result.(type) {
	case string:
		return types.NewString(v), nil
	case float64:
		return types.NewLong(int64(v)), nil
	case bool:
		return types.NewBool(v), nil
	default:
		return types.NewNull(), nil
	}
}

func (u *UnixSocketWorkerPool) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.closed = true
	if u.conn != nil {
		return u.conn.Close()
	}
	return nil
}
