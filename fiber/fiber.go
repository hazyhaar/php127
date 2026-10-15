// Package fiber implémente la classe finale Fiber et l'exception FiberError de PHP 8.3.
// L'ordonnancement coopératif garantit qu'une seule goroutine accède à la Zone à tout instant.
package fiber

import (
	"fmt"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// State représente l'état du cycle de vie d'une instance de Fiber.
type State uint8

const (
	StateUnstarted State = iota
	StateRunning
	StateSuspended
	StateTerminated
)

// Constantes d'identification de classe et de nettoyage dans la Zone.
const (
	ClassIDFiber      uint32           = 5201
	ClassIDFiberError uint32           = 5202
	CleanupFiber      zone.CleanupKind = 52
)

// Callback représente la fonction exécutée par une fibre dans la Zone.
type Callback func(z *zone.Zone, args []types.Value) (types.Value, error)

// FiberError représente l'exception PHP FiberError levée lors d'opérations invalides sur une fibre.
type FiberError struct {
	Message string
}

func (e FiberError) Error() string {
	return e.Message
}

// ThrownException encapsule une valeur d'exception PHP injectée via Fiber::throw.
type ThrownException struct {
	Exception types.Value
	Message   string
}

func (e ThrownException) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "Uncaught exception thrown into fiber"
}

type fiberAbortToken struct{}

type resumeMsg struct {
	val   types.Value
	err   error
	abort bool
}

type yieldMsg struct {
	val        types.Value
	err        error
	terminated bool
}

// Fiber représente une coroutine coopérative à pile complète associée à une Zone.
type Fiber struct {
	id         uint32
	mgrID      uint32
	z          *zone.Zone
	obj        types.Value
	callback   Callback
	state      State
	destroying bool
	uncaught   bool
	closed     bool
	retVal     types.Value
	err        error
	toFiber    chan resumeMsg
	fromFiber  chan yieldMsg
	done       chan struct{}
}

type objKey struct {
	slot uint32
	gen  uint16
}

// Manager maintient la pile des fibres actives et le registre des fibres d'une Zone.
type Manager struct {
	id      uint32
	z       *zone.Zone
	nextID  uint32
	stack   []*Fiber
	byID    map[uint32]*Fiber
	byObj   map[objKey]*Fiber
	cleaned bool
}

var (
	managersMu   sync.Mutex
	nextMgrID    uint32
	managersByZ  = make(map[*zone.Zone]*Manager)
	managersByID = make(map[uint32]*Manager)
)

func init() {
	if !types.IsProcessZoneFrozen() {
		types.RegisterObjectClassName(ClassIDFiber, "Fiber")
		types.RegisterObjectClassName(ClassIDFiberError, "FiberError")
	}
}

// EnsureClassNames ré-enregistre les noms de classes Fiber et FiberError après un ResetProcessZone.
func EnsureClassNames() {
	if !types.IsProcessZoneFrozen() {
		types.RegisterObjectClassName(ClassIDFiber, "Fiber")
		types.RegisterObjectClassName(ClassIDFiberError, "FiberError")
	}
}

func getOrCreateManager(z *zone.Zone) *Manager {
	if z == nil {
		panic("fiber: nil zone")
	}
	managersMu.Lock()
	defer managersMu.Unlock()
	if mgr, ok := managersByZ[z]; ok {
		return mgr
	}
	nextMgrID++
	mgrID := nextMgrID
	mgr := &Manager{
		id:    mgrID,
		z:     z,
		byID:  make(map[uint32]*Fiber),
		byObj: make(map[objKey]*Fiber),
	}
	managersByZ[z] = mgr
	managersByID[mgrID] = mgr
	z.RegisterCleanup(zone.CleanupItem{
		Kind:   CleanupFiber,
		Target: mgrID,
		Extra:  0,
	})
	return mgr
}

func lookupManager(z *zone.Zone) *Manager {
	managersMu.Lock()
	defer managersMu.Unlock()
	return managersByZ[z]
}

// CleanupDispatcher retourne la fonction de rappel compatible avec z.Reset.
func CleanupDispatcher(z *zone.Zone) func(zone.CleanupItem) {
	return func(item zone.CleanupItem) {
		DispatchCleanup(z, item)
	}
}

// DispatchCleanup traite les éléments de nettoyage de fibres et délègue les destructeurs d'objets lors de z.Reset.
func DispatchCleanup(z *zone.Zone, item zone.CleanupItem) {
	switch item.Kind {
	case CleanupFiber:
		managersMu.Lock()
		mgr := managersByID[item.Target]
		managersMu.Unlock()
		if mgr == nil {
			return
		}
		if z == nil {
			z = mgr.z
		}
		if item.Extra == 0 {
			mgr.cleanupAll(z)
			managersMu.Lock()
			delete(managersByID, mgr.id)
			if managersByZ[z] == mgr {
				delete(managersByZ, z)
			}
			managersMu.Unlock()
			return
		}
		if f, ok := mgr.byID[item.Extra]; ok {
			f.cleanup(z)
		}
	case zone.CleanupDestructor:
		if z != nil {
			types.DispatchObjectDestructor(z, item)
		}
	}
}

// CleanupZone force la terminaison de toutes les fibres suspendues attachées à la Zone.
func CleanupZone(z *zone.Zone) {
	mgr := lookupManager(z)
	if mgr == nil {
		return
	}
	mgr.cleanupAll(z)
	managersMu.Lock()
	delete(managersByID, mgr.id)
	if managersByZ[z] == mgr {
		delete(managersByZ, z)
	}
	managersMu.Unlock()
}

func (m *Manager) cleanupAll(z *zone.Zone) {
	if m.cleaned {
		return
	}
	m.cleaned = true
	// Nettoyage en ordre LIFO d'allocation des fibres.
	for id := m.nextID; id >= 1; id-- {
		if f, ok := m.byID[id]; ok {
			f.cleanup(z)
		}
		if id == 1 {
			break
		}
	}
	clear(m.byID)
	clear(m.byObj)
	m.stack = m.stack[:0]
}

// New crée une nouvelle instance de Fiber non démarrée attachée à la Zone z.
func New(z *zone.Zone, cb Callback) (*Fiber, error) {
	if cb == nil {
		return nil, FiberError{Message: "Fiber::__construct(): Argument #1 ($callback) must be a valid callback"}
	}
	mgr := getOrCreateManager(z)
	mgr.nextID++
	fid := mgr.nextID
	f := &Fiber{
		id:       fid,
		mgrID:    mgr.id,
		z:        z,
		callback: cb,
		state:    StateUnstarted,
	}
	mgr.byID[fid] = f
	z.RegisterCleanup(zone.CleanupItem{
		Kind:   CleanupFiber,
		Target: mgr.id,
		Extra:  fid,
	})
	return f, nil
}

// NewObject crée une instance de Fiber ainsi que son objet Value de type ClassIDFiber dans la Zone.
func NewObject(z *zone.Zone, cb Callback) (types.Value, *Fiber, error) {
	if cb == nil {
		return types.Value{}, nil, FiberError{Message: "Fiber::__construct(): Argument #1 ($callback) must be a valid callback"}
	}
	obj, err := types.NewObject(z, ClassIDFiber, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	f, err := New(z, cb)
	if err != nil {
		types.ReleaseObject(z, &obj)
		return types.Value{}, nil, err
	}
	f.obj = obj
	mgr := getOrCreateManager(z)
	mgr.byObj[objKey{slot: obj.DataSlot, gen: obj.Extra}] = f
	return obj, f, nil
}

// FromObject retrouve l'instance Fiber associée à une Value objet PHP.
func FromObject(z *zone.Zone, v types.Value) (*Fiber, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, FiberError{Message: "Expected Fiber object"}
	}
	hdr := types.ObjectInfo(z, v)
	if hdr.ClassID != ClassIDFiber {
		return nil, FiberError{Message: "Object is not an instance of Fiber"}
	}
	mgr := lookupManager(z)
	if mgr == nil {
		return nil, FiberError{Message: "Fiber manager not initialized for zone"}
	}
	f, ok := mgr.byObj[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, FiberError{Message: "Uninitialized Fiber object"}
	}
	return f, nil
}

// Object retourne la Value objet associée à la fibre, ou TypeUndef si créée via New sans objet.
func (f *Fiber) Object() types.Value {
	return f.obj
}

// State retourne l'état courant de la fibre.
func (f *Fiber) State() State {
	return f.state
}

// IsStarted indique si la fibre a été démarrée.
func (f *Fiber) IsStarted() bool {
	return f.state != StateUnstarted
}

// IsSuspended indique si la fibre est actuellement suspendue.
func (f *Fiber) IsSuspended() bool {
	return f.state == StateSuspended
}

// IsRunning indique si la fibre est en cours d'exécution.
func (f *Fiber) IsRunning() bool {
	return f.state == StateRunning
}

// IsTerminated indique si la fibre est terminée.
func (f *Fiber) IsTerminated() bool {
	return f.state == StateTerminated
}

// Start démarre l'exécution de la fibre avec les arguments fournis.
// Lorsque la fibre se termine normalement, Start retourne (null, nil) et la valeur
// retournée par le callback est disponible via GetReturn.
func (f *Fiber) Start(z *zone.Zone, args ...types.Value) (types.Value, error) {
	if z != f.z {
		return types.NewNull(), FiberError{Message: "Fiber zone mismatch"}
	}
	if f.state != StateUnstarted {
		return types.NewNull(), FiberError{Message: "Cannot start a fiber that has already been started"}
	}
	mgr := getOrCreateManager(z)

	retainedArgs := make([]types.Value, len(args))
	for i, a := range args {
		retainedArgs[i] = types.RetainValue(z, a)
	}

	f.toFiber = make(chan resumeMsg)
	f.fromFiber = make(chan yieldMsg)
	f.done = make(chan struct{})
	f.state = StateRunning
	mgr.stack = append(mgr.stack, f)

	go f.run(z, mgr, retainedArgs)

	// Synchronisation stricte : la goroutine appelante attend sur fromFiber
	// dès que le signal de démarrage est envoyé sur toFiber.
	f.toFiber <- resumeMsg{}
	out := <-f.fromFiber
	if out.err != nil {
		return types.NewNull(), out.err
	}
	return out.val, nil
}

func (f *Fiber) run(z *zone.Zone, mgr *Manager, args []types.Value) {
	defer close(f.done)
	defer func() {
		for i := range args {
			if !args[i].IsUndef() {
				types.ReleaseValue(z, &args[i])
			}
		}
	}()

	// Attente de la barrière initiale envoyée par Start.
	<-f.toFiber

	var aborted bool
	var retVal types.Value
	var retErr error

	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(fiberAbortToken); ok {
					aborted = true
					return
				}
				panic(r)
			}
		}()
		retVal, retErr = f.callback(z, args)
	}()

	if len(mgr.stack) > 0 && mgr.stack[len(mgr.stack)-1] == f {
		mgr.stack = mgr.stack[:len(mgr.stack)-1]
	}
	f.state = StateTerminated

	if aborted {
		return
	}

	if retErr != nil {
		f.uncaught = true
		f.err = retErr
		f.fromFiber <- yieldMsg{
			val:        types.NewNull(),
			err:        retErr,
			terminated: true,
		}
		return
	}

	f.retVal = types.RetainValue(z, retVal)
	f.fromFiber <- yieldMsg{
		val:        types.NewNull(),
		terminated: true,
	}
}

// Resume reprend une fibre suspendue en faisant retourner val par l'appel Suspend en attente.
func (f *Fiber) Resume(z *zone.Zone, val types.Value) (types.Value, error) {
	if z != f.z {
		return types.NewNull(), FiberError{Message: "Fiber zone mismatch"}
	}
	if f.state != StateSuspended || f.destroying {
		return types.NewNull(), FiberError{Message: "Cannot resume a fiber that is not suspended"}
	}
	mgr := getOrCreateManager(z)
	f.state = StateRunning
	mgr.stack = append(mgr.stack, f)

	f.toFiber <- resumeMsg{val: val}
	out := <-f.fromFiber
	if out.err != nil {
		return types.NewNull(), out.err
	}
	return out.val, nil
}

// Throw reprend une fibre suspendue en faisant retourner l'erreur exc par l'appel Suspend en attente.
func (f *Fiber) Throw(z *zone.Zone, exc error) (types.Value, error) {
	if z != f.z {
		return types.NewNull(), FiberError{Message: "Fiber zone mismatch"}
	}
	if f.state != StateSuspended || f.destroying {
		return types.NewNull(), FiberError{Message: "Cannot resume a fiber that is not suspended"}
	}
	if exc == nil {
		exc = ThrownException{Message: "Uncaught exception thrown into fiber"}
	}
	mgr := getOrCreateManager(z)
	f.state = StateRunning
	mgr.stack = append(mgr.stack, f)

	f.toFiber <- resumeMsg{err: exc}
	out := <-f.fromFiber
	if out.err != nil {
		return types.NewNull(), out.err
	}
	return out.val, nil
}

// ThrowValue reprend une fibre suspendue en injectant une exception représentée par une Value PHP.
func (f *Fiber) ThrowValue(z *zone.Zone, exc types.Value) (types.Value, error) {
	msg := "Uncaught exception thrown into fiber"
	deref := types.Deref(z, exc)
	if deref.Type == types.TypeString {
		msg = fmt.Sprintf("Uncaught exception: %s", types.StringData(z, deref))
	}
	return f.Throw(z, ThrownException{
		Exception: exc,
		Message:   msg,
	})
}

// GetReturn retourne la valeur finale d'une fibre terminée normalement.
func (f *Fiber) GetReturn(z *zone.Zone) (types.Value, error) {
	if z != f.z {
		return types.NewNull(), FiberError{Message: "Fiber zone mismatch"}
	}
	switch f.state {
	case StateUnstarted:
		return types.NewNull(), FiberError{Message: "Cannot get fiber return value: The fiber has not been started"}
	case StateRunning, StateSuspended:
		return types.NewNull(), FiberError{Message: "Cannot get fiber return value: The fiber has not terminated"}
	case StateTerminated:
		if f.uncaught || f.destroying {
			return types.NewNull(), FiberError{Message: "Cannot get fiber return value: The fiber threw an uncaught exception"}
		}
		return f.retVal, nil
	default:
		return types.NewNull(), FiberError{Message: "Cannot get fiber return value: The fiber has not terminated"}
	}
}

// Suspend suspend l'exécution de la fibre courante et transmet val à l'appelant de Start/Resume/Throw.
func Suspend(z *zone.Zone, val types.Value) (types.Value, error) {
	mgr := lookupManager(z)
	if mgr == nil || len(mgr.stack) == 0 {
		return types.NewNull(), FiberError{Message: "Cannot suspend outside of a fiber"}
	}
	current := mgr.stack[len(mgr.stack)-1]
	if current.destroying {
		return types.NewNull(), FiberError{Message: "Cannot suspend in a force-closed fiber"}
	}

	mgr.stack = mgr.stack[:len(mgr.stack)-1]
	current.state = StateSuspended

	// Envoi de la valeur suspendue et attente bloquante immédiate de la reprise.
	current.fromFiber <- yieldMsg{val: val}
	msg := <-current.toFiber
	if msg.abort {
		panic(fiberAbortToken{})
	}
	if msg.err != nil {
		return types.NewNull(), msg.err
	}
	return msg.val, nil
}

// GetCurrent retourne la fibre en cours d'exécution dans la Zone z, ou nil si sur la pile principale.
func GetCurrent(z *zone.Zone) *Fiber {
	mgr := lookupManager(z)
	if mgr == nil || len(mgr.stack) == 0 {
		return nil
	}
	return mgr.stack[len(mgr.stack)-1]
}

// GetCurrentObject retourne l'objet Value de la fibre courante, ou null si aucune fibre n'est active.
func GetCurrentObject(z *zone.Zone) types.Value {
	cur := GetCurrent(z)
	if cur == nil || cur.obj.IsUndef() {
		return types.NewNull()
	}
	return cur.obj
}

func (f *Fiber) cleanup(z *zone.Zone) {
	if f.closed {
		return
	}
	f.closed = true
	if f.state == StateSuspended {
		mgr := lookupManager(z)
		f.destroying = true
		f.state = StateRunning
		if mgr != nil {
			mgr.stack = append(mgr.stack, f)
		}
		f.toFiber <- resumeMsg{abort: true}
		<-f.done
	}
	if !f.retVal.IsUndef() {
		types.ReleaseValue(z, &f.retVal)
	}
}
