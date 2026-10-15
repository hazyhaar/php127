package vm

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"weak"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/datetime"
	"code.hazyhaar.fr/devhoros/php127/ext"
	"code.hazyhaar.fr/devhoros/php127/fiber"
	"code.hazyhaar.fr/devhoros/php127/mysqli"
	"code.hazyhaar.fr/devhoros/php127/pdo"
	"code.hazyhaar.fr/devhoros/php127/reflection"
	"code.hazyhaar.fr/devhoros/php127/sapi"
	"code.hazyhaar.fr/devhoros/php127/session"
	"code.hazyhaar.fr/devhoros/php127/spl"
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const maxFrames = 8192

type Context struct {
	over              map[*bytecode.OpArray]map[int]bytecode.Opcode
	strs              map[*bytecode.OpArray][]types.Value
	BlockTaintedSinks bool
	TaintedSinks      int
	// CLIArgv porte l'argv de la SAPI CLI (script tel que nommé, puis ses arguments) ; non nil, il fait
	// construire $_SERVER comme le fait PHP en ligne de commande. CLIInline signale un code fourni par -r
	// ou l'entrée standard.
	CLIArgv   []string
	CLIInline bool
	// INIOverrides porte les directives de la ligne de commande (-d clé=valeur) : valeurs maîtresses de la
	// requête, rendues par ini_get, rétablies par ini_restore et publiées en global_value par ini_get_all.
	INIOverrides map[string]string
	// ArchtimeImage porte l'image de bootstrap gelée dans la zone de processus (variables globales et constantes).
	ArchtimeImage *ArchtimeImage
}

func NewContext() *Context { return &Context{} }

func (c *Context) Specialize(fn *bytecode.OpArray, pc int, op bytecode.Opcode) {
	if c == nil || fn == nil {
		return
	}
	if c.over == nil {
		c.over = make(map[*bytecode.OpArray]map[int]bytecode.Opcode)
	}
	m := c.over[fn]
	if m == nil {
		m = make(map[int]bytecode.Opcode)
		c.over[fn] = m
	}
	m[pc] = op
}

func (c *Context) opcode(fn *bytecode.OpArray, pc int, fallback bytecode.Opcode) bytecode.Opcode {
	if c == nil || c.over == nil {
		return fallback
	}
	if m := c.over[fn]; m != nil {
		if op, ok := m[pc]; ok {
			return op
		}
	}
	return fallback
}

type iterState struct {
	gen     *generator
	iterObj types.Value
	keys    []types.Value
	vals    []types.Value
	index   int
	arrayCV int
	byRef   bool
	active  bool
}

const (
	contReturn = iota
	contThrow
	contJump
	contExit
)

type finState struct {
	tryIndex int
	kind     int
	val      types.Value
	target   int
	origin   int
	active   bool
}

type frame struct {
	gen           *generator
	fn            *bytecode.OpArray
	pc            int
	cvBase        int
	evalBase      int
	evalTop       int
	span          int
	calledClass   uint32
	scopeClass    uint32
	this          types.Value
	hasThis       bool
	inlineFrame   bool
	callLine      uint32
	iters         []iterState
	fin           []finState
	tryDepth      []int
	pending       types.Value
	hasPending    bool
	dynamicLocals map[string]types.Value
	args          []types.Value
	// includeScope vaut l'indice plus un du cadre dont un include ou un eval partage la portée ; zéro désigne une portée propre.
	includeScope int
	// silence compte les régions @ ouvertes dans ce cadre ; un catch ou un finally du même cadre le remet à zéro, l'opérateur @ ne portant que sur une expression.
	silence int
}

type namedArg struct {
	name string
	val  types.Value
}

type callKind uint8

const (
	callFunc callKind = iota
	callMethod
	callStatic
	callNew
	callClosure
)

type callSpec struct {
	kind       callKind
	funcIndex  int
	icPC       int32
	classID    uint32
	method     string
	obj        types.Value
	hasObj     bool
	pooledArgs bool
	// refResolved, refParams et refNative mémorisent la signature de l'appelé lue par funcArgByRef.
	refResolved bool
	refParams   []bytecode.ParamInfo
	refNative   string
	args        []types.Value
	named       []namedArg
	flags       uint16
	line        uint32
}

type closureData struct {
	fn   *bytecode.OpArray
	uses []types.Value
	live bool
}

type VM struct {
	Zone    *zone.Zone
	Out     io.Writer
	Err     io.Writer
	Program *bytecode.Program
	Context *Context

	inUse         atomic.Bool
	pooled        bool
	sessionActive bool
	lastOut       io.Writer
	lastErr       io.Writer
	defaultCtx    Context
	mainStrs      []types.Value
	reflProvider  vmReflectionProvider

	slots    []types.Value
	used     int
	frames   []frame
	pending  []callSpec
	argPool  [][]types.Value
	exit     int
	stop     bool
	control  bool
	line     uint32
	retVal   types.Value
	hasRet   bool
	thrown   types.Value
	hasThrow bool
	excTrace []traceItem

	closures       []closureData
	generators     []*generator
	enums          map[uint32][]types.Value
	statics        map[uint32][]types.Value
	localStatics   map[*bytecode.OpArray]map[uint32]types.Value
	globals        map[string]types.Value
	constCache     map[string]types.Value
	errorReporting int64
	included       map[string]bool
	progCloned     bool
	// origProgram retient le programme partagé remplacé par sa copie à la première fusion dynamique ; finishRequest le restaure.
	origProgram       *bytecode.Program
	superglobalsInit  bool
	lastProg          *bytecode.Program
	propCaches        [][]bytecode.PropIC
	callCaches        [][]bytecode.CallIC
	fiberCallerStack  []fiberStackSnap
	fiberSuspended    map[*fiber.Fiber]fiberStackSnap
	fiberCallbacks    map[*fiber.Fiber]types.Value
	magicGuard        map[magicKey]bool
	obStack           []*bytes.Buffer
	origOut           io.Writer
	errorHandlers     []types.Value
	exceptionHandlers []types.Value
	autoloaders       []types.Value
	arrayCursors      map[uint32]int

	// État de liaison dynamique des classes, propre à une requête et remis à zéro par finishRequest.
	classState     map[uint32]bool
	classIndex     map[uint32]*bytecode.ClassProto
	classNames     map[string]uint32
	classAliases   map[string]uint32
	ownedClasses   map[*bytecode.ClassProto]bool
	pendingClasses []pendingClass
	declOrder      []declEntry
	userConsts     []string
	autoloading    map[string]bool
	ifaceTried     map[string]bool
	mainDecl       *fileDecls
	mainDeclDone   bool
	funcSites      map[string]declSite
	funcNames      map[string]int
	nextClassID    uint32
	nextFuncID     uint32
	shutdownFuncs  []shutdownCall
	ini            map[string]string
	// mainReturned signale que le programme principal a atteint son retour, distinct d'un exit ou d'une erreur fatale.
	mainReturned bool
	// gcDisabled reflète gc_disable() pour gc_enabled() pendant la requête.
	gcDisabled bool
}

// pendingClass décrit une classe compilée dont le parent ou un trait n'est pas encore déclaré.
type pendingClass struct {
	id       uint32
	topLevel bool
	file     string
	line     uint32
}

// declEntry note une déclaration de classe liée dynamiquement ou un alias, dans l'ordre de get_declared_classes.
type declEntry struct {
	id    uint32
	alias string
}

// shutdownCall retient un rappel de register_shutdown_function et ses arguments.
type shutdownCall struct {
	cb   types.Value
	args []types.Value
}

type magicKey struct {
	slot uint32
	name string
}

func (vm *VM) resetInlineCachesIfProgramChanged() {
	if vm.lastProg == vm.Program && !vm.progCloned {
		return
	}
	vm.lastProg = vm.Program
	for i := range vm.propCaches {
		clear(vm.propCaches[i])
		vm.propCaches[i] = vm.propCaches[i][:0]
	}
	for i := range vm.callCaches {
		clear(vm.callCaches[i])
		vm.callCaches[i] = vm.callCaches[i][:0]
	}
}

func (vm *VM) propCache(fn *bytecode.OpArray) []bytecode.PropIC {
	if fn == nil || fn.FuncID == 0 || int(fn.FuncID) >= len(vm.propCaches) {
		return nil
	}
	return vm.propCaches[fn.FuncID]
}

func (vm *VM) ensurePropCache(fn *bytecode.OpArray) []bytecode.PropIC {
	if fn == nil || fn.FuncID == 0 {
		return nil
	}
	id := int(fn.FuncID)
	if id >= len(vm.propCaches) {
		next := make([][]bytecode.PropIC, max(id+1, len(vm.propCaches)*2, 8))
		copy(next, vm.propCaches)
		vm.propCaches = next
	}
	c := vm.propCaches[id]
	if len(c) != len(fn.Code) {
		if cap(c) >= len(fn.Code) {
			c = c[:len(fn.Code)]
			clear(c)
		} else {
			c = make([]bytecode.PropIC, len(fn.Code))
		}
		vm.propCaches[id] = c
	}
	return c
}

func (vm *VM) callCache(fn *bytecode.OpArray) []bytecode.CallIC {
	if fn == nil || fn.FuncID == 0 || int(fn.FuncID) >= len(vm.callCaches) {
		return nil
	}
	return vm.callCaches[fn.FuncID]
}

func (vm *VM) ensureCallCache(fn *bytecode.OpArray) []bytecode.CallIC {
	if fn == nil || fn.FuncID == 0 {
		return nil
	}
	id := int(fn.FuncID)
	if id >= len(vm.callCaches) {
		next := make([][]bytecode.CallIC, max(id+1, len(vm.callCaches)*2, 8))
		copy(next, vm.callCaches)
		vm.callCaches = next
	}
	c := vm.callCaches[id]
	if len(c) != len(fn.Code) {
		if cap(c) >= len(fn.Code) {
			c = c[:len(fn.Code)]
			clear(c)
		} else {
			c = make([]bytecode.CallIC, len(fn.Code))
		}
		vm.callCaches[id] = c
	}
	return c
}

// zoneVM associe une zone à la VM qui l'exécute, le temps d'une requête seulement : le crochet de destructeur y
// retrouve la VM. L'entrée est retirée à la sortie d'Execute, aucune zone n'y reste attachée entre deux requêtes.
var zoneVM sync.Map

// idleVM garde au repos la VM réservée à une zone entre deux requêtes, sous une clé faible : la VM au repos ne
// référence plus sa zone, l'entrée ne retient donc pas la zone, et un nettoyage d'exécution la retire lorsque la
// zone est collectée.
var idleVM sync.Map

func init() {
	types.SetObjectDestructor(func(z *zone.Zone, v types.Value) {
		if raw, ok := zoneVM.Load(z); ok {
			raw.(*VM).runDestructor(v)
		}
	})
}

func New(z *zone.Zone, stdout, stderr io.Writer, prog *bytecode.Program, ctx *Context) *VM {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	vm := &VM{
		Zone: z, Out: stdout, Err: stderr, Program: prog,
		slots:          make([]types.Value, 256),
		frames:         make([]frame, 0, 32),
		pending:        make([]callSpec, 0, 16),
		globals:        make(map[string]types.Value, 8),
		errorReporting: 32767,
	}
	if ctx == nil {
		vm.Context = &vm.defaultCtx
	} else {
		vm.Context = ctx
	}
	return vm
}

func Execute(z *zone.Zone, stdout, stderr io.Writer, prog *bytecode.Program, ctx *Context) int {
	if z == nil {
		return New(z, stdout, stderr, prog, ctx).Execute()
	}
	key := weak.Make(z)
	if raw, ok := idleVM.Load(key); ok {
		vm := raw.(*VM)
		if !vm.inUse.CompareAndSwap(false, true) {
			return New(z, stdout, stderr, prog, ctx).Execute()
		}
		if stdout == nil {
			stdout = io.Discard
		}
		if stderr == nil {
			stderr = io.Discard
		}
		vm.Zone = z
		vm.Out = stdout
		vm.Err = stderr
		vm.Program = prog
		if ctx == nil {
			vm.Context = &vm.defaultCtx
		} else {
			vm.Context = ctx
		}
		vm.exit = 0
		vm.stop = false
		vm.control = false
		vm.errorReporting = 32767
		vm.progCloned = false
		vm.superglobalsInit = false
		vm.sessionActive = false
		vm.dropDynamicState()
		return vm.executePooled()
	}
	vm := New(z, stdout, stderr, prog, ctx)
	vm.pooled = true
	vm.inUse.Store(true)
	if _, loaded := idleVM.LoadOrStore(key, vm); loaded {
		// Une autre requête a réservé une VM à cette zone entre-temps : celle-ci s'exécute hors réserve.
		vm.pooled = false
		vm.inUse.Store(false)
		return vm.Execute()
	}
	runtime.AddCleanup(z, func(k weak.Pointer[zone.Zone]) { idleVM.Delete(k) }, key)
	return vm.executePooled()
}

func (vm *VM) executePooled() (res int) {
	z := vm.Zone
	zoneVM.Store(z, vm)
	defer func() {
		if r := recover(); r != nil {
			if vm.Out != nil {
				_, _ = fmt.Fprintf(vm.Out, "\nFatal error: internal error: %v\n", r)
			}
			vm.stop = true
			vm.exit = 255
			vm.finishAfterPanic()
			res = 255
		}
		zoneVM.CompareAndDelete(z, vm)
		// La VM au repos lâche sa zone et ses flux de sortie : la réserve idleVM ne les retient pas.
		vm.Zone = nil
		vm.Out = nil
		vm.Err = nil
		vm.Context = nil
		vm.inUse.Store(false)
	}()
	if vm.Program == nil || vm.Program.Main == nil {
		vm.fatalFeature("programme")
		return vm.exit
	}
	vm.resetInlineCachesIfProgramChanged()
	if vm.Zone != nil {
		if vm.Out != vm.lastOut {
			streams.SetOutput(vm.Zone, vm.Out)
			vm.lastOut = vm.Out
		}
		if vm.Err != vm.lastErr {
			streams.SetStderr(vm.Zone, vm.Err)
			vm.lastErr = vm.Err
		}
	}
	vm.applyINIOverrides()
	vm.applyArchtimeImage()
	if vm.Program.NeedsSuperglobals || !vm.Program.Main.CVMetaResolved {
		vm.initSuperglobals()
		vm.superglobalsInit = true
	}
	vm.runMain()
	if !vm.stop {
		vm.exit = 0
	}
	vm.runShutdownFunctions()
	vm.finishRequest()
	res = vm.exit
	return res
}

func (vm *VM) Execute() (res int) {
	defer func() {
		if r := recover(); r != nil {
			if vm.Out != nil {
				_, _ = fmt.Fprintf(vm.Out, "\nFatal error: internal error: %v\n", r)
			}
			vm.stop = true
			vm.exit = 255
			vm.finishAfterPanic()
			res = 255
		}
	}()
	if vm.Program == nil || vm.Program.Main == nil {
		vm.fatalFeature("programme")
		return vm.exit
	}
	vm.resetInlineCachesIfProgramChanged()
	if vm.Zone != nil {
		if vm.Out != vm.lastOut {
			streams.SetOutput(vm.Zone, vm.Out)
			vm.lastOut = vm.Out
		}
		if vm.Err != vm.lastErr {
			streams.SetStderr(vm.Zone, vm.Err)
			vm.lastErr = vm.Err
		}
	}
	var prev any
	var hadPrev bool
	if vm.Zone != nil {
		prev, hadPrev = zoneVM.Swap(vm.Zone, vm)
		defer func() {
			if hadPrev {
				zoneVM.Store(vm.Zone, prev)
			} else {
				zoneVM.Delete(vm.Zone)
			}
		}()
	}
	vm.applyINIOverrides()
	vm.applyArchtimeImage()
	if vm.Program.NeedsSuperglobals || !vm.Program.Main.CVMetaResolved {
		vm.initSuperglobals()
		vm.superglobalsInit = true
	}
	vm.runMain()
	if !vm.stop {
		vm.exit = 0
	}
	vm.runShutdownFunctions()
	vm.finishRequest()
	return vm.exit
}

// runMain exécute le programme principal : contrôle préalable des classes qu'il déclare, $argv et $argc de la
// SAPI CLI posés dans sa portée, puis, à son retour, liaison ou erreur des classes restées en attente.
func (vm *VM) runMain() {
	main := vm.Program.Main
	vm.mainReturned = false
	if !vm.checkMainClasses() {
		return
	}
	if vm.Context != nil && (vm.Context.CLIArgv != nil || vm.Context.ArchtimeImage != nil) {
		// Le programme principal ne reçoit aucun argument : le cadre général suffit et laisse poser $argv, $argc et l'image ARCHTIME avant la première instruction.
		if !vm.pushFrame(main, 0, 0, types.Value{}, false, 0) {
			return
		}
		vm.seedRootGlobals()
		vm.seedCLIGlobals()
		vm.execFrames(len(vm.frames) - 1)
		v, _ := vm.takeReturn()
		vmRelease(vm.Zone, &v)
	} else {
		v, _ := vm.invokeOpArray(main, types.Value{}, false, nil, 0, 0, 0)
		vmRelease(vm.Zone, &v)
	}
	vm.settleMainPending()
}

// finishAfterPanic rejoue, après une panique récupérée, les rappels d'arrêt puis la fin de requête qui libère les ressources de la zone ; une seconde panique dans l'une ou l'autre étape est absorbée pour que la suivante s'exécute.
func (vm *VM) finishAfterPanic() {
	exit := vm.exit
	func() {
		defer func() { _ = recover() }()
		vm.runShutdownFunctions()
	}()
	func() {
		defer func() { _ = recover() }()
		vm.finishRequest()
	}()
	vm.exit = exit
}

func (vm *VM) runZoneCleanups() {
	vm.Zone.RunCleanups(func(item zone.CleanupItem) {
		fiber.DispatchCleanup(vm.Zone, item)
		spl.DispatchCleanup(vm.Zone, item)
		datetime.DispatchCleanup(vm.Zone, item)
		reflection.DispatchCleanup(vm.Zone, item)
		session.DispatchCleanup(vm.Zone, item)
		streams.DispatchCleanup(item)
		pdo.DispatchCleanup(item)
		if item.Kind == mysqli.CleanupKind {
			mysqli.DispatchZoneCleanup(vm.Zone, item)
		}
		stdlib.DispatchCleanup(vm.Zone, item)
		ext.DispatchCleanup(vm.Zone, item)
		types.DispatchObjectDestructor(vm.Zone, item)
	})
}

func (vm *VM) finishRequest() {
	if vm.Zone == nil {
		vm.dropDynamicState()
		vm.restoreProgram()
		return
	}
	vm.stop = false
	vm.control = false
	vm.syncSessionBeforeClose()
	if len(vm.fiberCallbacks) > 0 || len(vm.fiberSuspended) > 0 || len(vm.fiberCallerStack) > 0 {
		for f, obj := range vm.fiberCallbacks {
			v := obj
			types.ReleaseValue(vm.Zone, &v)
			delete(vm.fiberCallbacks, f)
		}
		for f, snap := range vm.fiberSuspended {
			vm.releaseStackSnap(&snap)
			delete(vm.fiberSuspended, f)
		}
		for i := range vm.fiberCallerStack {
			vm.releaseStackSnap(&vm.fiberCallerStack[i])
		}
		vm.fiberCallerStack = nil
		fiber.CleanupZone(vm.Zone)
	}
	if vm.Zone.PendingCleanups() > 0 {
		vm.runZoneCleanups()
	}
	if vm.superglobalsInit || vm.Program == nil || vm.Program.NeedsSuperglobals || !vm.Program.Main.CVMetaResolved {
		sapi.BindContext(vm.Zone, nil)
	}
	vm.superglobalsInit = false
	for len(vm.frames) > 0 {
		vm.releaseFrameValues()
		if len(vm.frames) > 0 {
			fr := vm.frames[len(vm.frames)-1]
			clear(vm.slots[fr.cvBase : fr.cvBase+fr.span])
			vm.popFrame()
		}
	}
	if len(vm.pending) > 0 {
		for _, sp := range vm.pending {
			vm.releaseArgs(sp.args)
			vm.releaseSpecObj(sp)
		}
		vm.pending = vm.pending[:0]
	}
	if vm.hasRet {
		vmRelease(vm.Zone, &vm.retVal)
		vm.hasRet = false
	}
	if vm.hasThrow {
		vmRelease(vm.Zone, &vm.thrown)
		vm.hasThrow = false
	}
	vm.excTrace = nil
	if len(vm.statics) > 0 {
		for id, slots := range vm.statics {
			for i := range slots {
				types.ReleaseValue(vm.Zone, &slots[i])
			}
			delete(vm.statics, id)
		}
	}
	if len(vm.enums) > 0 {
		for id, cases := range vm.enums {
			vm.releaseArgs(cases)
			delete(vm.enums, id)
		}
	}
	if len(vm.localStatics) > 0 {
		for fn, values := range vm.localStatics {
			for idx, v := range values {
				types.ReleaseValue(vm.Zone, &v)
				delete(values, idx)
			}
			delete(vm.localStatics, fn)
		}
	}
	if len(vm.globals) > 0 {
		for name, v := range vm.globals {
			val := v
			types.ReleaseValue(vm.Zone, &val)
			delete(vm.globals, name)
		}
	}
	if len(vm.constCache) > 0 {
		for name, v := range vm.constCache {
			val := v
			types.ReleaseValue(vm.Zone, &val)
			delete(vm.constCache, name)
		}
	}
	if len(vm.mainStrs) > 0 {
		for i := range vm.mainStrs {
			if vm.mainStrs[i].Type != types.TypeUndef {
				types.ReleaseValue(vm.Zone, &vm.mainStrs[i])
			}
		}
		vm.mainStrs = vm.mainStrs[:0]
	}
	if vm.Context != nil && len(vm.Context.strs) > 0 {
		for fn, cache := range vm.Context.strs {
			for i := range cache {
				if cache[i].Type != types.TypeUndef {
					types.ReleaseValue(vm.Zone, &cache[i])
				}
			}
			vm.Context.strs[fn] = cache[:0]
		}
	}
	if len(vm.closures) > 0 {
		for i := range vm.closures {
			vm.releaseClosure(i)
		}
		vm.closures = vm.closures[:0]
	}
	if len(vm.generators) > 0 {
		for _, g := range vm.generators {
			vm.releaseGenerator(g)
		}
		vm.generators = vm.generators[:0]
	}
	if len(vm.included) > 0 {
		clear(vm.included)
	}
	if len(vm.magicGuard) > 0 {
		clear(vm.magicGuard)
	}
	if len(vm.arrayCursors) > 0 {
		clear(vm.arrayCursors)
	}
	if len(vm.obStack) > 0 {
		if vm.origOut != nil {
			for _, v := range slices.Backward(vm.obStack) {
				_, _ = vm.origOut.Write(v.Bytes())
			}
			vm.Out = vm.origOut
			vm.origOut = nil
		}
		vm.obStack = nil
	}
	for i := range vm.errorHandlers {
		vmRelease(vm.Zone, &vm.errorHandlers[i])
	}
	vm.errorHandlers = nil
	for i := range vm.exceptionHandlers {
		vmRelease(vm.Zone, &vm.exceptionHandlers[i])
	}
	vm.exceptionHandlers = nil
	for i := range vm.autoloaders {
		vmRelease(vm.Zone, &vm.autoloaders[i])
	}
	vm.autoloaders = nil
	for i := range vm.shutdownFuncs {
		vmRelease(vm.Zone, &vm.shutdownFuncs[i].cb)
		vm.releaseArgs(vm.shutdownFuncs[i].args)
	}
	stdlib.ResetSysState(vm.Zone)
	vm.dropDynamicState()
	vm.restoreProgram()
}

// restoreProgram remet en place le programme partagé d'origine : la copie construite par cloneProgram porte les classes et fonctions des fichiers inclus et ne doit pas survivre à la requête.
func (vm *VM) restoreProgram() {
	if !vm.progCloned {
		return
	}
	if vm.origProgram != nil {
		vm.Program = vm.origProgram
	}
	vm.origProgram = nil
	vm.progCloned = false
	vm.lastProg = nil
}

// forwardThis transmet $this à un appel parent::, self::, static:: ou Classe:: émis depuis une méthode d'instance dont l'objet appartient à la classe visée, comme PHP pour une méthode non statique.
func (vm *VM) forwardThis(in *bytecode.Instr) {
	if in.Flags&bytecode.FlagUseThis != 0 || len(vm.frames) == 0 {
		return
	}
	fr := vm.frame()
	if !fr.hasThis {
		return
	}
	target := in.Op2
	switch {
	case in.Flags&bytecode.FlagParent != 0:
		target = vm.parentOf(fr.scopeClass)
	case in.Flags&bytecode.FlagSelf != 0:
		target = fr.scopeClass
	case in.Flags&bytecode.FlagStatic != 0:
		target = fr.calledClass
	}
	this := types.Deref(vm.Zone, fr.this)
	if this.Type != types.TypeObject || target == bytecode.ClassNone {
		return
	}
	if !vm.isInstance(types.ObjectInfo(vm.Zone, this).ClassID, target) {
		return
	}
	if int(in.Op1) < len(fr.fn.Strings) {
		if raw := fr.fn.Strings[in.Op1]; !strings.HasPrefix(raw, "\x00") {
			if m, _, ok := vm.findMethod(target, raw); ok && m.Static {
				return
			}
		}
	}
	in.Flags |= bytecode.FlagUseThis
}

// dropDynamicState oublie l'état de liaison dynamique sans libérer de valeur de zone ; finishRequest libère au préalable les valeurs retenues.
func (vm *VM) dropDynamicState() {
	vm.classState = nil
	vm.classIndex = nil
	vm.classNames = nil
	vm.classAliases = nil
	vm.ownedClasses = nil
	vm.pendingClasses = nil
	vm.declOrder = nil
	vm.userConsts = nil
	vm.autoloading = nil
	vm.ifaceTried = nil
	vm.mainDecl = nil
	vm.mainDeclDone = false
	vm.funcSites = nil
	vm.funcNames = nil
	vm.nextClassID = 0
	vm.nextFuncID = 0
	vm.shutdownFuncs = nil
	vm.ini = nil
	vm.gcDisabled = false
}

// runShutdownFunctions exécute en ordre FIFO les rappels de register_shutdown_function, y compris après exit ou une erreur fatale ; un exit dans un rappel interrompt la suite.
func (vm *VM) runShutdownFunctions() {
	if len(vm.shutdownFuncs) == 0 || vm.Zone == nil {
		return
	}
	exit := vm.exit
	vm.stop = false
	vm.control = false
	for len(vm.frames) > 1 {
		vm.releaseFrameValues()
		if len(vm.frames) > 1 {
			fr := vm.frames[len(vm.frames)-1]
			clear(vm.slots[fr.cvBase : fr.cvBase+fr.span])
			vm.popFrame()
		}
	}
	if len(vm.frames) == 0 {
		// Le cadre racine a été dépilé par une exception non rattrapée : un cadre neutre tient lieu de portée principale pour les rappels.
		root := &bytecode.OpArray{Name: "{main}", Code: []bytecode.Instr{{Op: bytecode.OpReturn, Flags: bytecode.FlagReturnVoid}}, Aux: []uint32{bytecode.NoCV}, CVMetaResolved: true}
		if vm.Program != nil && vm.Program.Main != nil {
			root.Filename = vm.Program.Main.Filename
		}
		if !vm.pushFrame(root, 0, 0, types.Value{}, false, 0) {
			return
		}
	}
	for _, sp := range vm.pending {
		vm.releaseArgs(sp.args)
		vm.releaseSpecObj(sp)
	}
	vm.pending = vm.pending[:0]
	if vm.hasThrow {
		vmRelease(vm.Zone, &vm.thrown)
		vm.hasThrow = false
	}
	for i := 0; i < len(vm.shutdownFuncs); i++ {
		entry := vm.shutdownFuncs[i]
		vm.shutdownFuncs[i] = shutdownCall{}
		call, ok := vm.callback(entry.cb)
		if !ok {
			vmRelease(vm.Zone, &entry.cb)
			vm.releaseArgs(entry.args)
			continue
		}
		ret, threw := vm.invokeCallback(call, entry.args, 0)
		vmRelease(vm.Zone, &ret)
		vmRelease(vm.Zone, &entry.cb)
		if vm.stop || threw {
			for _, rest := range vm.shutdownFuncs[i+1:] {
				vmRelease(vm.Zone, &rest.cb)
				vm.releaseArgs(rest.args)
			}
			vm.shutdownFuncs = nil
			return
		}
	}
	vm.shutdownFuncs = nil
	vm.exit = exit
}

func (vm *VM) frame() *frame {
	return &vm.frames[len(vm.frames)-1]
}

func (vm *VM) pushFastFrame(fn *bytecode.OpArray, called, scope uint32, this types.Value, hasThis bool, callLine uint32, inline bool) bool {
	if len(vm.frames) >= maxFrames {
		vm.fatalFeature("profondeur d'appel")
		return false
	}
	ncv := fn.NumCVs
	span := ncv + fn.MaxStack
	if vm.used+span > len(vm.slots) {
		capNeeded := max(vm.used+span, max(len(vm.slots)*2, 64))
		next := make([]types.Value, capNeeded)
		copy(next, vm.slots[:vm.used])
		vm.slots = next
	}
	base := vm.used
	vm.used += span
	clear(vm.slots[base : base+span])
	var tryDepth []int
	if len(fn.TryTable) > 0 {
		tryDepth = make([]int, len(fn.TryTable))
		for i := range tryDepth {
			tryDepth[i] = -1
		}
	}
	vm.frames = append(vm.frames, frame{
		fn: fn, pc: 0, cvBase: base, evalBase: base + ncv, evalTop: base + ncv, span: span,
		calledClass: called, scopeClass: scope, this: this, hasThis: hasThis, inlineFrame: inline, callLine: callLine,
		tryDepth: tryDepth,
	})
	return true
}

func (vm *VM) pushFrame(fn *bytecode.OpArray, called, scope uint32, this types.Value, hasThis bool, callLine uint32) bool {
	if fn == nil {
		vm.fatalFeature("fonction")
		return false
	}
	if len(vm.frames) >= maxFrames {
		vm.fatalFeature("profondeur d'appel")
		return false
	}
	ncv := max(fn.NumCVs, 0)
	ms := max(fn.MaxStack, 0)
	span := ncv + ms
	if vm.used+span > len(vm.slots) {
		capNeeded := max(vm.used+span, max(len(vm.slots)*2, 64))
		next := make([]types.Value, capNeeded)
		copy(next, vm.slots[:vm.used])
		vm.slots = next
	}
	base := vm.used
	vm.used += span
	clear(vm.slots[base : base+span])
	var tryDepth []int
	if len(fn.TryTable) > 0 {
		tryDepth = make([]int, len(fn.TryTable))
		for i := range tryDepth {
			tryDepth[i] = -1
		}
	}
	if len(fn.SuperglobalCVs) > 0 {
		for _, idx := range fn.SuperglobalCVs {
			if int(idx) < len(fn.CVNames) {
				ref := vm.superglobalRef(fn.CVNames[idx])
				if ref.Type == types.TypeReference {
					vm.slots[base+int(idx)] = types.RetainReference(vm.Zone, ref)
				}
			}
		}
	} else if !fn.CVMetaResolved && len(fn.CVNames) > 0 {
		for i, name := range fn.CVNames {
			if isSuperglobal(name) {
				ref := vm.superglobalRef(name)
				if ref.Type == types.TypeReference {
					vm.slots[base+i] = types.RetainReference(vm.Zone, ref)
				}
			}
		}
	}
	fr := frame{
		fn: fn, pc: 0, cvBase: base, evalBase: base + ncv, evalTop: base + ncv, span: span,
		calledClass: called, scopeClass: scope, this: this, hasThis: hasThis, callLine: callLine,
		tryDepth: tryDepth,
	}
	vm.frames = append(vm.frames, fr)
	return true
}

func (vm *VM) popFrame() {
	if len(vm.frames) == 0 {
		return
	}
	fr := vm.frames[len(vm.frames)-1]
	if vm.used == fr.cvBase+fr.span {
		vm.used = fr.cvBase
	}
	vm.frames = vm.frames[:len(vm.frames)-1]
	vm.checkSlotsAfterPop(fr.cvBase, fr.span)
}

func (vm *VM) releaseFrameValues() {
	if len(vm.frames) == 0 {
		return
	}
	frameIdx := len(vm.frames) - 1
	if len(vm.frames[frameIdx].dynamicLocals) > 0 {
		locals := vm.frames[frameIdx].dynamicLocals
		vm.frames[frameIdx].dynamicLocals = nil
		for name, v := range locals {
			delete(locals, name)
			vmRelease(vm.Zone, &v)
			if vm.stop || vm.hasThrow || len(vm.frames) <= frameIdx {
				return
			}
		}
	}
	if len(vm.frames[frameIdx].args) > 0 {
		args := vm.frames[frameIdx].args
		vm.frames[frameIdx].args = nil
		vm.releaseArgs(args)
		if vm.stop || vm.hasThrow || len(vm.frames) <= frameIdx {
			return
		}
	}
	for vm.frames[frameIdx].evalTop > vm.frames[frameIdx].evalBase {
		vm.frames[frameIdx].evalTop--
		idx := vm.frames[frameIdx].evalTop
		old := vm.slots[idx]
		vm.slots[idx] = types.Value{}
		if old.Type > types.TypeDouble && old.Type != types.TypeResource {
			types.ReleaseValue(vm.Zone, &old)
			if vm.stop || vm.hasThrow || len(vm.frames) <= frameIdx {
				return
			}
		}
	}
	for i := 0; i < len(vm.frames[frameIdx].iters); i++ {
		it := vm.frames[frameIdx].iters[i]
		vm.frames[frameIdx].iters[i] = iterState{}
		vm.freeIter(&it)
		if vm.stop || vm.hasThrow || len(vm.frames) <= frameIdx {
			return
		}
	}
	cvBase := vm.frames[frameIdx].cvBase
	ncv := vm.frames[frameIdx].fn.NumCVs
	for i := range ncv {
		old := vm.slots[cvBase+i]
		vm.slots[cvBase+i] = types.Value{}
		if old.Type > types.TypeDouble && old.Type != types.TypeResource {
			types.ReleaseValue(vm.Zone, &old)
			if vm.stop || vm.hasThrow || len(vm.frames) <= frameIdx {
				return
			}
		}
	}
	for i := 0; i < len(vm.frames[frameIdx].fin); i++ {
		old := vm.frames[frameIdx].fin[i].val
		vm.frames[frameIdx].fin[i].val = types.Value{}
		vmRelease(vm.Zone, &old)
		if vm.stop || vm.hasThrow || len(vm.frames) <= frameIdx {
			return
		}
	}
	if vm.frames[frameIdx].hasPending {
		old := vm.frames[frameIdx].pending
		vm.frames[frameIdx].pending = types.Value{}
		vm.frames[frameIdx].hasPending = false
		vmRelease(vm.Zone, &old)
	}
}

func (vm *VM) push(v types.Value) {
	fr := vm.frame()
	if fr.evalTop >= fr.evalBase+fr.fn.MaxStack {
		vm.fatalFeature("dépassement de pile")
		vmRelease(vm.Zone, &v)
		return
	}
	vm.slots[fr.evalTop] = v
	fr.evalTop++
}

func (vm *VM) pop() types.Value {
	fr := vm.frame()
	if fr.evalTop <= fr.evalBase {
		vm.fatalFeature("sous-dépassement de pile")
		return types.NewUndef()
	}
	fr.evalTop--
	v := vm.slots[fr.evalTop]
	vm.slots[fr.evalTop] = types.Value{}
	return v
}

func (vm *VM) cvPtr(idx uint32) *types.Value {
	fr := vm.frame()
	return &vm.slots[fr.cvBase+int(idx)]
}

func (vm *VM) bump() {
	if vm.stop || vm.control || len(vm.frames) == 0 {
		return
	}
	vm.frame().pc++
}

func (vm *VM) execFrames(base int) {
	for len(vm.frames) > base && !vm.stop {
		vm.control = false
		fr := &vm.frames[len(vm.frames)-1]
		if fr.pc < 0 || fr.pc >= len(fr.fn.Code) {
			vm.fatalFeature("compteur ordinal hors code")
			return
		}
		if len(fr.fn.TryTable) > 0 {
			vm.noteTry(fr.pc)
		}
		in := fr.fn.Code[fr.pc]
		vm.line = in.Line
		op := in.Op
		hasOver := vm.Context != nil && len(vm.Context.over) > 0
		if hasOver {
			op = vm.Context.opcode(fr.fn, fr.pc, op)
		}
		switch op {
		case bytecode.OpLoadCV:
			if in.Flags == 0 && !fr.fn.HasDynamicCV && (!fr.fn.HasGlobalsCV || in.Op1 != fr.fn.GlobalsCV) && fr.fn.CVMetaResolved {
				slot := vm.slots[fr.cvBase+int(in.Op1)]
				if (slot.Type == types.TypeLong || slot.Type == types.TypeDouble) && !hasOver && len(fr.fn.TryTable) == 0 && fr.pc+2 < len(fr.fn.Code) {
					in1 := fr.fn.Code[fr.pc+1]
					var r types.Value
					rOK := false
					if in1.Op == bytecode.OpLoadCV && in1.Flags == 0 && (!fr.fn.HasGlobalsCV || in1.Op1 != fr.fn.GlobalsCV) {
						rv := vm.slots[fr.cvBase+int(in1.Op1)]
						if rv.Type == types.TypeLong || rv.Type == types.TypeDouble {
							r = rv
							rOK = true
						}
					} else if in1.Op == bytecode.OpConst && in1.Flags == 0 {
						rv := fr.fn.Consts[in1.Op1]
						if rv.Type == types.TypeLong || rv.Type == types.TypeDouble {
							r = rv
							rOK = true
						}
					}
					if rOK {
						in2 := fr.fn.Code[fr.pc+2]
						switch in2.Op {
						case bytecode.OpIsSmaller, bytecode.OpIsSmallerOrEqual, bytecode.OpIsEqual, bytecode.OpIsIdentical, bytecode.OpIsNotEqual, bytecode.OpIsNotIdentical:
							if fr.pc+3 < len(fr.fn.Code) {
								in3 := fr.fn.Code[fr.pc+3]
								if in3.Op == bytecode.OpJmpZ || in3.Op == bytecode.OpJmpNZ {
									var res bool
									if slot.Type == types.TypeLong && r.Type == types.TypeLong {
										switch in2.Op {
										case bytecode.OpIsSmaller:
											res = int64(slot.Scalar) < int64(r.Scalar)
										case bytecode.OpIsSmallerOrEqual:
											res = int64(slot.Scalar) <= int64(r.Scalar)
										case bytecode.OpIsEqual, bytecode.OpIsIdentical:
											res = slot.Scalar == r.Scalar
										default:
											res = slot.Scalar != r.Scalar
										}
									} else {
										lf, rf := slot.ToDouble(), r.ToDouble()
										switch in2.Op {
										case bytecode.OpIsSmaller:
											res = lf < rf
										case bytecode.OpIsSmallerOrEqual:
											res = lf <= rf
										case bytecode.OpIsEqual:
											res = lf == rf
										case bytecode.OpIsIdentical:
											res = slot.Type == r.Type && lf == rf
										case bytecode.OpIsNotIdentical:
											res = slot.Type != r.Type || lf != rf
										default:
											res = lf != rf
										}
									}
									if in3.Op == bytecode.OpJmpZ {
										if !res {
											fr.pc = int(in3.Op1)
										} else {
											fr.pc += 4
										}
									} else {
										if res {
											fr.pc = int(in3.Op1)
										} else {
											fr.pc += 4
										}
									}
									continue
								}
							}
						case bytecode.OpAdd:
							res := types.Add(slot, r)
							if fr.pc+3 < len(fr.fn.Code) && fr.fn.Code[fr.pc+3].Op == bytecode.OpStoreCV && fr.fn.Code[fr.pc+3].Flags == 0 {
								dst := &vm.slots[fr.cvBase+int(fr.fn.Code[fr.pc+3].Op1)]
								if dst.Type <= types.TypeDouble {
									*dst = res
									fr.pc += 4
									continue
								}
							}
							if fr.evalTop < fr.evalBase+fr.fn.MaxStack {
								vm.slots[fr.evalTop] = res
								fr.evalTop++
								fr.pc += 3
								continue
							}
						case bytecode.OpSub:
							res := types.Sub(slot, r)
							if fr.pc+3 < len(fr.fn.Code) && fr.fn.Code[fr.pc+3].Op == bytecode.OpStoreCV && fr.fn.Code[fr.pc+3].Flags == 0 {
								dst := &vm.slots[fr.cvBase+int(fr.fn.Code[fr.pc+3].Op1)]
								if dst.Type <= types.TypeDouble {
									*dst = res
									fr.pc += 4
									continue
								}
							}
							if fr.evalTop < fr.evalBase+fr.fn.MaxStack {
								vm.slots[fr.evalTop] = res
								fr.evalTop++
								fr.pc += 3
								continue
							}
						case bytecode.OpMul:
							res := types.Mul(slot, r)
							if fr.pc+3 < len(fr.fn.Code) && fr.fn.Code[fr.pc+3].Op == bytecode.OpStoreCV && fr.fn.Code[fr.pc+3].Flags == 0 {
								dst := &vm.slots[fr.cvBase+int(fr.fn.Code[fr.pc+3].Op1)]
								if dst.Type <= types.TypeDouble {
									*dst = res
									fr.pc += 4
									continue
								}
							}
							if fr.evalTop < fr.evalBase+fr.fn.MaxStack {
								vm.slots[fr.evalTop] = res
								fr.evalTop++
								fr.pc += 3
								continue
							}
						}
					}
				}
				if slot.Type <= types.TypeDouble && fr.evalTop < fr.evalBase+fr.fn.MaxStack {
					if slot.Type == types.TypeUndef {
						vm.slots[fr.evalTop] = types.NewNull()
					} else {
						vm.slots[fr.evalTop] = slot
					}
					fr.evalTop++
					fr.pc++
					continue
				}
			}
			vm.opLoadCV(in)
		case bytecode.OpStoreCV:
			if in.Flags == 0 && !fr.fn.HasDynamicCV && fr.evalTop > fr.evalBase {
				v := vm.slots[fr.evalTop-1]
				slot := &vm.slots[fr.cvBase+int(in.Op1)]
				if v.Type <= types.TypeDouble && slot.Type <= types.TypeDouble {
					fr.evalTop--
					vm.slots[fr.evalTop] = types.Value{}
					*slot = v
					fr.pc++
					continue
				}
			}
			vm.opStore(in.Op1, false, in.Flags)
		case bytecode.OpAssignCV:
			if in.Flags == 0 && !fr.fn.HasDynamicCV && fr.evalTop > fr.evalBase {
				v := vm.slots[fr.evalTop-1]
				slot := &vm.slots[fr.cvBase+int(in.Op1)]
				if v.Type <= types.TypeDouble && slot.Type <= types.TypeDouble {
					*slot = v
					fr.pc++
					continue
				}
			}
			vm.opStore(in.Op1, true, in.Flags)
		case bytecode.OpConst:
			if in.Flags == 0 {
				c := fr.fn.Consts[in.Op1]
				if c.Type <= types.TypeDouble && fr.evalTop < fr.evalBase+fr.fn.MaxStack {
					vm.slots[fr.evalTop] = c
					fr.evalTop++
					fr.pc++
					continue
				}
			}
			vm.opConst(in)
		case bytecode.OpPop:
			if fr.evalTop > fr.evalBase {
				top := fr.evalTop - 1
				v := vm.slots[top]
				if v.Type <= types.TypeDouble {
					fr.evalTop = top
					vm.slots[top] = types.Value{}
					fr.pc++
					continue
				}
			}
			v := vm.pop()
			vmRelease(vm.Zone, &v)
			vm.bump()
		case bytecode.OpAdd:
			if fr.evalTop-fr.evalBase >= 2 {
				r := vm.slots[fr.evalTop-1]
				l := vm.slots[fr.evalTop-2]
				if (l.Type == types.TypeLong || l.Type == types.TypeDouble) && (r.Type == types.TypeLong || r.Type == types.TypeDouble) {
					fr.evalTop--
					vm.slots[fr.evalTop] = types.Value{}
					vm.slots[fr.evalTop-1] = types.Add(l, r)
					fr.pc++
					continue
				}
			}
			vm.opBinary(op)
		case bytecode.OpSub:
			if fr.evalTop-fr.evalBase >= 2 {
				r := vm.slots[fr.evalTop-1]
				l := vm.slots[fr.evalTop-2]
				if (l.Type == types.TypeLong || l.Type == types.TypeDouble) && (r.Type == types.TypeLong || r.Type == types.TypeDouble) {
					fr.evalTop--
					vm.slots[fr.evalTop] = types.Value{}
					vm.slots[fr.evalTop-1] = types.Sub(l, r)
					fr.pc++
					continue
				}
			}
			vm.opBinary(op)
		case bytecode.OpMul:
			if fr.evalTop-fr.evalBase >= 2 {
				r := vm.slots[fr.evalTop-1]
				l := vm.slots[fr.evalTop-2]
				if (l.Type == types.TypeLong || l.Type == types.TypeDouble) && (r.Type == types.TypeLong || r.Type == types.TypeDouble) {
					fr.evalTop--
					vm.slots[fr.evalTop] = types.Value{}
					vm.slots[fr.evalTop-1] = types.Mul(l, r)
					fr.pc++
					continue
				}
			}
			vm.opBinary(op)
		case bytecode.OpBWAnd, bytecode.OpBWOr, bytecode.OpBWXor, bytecode.OpSL, bytecode.OpSR:
			if fr.evalTop-fr.evalBase >= 2 {
				r := vm.slots[fr.evalTop-1]
				l := vm.slots[fr.evalTop-2]
				if l.Type == types.TypeLong && r.Type == types.TypeLong {
					switch op {
					case bytecode.OpBWAnd:
						fr.evalTop--
						vm.slots[fr.evalTop] = types.Value{}
						vm.slots[fr.evalTop-1] = types.NewLong(int64(l.Scalar & r.Scalar))
						fr.pc++
						continue
					case bytecode.OpBWOr:
						fr.evalTop--
						vm.slots[fr.evalTop] = types.Value{}
						vm.slots[fr.evalTop-1] = types.NewLong(int64(l.Scalar | r.Scalar))
						fr.pc++
						continue
					case bytecode.OpBWXor:
						fr.evalTop--
						vm.slots[fr.evalTop] = types.Value{}
						vm.slots[fr.evalTop-1] = types.NewLong(int64(l.Scalar ^ r.Scalar))
						fr.pc++
						continue
					case bytecode.OpSL:
						if int64(r.Scalar) >= 0 && int64(r.Scalar) < 64 {
							fr.evalTop--
							vm.slots[fr.evalTop] = types.Value{}
							vm.slots[fr.evalTop-1] = types.NewLong(int64(l.Scalar << (r.Scalar & 63)))
							fr.pc++
							continue
						}
					case bytecode.OpSR:
						if int64(r.Scalar) >= 0 && int64(r.Scalar) < 64 {
							fr.evalTop--
							vm.slots[fr.evalTop] = types.Value{}
							vm.slots[fr.evalTop-1] = types.NewLong(int64(l.Scalar) >> (r.Scalar & 63))
							fr.pc++
							continue
						}
					}
				}
			}
			vm.opBinary(op)
		case bytecode.OpIsSmaller, bytecode.OpIsSmallerOrEqual, bytecode.OpIsEqual, bytecode.OpIsIdentical, bytecode.OpIsNotEqual, bytecode.OpIsNotIdentical:
			if fr.evalTop-fr.evalBase >= 2 {
				r := vm.slots[fr.evalTop-1]
				l := vm.slots[fr.evalTop-2]
				if l.Type == types.TypeLong && r.Type == types.TypeLong {
					var res bool
					switch op {
					case bytecode.OpIsSmaller:
						res = int64(l.Scalar) < int64(r.Scalar)
					case bytecode.OpIsSmallerOrEqual:
						res = int64(l.Scalar) <= int64(r.Scalar)
					case bytecode.OpIsEqual, bytecode.OpIsIdentical:
						res = l.Scalar == r.Scalar
					default:
						res = l.Scalar != r.Scalar
					}
					fr.evalTop--
					vm.slots[fr.evalTop] = types.Value{}
					vm.slots[fr.evalTop-1] = types.NewBool(res)
					fr.pc++
					continue
				}
				if (l.Type == types.TypeLong || l.Type == types.TypeDouble) && (r.Type == types.TypeLong || r.Type == types.TypeDouble) {
					lf, rf := l.ToDouble(), r.ToDouble()
					var res bool
					switch op {
					case bytecode.OpIsSmaller:
						res = lf < rf
					case bytecode.OpIsSmallerOrEqual:
						res = lf <= rf
					case bytecode.OpIsEqual:
						res = lf == rf
					case bytecode.OpIsIdentical:
						res = l.Type == r.Type && lf == rf
					case bytecode.OpIsNotIdentical:
						res = l.Type != r.Type || lf != rf
					default:
						res = lf != rf
					}
					fr.evalTop--
					vm.slots[fr.evalTop] = types.Value{}
					vm.slots[fr.evalTop-1] = types.NewBool(res)
					fr.pc++
					continue
				}
			}
			vm.opCmp(op)
		case bytecode.OpJmp:
			if len(fr.fn.TryTable) == 0 {
				fr.pc = int(in.Op1)
				continue
			}
			vm.jump(int(in.Op1))
		case bytecode.OpJmpZ, bytecode.OpJmpNZ:
			if len(fr.fn.TryTable) == 0 && fr.evalTop > fr.evalBase {
				top := fr.evalTop - 1
				v := vm.slots[top]
				if v.Type <= types.TypeLong {
					fr.evalTop = top
					vm.slots[top] = types.Value{}
					truth := v.Type == types.TypeTrue || (v.Type == types.TypeLong && v.Scalar != 0)
					if op == bytecode.OpJmpZ {
						if !truth {
							fr.pc = int(in.Op1)
						} else {
							fr.pc++
						}
					} else {
						if truth {
							fr.pc = int(in.Op1)
						} else {
							fr.pc++
						}
					}
					continue
				}
			}
			vm.opCond(op, int(in.Op1))
		case bytecode.OpYield, bytecode.OpYieldFrom:
			vm.opYield(in)
			if fr.gen != nil && fr.gen.yielded {
				return
			}
		case bytecode.OpGlobalGet, bytecode.OpGlobalSet, bytecode.OpGlobalUnset, bytecode.OpGlobalIsset:
			vm.opGlobal(in.Op)
		case bytecode.OpBindGlobal:
			vm.bindGlobal(in.Op1)
		case bytecode.OpEval, bytecode.OpInclude:
			vm.opSource(in)
		case bytecode.OpStaticCheck:
			if ref, ok := vm.localStatics[fr.fn][in.Op1]; ok {
				slot := vm.cvPtr(in.Op1)
				old := *slot
				*slot = types.RetainReference(vm.Zone, ref)
				vmRelease(vm.Zone, &old)
				vm.jump(int(in.Op2))
			} else {
				vm.bump()
			}
		case bytecode.OpBindStatic:
			v := vm.pop()
			ref, err := types.NewReference(vm.Zone, v)
			vmRelease(vm.Zone, &v)
			if err != nil {
				vm.fatalFeature("reference statique")
				return
			}
			if vm.localStatics == nil {
				vm.localStatics = make(map[*bytecode.OpArray]map[uint32]types.Value)
			}
			values := vm.localStatics[fr.fn]
			if values == nil {
				values = make(map[uint32]types.Value)
				vm.localStatics[fr.fn] = values
			}
			oldStatic, hadOld := values[in.Op1]
			values[in.Op1] = ref
			if hadOld {
				vmRelease(vm.Zone, &oldStatic)
			}
			slot := vm.cvPtr(in.Op1)
			oldSlot := *slot
			*slot = types.RetainReference(vm.Zone, ref)
			vmRelease(vm.Zone, &oldSlot)
			vm.bump()
		case bytecode.OpNop:
			if in.Flags&bytecode.FlagDeclareClass != 0 {
				vm.opDeclareClass(in.Op1)
				break
			}
			if in.Flags&bytecode.FlagSilenceBegin != 0 {
				vm.frame().silence++
			} else if in.Flags&bytecode.FlagSilenceEnd != 0 {
				if fr := vm.frame(); fr.silence > 0 {
					fr.silence--
				}
			}
			vm.bump()
		case bytecode.OpEnterFinally:
			vm.frame().silence = 0
			vm.bump()
		case bytecode.OpDup:
			v := vm.pop()
			vm.push(vmRetain(vm.Zone, v))
			vm.push(v)
			vm.bump()
		case bytecode.OpEcho:
			v := vm.pop()
			vm.echo(v)
			vmRelease(vm.Zone, &v)
			vm.bump()
		case bytecode.OpPrint:
			v := vm.pop()
			vm.echo(v)
			vmRelease(vm.Zone, &v)
			vm.push(types.NewLong(1))
			vm.bump()
		case bytecode.OpAssignRefCV:
			vm.opAssignRef(in.Op1, in.Op2)
		case bytecode.OpUnsetCV:
			slot := vm.cvPtr(in.Op1)
			old := *slot
			*slot = types.Value{}
			vmRelease(vm.Zone, &old)
			vm.bump()
		case bytecode.OpIssetCV:
			vm.push(types.NewBool(vm.issetCV(in.Op1)))
			vm.bump()
		case bytecode.OpEmptyCV:
			if fr.fn.HasGlobalsCV && in.Op1 == fr.fn.GlobalsCV || (!fr.fn.CVMetaResolved && int(in.Op1) < len(fr.fn.CVNames) && fr.fn.CVNames[in.Op1] == "GLOBALS") {
				snap := vm.globalSnapshot()
				empty := !types.ToBoolZ(vm.Zone, snap)
				vmRelease(vm.Zone, &snap)
				vm.push(types.NewBool(empty))
				vm.bump()
				continue
			}
			slot := vm.slots[fr.cvBase+int(in.Op1)]
			vm.push(types.NewBool(!types.ToBoolZ(vm.Zone, slot)))
			vm.bump()
		case bytecode.OpMakeRef:
			vm.opMakeRef()
		case bytecode.OpDeref:
			v := vm.pop()
			d := vmRetain(vm.Zone, types.Deref(vm.Zone, v))
			vmRelease(vm.Zone, &v)
			vm.push(d)
			vm.bump()
		case bytecode.OpDiv, bytecode.OpMod, bytecode.OpPow, bytecode.OpConcat, bytecode.OpBoolXor:
			vm.opBinary(op)
		case bytecode.OpBWNot, bytecode.OpBoolNot, bytecode.OpUnaryPlus, bytecode.OpUnaryMinus, bytecode.OpCast:
			vm.opUnary(op, in.Op1)
		case bytecode.OpSpaceship:
			vm.opCmp(op)
		case bytecode.OpPreIncCV, bytecode.OpPreDecCV, bytecode.OpPostIncCV, bytecode.OpPostDecCV:
			slotPtr := &vm.slots[fr.cvBase+int(in.Op1)]
			if slotPtr.Type == types.TypeLong {
				val := int64(slotPtr.Scalar)
				if val != 9223372036854775807 && val != -9223372036854775808 {
					if in.Flags&bytecode.FlagDiscard != 0 {
						if op == bytecode.OpPreIncCV || op == bytecode.OpPostIncCV {
							slotPtr.Scalar = uint64(val + 1)
						} else {
							slotPtr.Scalar = uint64(val - 1)
						}
						fr.pc++
						continue
					}
					if fr.evalTop < fr.evalBase+fr.fn.MaxStack {
						switch op {
						case bytecode.OpPreIncCV:
							slotPtr.Scalar = uint64(val + 1)
							vm.slots[fr.evalTop] = *slotPtr
						case bytecode.OpPostIncCV:
							vm.slots[fr.evalTop] = *slotPtr
							slotPtr.Scalar = uint64(val + 1)
						case bytecode.OpPreDecCV:
							slotPtr.Scalar = uint64(val - 1)
							vm.slots[fr.evalTop] = *slotPtr
						default:
							vm.slots[fr.evalTop] = *slotPtr
							slotPtr.Scalar = uint64(val - 1)
						}
						fr.evalTop++
						fr.pc++
						continue
					}
				}
			}
			vm.opInc(op, in.Op1, in.Flags)
		case bytecode.OpJmpNull:
			vm.opJmpNull(int(in.Op1))
		case bytecode.OpSwitchLong, bytecode.OpSwitchString, bytecode.OpMatch:
			vm.opSwitch(op, in)
		case bytecode.OpReturn:
			vm.opReturn(in)
		case bytecode.OpExit:
			vm.opExit(in)
		case bytecode.OpInitArray:
			vm.opInitArray()
		case bytecode.OpAddArrayElement:
			vm.opAddElem(true, in)
		case bytecode.OpAppendArrayElement:
			vm.opAddElem(false, in)
		case bytecode.OpFetchDimR, bytecode.OpFetchDimW, bytecode.OpFetchDimIs:
			vm.opFetchDim(op, in.Flags)
		case bytecode.OpAssignDim, bytecode.OpAssignDimOp:
			vm.opAssignDim(op, in)
		case bytecode.OpUnsetDim:
			vm.opUnsetDim()
		case bytecode.OpIssetDim, bytecode.OpEmptyDim:
			vm.opIssetDim(op == bytecode.OpEmptyDim)
		case bytecode.OpFeResetR, bytecode.OpFeResetRW, bytecode.OpFeFetchR, bytecode.OpFeFetchRW, bytecode.OpFeFree:
			vm.opForeach(op, in)
		case bytecode.OpInitCall, bytecode.OpInitMethodCall:
			vm.opInit(op, in)
		case bytecode.OpInitNew:
			if in.Flags&bytecode.FlagDynamicClass != 0 {
				id, msg := vm.dynamicClassOperand(in.Op1, false)
				if id == 0 {
					if msg != "" {
						vm.raiseBuiltin(bytecode.ClassError, msg, 0)
					}
					break
				}
				in.Op1, in.Flags = id, in.Flags&^bytecode.FlagDynamicClass
			} else if in.Flags&(bytecode.FlagSelf|bytecode.FlagParent|bytecode.FlagStatic) == 0 && in.Op1 >= bytecode.ClassUser {
				id, ok := vm.classOperand(in.Op1)
				if !ok {
					if !vm.stop && !vm.control {
						vm.raiseClassNotFound(in.Op1)
					}
					break
				}
				in.Op1 = id
			}
			vm.opInit(op, in)
		case bytecode.OpInitStaticMethodCall:
			if in.Flags&bytecode.FlagDynamicClass != 0 {
				id, msg := vm.dynamicClassOperand(in.Op2, false)
				if id == 0 {
					if msg != "" {
						vm.raiseBuiltin(bytecode.ClassError, msg, 0)
					}
					break
				}
				in.Op2, in.Flags = id, in.Flags&^bytecode.FlagDynamicClass
			} else if in.Flags&(bytecode.FlagSelf|bytecode.FlagParent|bytecode.FlagStatic) == 0 && in.Op2 >= bytecode.ClassUser {
				id, ok := vm.classOperand(in.Op2)
				if !ok {
					if !vm.stop && !vm.control {
						vm.raiseClassNotFound(in.Op2)
					}
					break
				}
				in.Op2 = id
			}
			vm.forwardThis(&in)
			vm.opInit(op, in)
		case bytecode.OpSendVal, bytecode.OpSendRef:
			vm.opSend(op == bytecode.OpSendRef || in.Flags&bytecode.FlagFuncArg != 0 && vm.funcArgByRef())
		case bytecode.OpSendNamed:
			vm.opSendNamed(in.Op1)
		case bytecode.OpSendUnpack:
			vm.opSendUnpack()
		case bytecode.OpDoCall:
			vm.opDoCall()
		case bytecode.OpBindClosure:
			vm.opBindClosure(in)
		case bytecode.OpFetchPropR, bytecode.OpFetchPropW:
			vm.opFetchProp(op, in.Op1, in.Flags)
		case bytecode.OpAssignProp, bytecode.OpAssignPropOp:
			vm.opAssignProp(op, in)
		case bytecode.OpUnsetProp, bytecode.OpIssetProp, bytecode.OpEmptyProp:
			vm.opPropQuery(op, in.Op1)
		case bytecode.OpFetchStaticPropR, bytecode.OpAssignStaticProp, bytecode.OpFetchClassConst:
			if in.Flags&bytecode.FlagDynamicClass != 0 {
				if op == bytecode.OpFetchClassConst && vm.frame().fn.Strings[in.Op1] == "class" {
					vm.dynamicClassName(in.Op2)
					break
				}
				id, msg := vm.dynamicClassOperand(in.Op2, false)
				if id == 0 {
					if msg != "" {
						if op == bytecode.OpAssignStaticProp {
							v := vm.pop()
							vmRelease(vm.Zone, &v)
						}
						vm.raiseBuiltin(bytecode.ClassError, msg, 0)
					}
					break
				}
				in.Op2, in.Flags = id, in.Flags&^bytecode.FlagDynamicClass
			} else if in.Flags&(bytecode.FlagSelf|bytecode.FlagParent|bytecode.FlagStatic) == 0 && in.Op2 >= bytecode.ClassUser {
				id, ok := vm.classOperand(in.Op2)
				if !ok {
					if !vm.stop && !vm.control {
						if op == bytecode.OpAssignStaticProp {
							v := vm.pop()
							vmRelease(vm.Zone, &v)
						}
						vm.raiseClassNotFound(in.Op2)
					}
					break
				}
				in.Op2 = id
			}
			switch op {
			case bytecode.OpFetchStaticPropR:
				vm.opStaticProp(false, in)
			case bytecode.OpAssignStaticProp:
				vm.opStaticProp(true, in)
			default:
				vm.opClassConst(in)
			}
		case bytecode.OpInstanceof:
			switch {
			case in.Flags&bytecode.FlagDynamicClass != 0:
				id, msg := vm.dynamicClassOperand(in.Op1, true)
				if id != 0 {
					vm.opInstanceof(id)
					break
				}
				v := vm.pop()
				vmRelease(vm.Zone, &v)
				if msg != "" {
					vm.raiseBuiltin(bytecode.ClassError, msg, 0)
					break
				}
				vm.push(types.NewBool(false))
				vm.bump()
			case in.Flags&bytecode.FlagSelf != 0:
				vm.opInstanceof(vm.frame().scopeClass)
			case in.Flags&bytecode.FlagParent != 0:
				vm.opInstanceof(vm.parentOf(vm.frame().scopeClass))
			case in.Flags&bytecode.FlagStatic != 0:
				vm.opInstanceof(vm.frame().calledClass)
			default:
				vm.opInstanceof(in.Op1)
			}
		case bytecode.OpClone:
			vm.opClone()
		case bytecode.OpThrow:
			v := vm.pop()
			vm.excTrace = nil
			vm.throwAt(vm.frame().pc, v)
		case bytecode.OpEnterCatch:
			vm.frame().silence = 0
			vm.opEnterCatch(in.Op1)
		case bytecode.OpLeaveFinally, bytecode.OpFastRetFinally:
			vm.finishFinally()
		case bytecode.OpFastCallFinally:
			vm.fastCallFinally(int(in.Op1), int(in.Op2))
		default:
			vm.fatalFeature(op.String())
		}
	}
}
