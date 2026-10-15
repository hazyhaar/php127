package vm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/ast"
	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/parser"
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
)

type cachedSource struct {
	data    string
	modTime int64
	size    int64
}

var (
	sourceCacheMu sync.RWMutex
	sourceCache   = make(map[string]cachedSource, 32)
)

func readSourceFile(filename string) (string, error) {
	info, err := os.Stat(filename)
	if err != nil {
		return "", err
	}
	mtime := info.ModTime().UnixNano()
	sz := info.Size()
	sourceCacheMu.RLock()
	cached, ok := sourceCache[filename]
	sourceCacheMu.RUnlock()
	if ok && cached.modTime == mtime && cached.size == sz {
		return cached.data, nil
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	src := string(data)
	sourceCacheMu.Lock()
	if len(sourceCache) >= 512 {
		clear(sourceCache)
	}
	sourceCache[filename] = cachedSource{data: src, modTime: mtime, size: sz}
	sourceCacheMu.Unlock()
	return src, nil
}

// compiledInclude est le patron immuable d'un fichier inclus compilé : programme déjà terminé par le retour 1
// d'include et déclarations hissées, horodaté par la date de modification et la taille du fichier lu. Un patron
// n'est jamais lié tel quel : linkDynamic mute les prototypes et les OpArray qu'il fusionne, chaque inclusion
// lie donc un clone rendu par cloneCompiledInclude.
type compiledInclude struct {
	prog    *bytecode.Program
	decls   *fileDecls
	modTime int64
	size    int64
}

var (
	compiledIncludeMu sync.RWMutex
	// compiledIncludeCache indexe les patrons par chemin résolu ; une entrée dont l'horodatage diffère de celui
	// du fichier sur disque est recompilée et remplacée.
	compiledIncludeCache = make(map[string]*compiledInclude, 128)
)

// compiledIncludeFor rend le patron compilé d'un fichier inclus, depuis le cache inter-requêtes tant que la date
// de modification et la taille du fichier sont inchangées. readable est faux pour un fichier illisible ; err porte
// alors une erreur d'analyse ou de compilation à afficher, ou une includeReturnError du retour ajouté.
func compiledIncludeFor(filename string) (tmpl *compiledInclude, readable bool, err error) {
	info, err := os.Stat(filename)
	if err != nil {
		return nil, false, err
	}
	mtime, size := info.ModTime().UnixNano(), info.Size()
	compiledIncludeMu.RLock()
	cached := compiledIncludeCache[filename]
	compiledIncludeMu.RUnlock()
	if cached != nil && cached.modTime == mtime && cached.size == size {
		return cached, true, nil
	}
	src, err := readSourceFile(filename)
	if err != nil {
		return nil, false, err
	}
	file, err := parser.Parse(src, filename)
	if err != nil {
		return nil, true, err
	}
	decls := collectDecls(file)
	prog, err := compiler.Compile(file)
	if err != nil {
		return nil, true, err
	}
	// Le fichier inclus rend 1 en l'absence de return : sa dernière instruction devient le chargement de cette constante.
	main := prog.Main
	last := &main.Code[len(main.Code)-1]
	last.Flags = 0
	last.Op = bytecode.OpConst
	last.Op1 = uint32(len(main.Consts))
	main.Consts = append(main.Consts, types.NewLong(1))
	main.Code = append(main.Code, bytecode.Instr{Op: bytecode.OpReturn, Line: last.Line})
	main.Aux = append(main.Aux, bytecode.NoCV)
	main.MaxStack = max(main.MaxStack, 1)
	if err := bytecode.Verify(main); err != nil {
		return nil, true, includeReturnError{err}
	}
	tmpl = &compiledInclude{prog: prog, decls: decls, modTime: mtime, size: size}
	// Un fichier réécrit pendant sa lecture garde un horodatage qui ne décrit pas la source compilée : le patron sert cette inclusion sans entrer au cache.
	if after, err := os.Stat(filename); err != nil || after.ModTime().UnixNano() != mtime || after.Size() != size {
		return tmpl, true, nil
	}
	compiledIncludeMu.Lock()
	if len(compiledIncludeCache) >= 4096 {
		clear(compiledIncludeCache)
	}
	compiledIncludeCache[filename] = tmpl
	compiledIncludeMu.Unlock()
	return tmpl, true, nil
}

// includeReturnError signale l'échec de vérification du programme après l'ajout du retour 1 d'include, distinct
// d'une erreur d'analyse ou de compilation que l'inclusion affiche telle quelle.
type includeReturnError struct{ err error }

func (e includeReturnError) Error() string { return e.err.Error() }

// progCloner copie un programme patron en préservant le partage des OpArray (méthode de trait recopiée dans une
// classe, thunk de constante référencé par Program.Funcs et par une constante) et des constantes différées.
type progCloner struct {
	fns    map[*bytecode.OpArray]*bytecode.OpArray
	consts map[*bytecode.Constant]*bytecode.Constant
}

// cloneCompiledInclude rend une copie liable d'un programme patron. Sont copiés les champs que linkDynamic et la
// liaison des classes réécrivent : Code, Strings (par réallocation à l'ajout), TryTable et ses classes de capture,
// FuncID par la copie de l'OpArray ; Props, ResolvedProps, Methods, Constants et Cases des prototypes, avec leurs
// constantes différées dont le thunk désigne l'OpArray copié. Les tranches jamais réécrites (Consts, CVNames, Aux,
// Tables, Uses, SuperglobalCVs, Interfaces, Traits) restent partagées avec le patron.
func cloneCompiledInclude(prog *bytecode.Program) *bytecode.Program {
	c := progCloner{fns: make(map[*bytecode.OpArray]*bytecode.OpArray, len(prog.Funcs)+8)}
	out := &bytecode.Program{Constants: prog.Constants, NeedsSuperglobals: prog.NeedsSuperglobals}
	out.Main = c.fn(prog.Main)
	out.Funcs = make([]*bytecode.OpArray, len(prog.Funcs))
	for i, fn := range prog.Funcs {
		out.Funcs[i] = c.fn(fn)
	}
	out.Classes = make([]*bytecode.ClassProto, len(prog.Classes))
	for i, cl := range prog.Classes {
		out.Classes[i] = c.class(cl)
	}
	out.Traits = make([]*bytecode.ClassProto, len(prog.Traits))
	for i, cl := range prog.Traits {
		out.Traits[i] = c.class(cl)
	}
	return out
}

func (c *progCloner) fn(f *bytecode.OpArray) *bytecode.OpArray {
	if f == nil {
		return nil
	}
	if cp, ok := c.fns[f]; ok {
		return cp
	}
	cp := *f
	c.fns[f] = &cp
	cp.Code = slices.Clone(f.Code)
	cp.Strings = slices.Clip(f.Strings)
	cp.Consts = slices.Clip(f.Consts)
	cp.Aux = slices.Clip(f.Aux)
	if len(f.TryTable) > 0 {
		cp.TryTable = slices.Clone(f.TryTable)
		for t := range cp.TryTable {
			targets := slices.Clone(cp.TryTable[t].CatchTargets)
			for k := range targets {
				targets[k].Classes = slices.Clone(targets[k].Classes)
			}
			cp.TryTable[t].CatchTargets = targets
		}
	}
	if slices.ContainsFunc(f.Params, func(p bytecode.ParamInfo) bool { return c.deferred(p.DefaultConst) || c.attrsDeferred(p.Attributes) }) {
		cp.Params = slices.Clone(f.Params)
		for i := range cp.Params {
			cp.Params[i].DefaultConst = c.constant(cp.Params[i].DefaultConst)
			cp.Params[i].Attributes = c.attrs(cp.Params[i].Attributes)
		}
	}
	cp.Attributes = c.attrs(f.Attributes)
	return &cp
}

func (c *progCloner) class(p *bytecode.ClassProto) *bytecode.ClassProto {
	if p == nil {
		return nil
	}
	cp := *p
	cp.Props = c.props(p.Props)
	cp.ResolvedProps = c.props(p.ResolvedProps)
	cp.Methods = slices.Clone(p.Methods)
	for i := range cp.Methods {
		cp.Methods[i].Func = c.fn(cp.Methods[i].Func)
		cp.Methods[i].Attributes = c.attrs(cp.Methods[i].Attributes)
	}
	cp.Constants = slices.Clone(p.Constants)
	for i := range cp.Constants {
		cp.Constants[i].Initial = c.constant(cp.Constants[i].Initial)
		cp.Constants[i].Attributes = c.attrs(cp.Constants[i].Attributes)
	}
	cp.Cases = slices.Clone(p.Cases)
	for i := range cp.Cases {
		cp.Cases[i].Value = c.constant(cp.Cases[i].Value)
		cp.Cases[i].Attributes = c.attrs(cp.Cases[i].Attributes)
	}
	cp.Attributes = c.attrs(p.Attributes)
	return &cp
}

func (c *progCloner) props(props []bytecode.PropInfo) []bytecode.PropInfo {
	if props == nil {
		return nil
	}
	out := slices.Clone(props)
	for i := range out {
		out[i].Initial = c.constant(out[i].Initial)
		out[i].Attributes = c.attrs(out[i].Attributes)
	}
	return out
}

// deferred indique qu'une constante porte, directement ou dans un élément de tableau, une expression différée.
func (c *progCloner) deferred(k *bytecode.Constant) bool {
	if k == nil {
		return false
	}
	if k.Thunk != nil {
		return true
	}
	for i := range k.Items {
		if c.deferred(k.Items[i].Key) || c.deferred(&k.Items[i].Value) {
			return true
		}
	}
	return false
}

// constant rend la constante elle-même lorsqu'elle ne porte aucune expression différée, sinon une copie dont les thunks désignent les OpArray copiés.
func (c *progCloner) constant(k *bytecode.Constant) *bytecode.Constant {
	if !c.deferred(k) {
		return k
	}
	if cp, ok := c.consts[k]; ok {
		return cp
	}
	if c.consts == nil {
		c.consts = make(map[*bytecode.Constant]*bytecode.Constant)
	}
	cp := *k
	c.consts[k] = &cp
	cp.Thunk = c.fn(k.Thunk)
	if len(k.Items) > 0 {
		cp.Items = slices.Clone(k.Items)
		for i := range cp.Items {
			cp.Items[i].Key = c.constant(cp.Items[i].Key)
			cp.Items[i].Value = *c.constant(&k.Items[i].Value)
		}
	}
	return &cp
}

func (c *progCloner) attrsDeferred(attrs []bytecode.AttributeProto) bool {
	for _, a := range attrs {
		for _, arg := range a.Args {
			if c.deferred(arg.Value) {
				return true
			}
		}
	}
	return false
}

func (c *progCloner) attrs(attrs []bytecode.AttributeProto) []bytecode.AttributeProto {
	if !c.attrsDeferred(attrs) {
		return attrs
	}
	out := slices.Clone(attrs)
	for i := range out {
		out[i].Args = slices.Clone(out[i].Args)
		for j := range out[i].Args {
			out[i].Args[j].Value = c.constant(out[i].Args[j].Value)
		}
	}
	return out
}

// declSite situe une déclaration de classe ou de fonction : niveau fichier ou bloc conditionnel, fichier et ligne.
type declSite struct {
	topLevel bool
	file     string
	line     uint32
}

// fileDecls recense, par nom en minuscules, les déclarations que le compilateur hisse depuis un fichier.
type fileDecls struct {
	classes map[string]declSite
	funcs   map[string]declSite
}

// collectDecls reproduit le parcours de declareClasses et de collectFuncs du compilateur : la première déclaration d'un nom l'emporte. Les classes, interfaces, traits et enums sont indexés par leur nom qualifié par l'espace de noms, comme le compilateur nomme leurs prototypes ; les fonctions gardent leur nom court, que le compilateur ne qualifie pas.
func collectDecls(file *ast.File) *fileDecls {
	d := &fileDecls{classes: make(map[string]declSite), funcs: make(map[string]declSite)}
	add := func(m map[string]declSite, name string, top bool, line int) {
		key := strings.ToLower(strings.TrimPrefix(name, `\`))
		if _, ok := m[key]; !ok {
			m[key] = declSite{topLevel: top, file: file.Filename, line: uint32(line)}
		}
	}
	qualify := func(ns, name string) string {
		if ns == "" {
			return name
		}
		return ns + `\` + name
	}
	var walk func(stmts []ast.Stmt, ns string, top bool)
	walk = func(stmts []ast.Stmt, ns string, top bool) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.NamespaceDecl:
				if len(s.Body) > 0 || s.Bracketed {
					walk(s.Body, s.Name, top)
				} else {
					ns = s.Name
				}
			case *ast.IfStmt:
				walk(s.Body, ns, false)
				for _, cl := range s.ElseIfs {
					walk(cl.Body, ns, false)
				}
				walk(s.Else, ns, false)
			case *ast.BlockStmt:
				walk(s.Body, ns, false)
			case *ast.DeclareStmt:
				walk(s.Body, ns, top)
			case *ast.SwitchStmt:
				for _, c := range s.Cases {
					walk(c.Body, ns, false)
				}
			case *ast.TryStmt:
				walk(s.Body, ns, false)
				for _, c := range s.Catches {
					walk(c.Body, ns, false)
				}
				walk(s.Finally, ns, false)
			case *ast.FunctionDecl:
				add(d.funcs, s.Name, top, s.Line)
				walk(s.Body, ns, false)
			case *ast.ClassDecl:
				add(d.classes, qualify(ns, s.Name), top, s.Line)
			case *ast.InterfaceDecl:
				add(d.classes, qualify(ns, s.Name), top, s.Line)
			case *ast.TraitDecl:
				add(d.classes, qualify(ns, s.Name), top, s.Line)
			case *ast.EnumDecl:
				add(d.classes, qualify(ns, s.Name), top, s.Line)
			}
		}
	}
	walk(file.Stmts, "", true)
	return d
}

// mainFileDeclarations relit le fichier du programme principal pour trancher entre classe vide déclarée et nom seulement référencé ; nil si le fichier est illisible.
func mainFileDeclarations(fn *bytecode.OpArray) *fileDecls {
	if fn == nil || fn.Filename == "" {
		return nil
	}
	src, err := readSourceFile(fn.Filename)
	if err != nil {
		return nil
	}
	file, err := parser.Parse(src, fn.Filename)
	if err != nil {
		return nil
	}
	return collectDecls(file)
}

// cloneProgram copie le programme partagé avant toute fusion et construit les index par identifiant et par nom de classe.
func (vm *VM) cloneProgram() {
	if vm.Program == nil || vm.progCloned && vm.classIndex != nil {
		return
	}
	if !vm.progCloned {
		for _, list := range [][]*bytecode.ClassProto{vm.Program.Classes, vm.Program.Traits} {
			for _, c := range list {
				if c != nil {
					vm.classDeclared(c)
				}
			}
		}
		cp := *vm.Program
		cp.Funcs = slices.Clone(vm.Program.Funcs)
		cp.Classes = slices.Clone(vm.Program.Classes)
		cp.Traits = slices.Clone(vm.Program.Traits)
		vm.origProgram = vm.Program
		vm.Program = &cp
		vm.progCloned = true
	}
	vm.classIndex = make(map[uint32]*bytecode.ClassProto, len(vm.Program.Classes)+len(vm.Program.Traits)+16)
	vm.classNames = make(map[string]uint32, len(vm.Program.Classes)+len(vm.Program.Traits)+16)
	if vm.ownedClasses == nil {
		vm.ownedClasses = make(map[*bytecode.ClassProto]bool)
	}
	if vm.classState == nil {
		vm.classState = make(map[uint32]bool)
	}
	vm.nextClassID = bytecode.ClassUser
	for _, list := range [][]*bytecode.ClassProto{vm.Program.Classes, vm.Program.Traits} {
		for _, c := range list {
			if c == nil {
				continue
			}
			vm.indexClass(c)
			if c.ID >= vm.nextClassID {
				if _, builtin := bytecode.BuiltinClassName(c.ID); !builtin {
					vm.nextClassID = c.ID + 1
				}
			}
		}
	}
	vm.nextFuncID = 2
	note := func(fn *bytecode.OpArray) {
		if fn != nil && fn.FuncID >= vm.nextFuncID {
			vm.nextFuncID = fn.FuncID + 1
		}
	}
	note(vm.Program.Main)
	for _, fn := range vm.Program.Funcs {
		note(fn)
	}
	for _, list := range [][]*bytecode.ClassProto{vm.Program.Classes, vm.Program.Traits} {
		for _, c := range list {
			if c != nil {
				for _, m := range c.Methods {
					note(m.Func)
				}
			}
		}
	}
}

func (vm *VM) indexClass(c *bytecode.ClassProto) {
	vm.classIndex[c.ID] = c
	key := strings.ToLower(strings.TrimPrefix(c.Name, `\`))
	if _, ok := vm.classNames[key]; !ok {
		vm.classNames[key] = c.ID
	}
}

func (vm *VM) allocClassID() uint32 {
	for {
		id := vm.nextClassID
		vm.nextClassID++
		if _, builtin := bytecode.BuiltinClassName(id); builtin {
			continue
		}
		if _, used := vm.classIndex[id]; used {
			continue
		}
		return id
	}
}

func (vm *VM) allocFuncID() uint32 {
	id := vm.nextFuncID
	vm.nextFuncID++
	return id
}

// replaceClass substitue un prototype à un autre dans la tranche de programme qui le contient, sans toucher au prototype partagé remplacé.
func (vm *VM) replaceClass(old, next *bytecode.ClassProto) {
	for _, list := range [][]*bytecode.ClassProto{vm.Program.Classes, vm.Program.Traits} {
		for i, c := range list {
			if c == old {
				list[i] = next
				vm.classIndex[next.ID] = next
				return
			}
		}
	}
	vm.Program.Classes = append(vm.Program.Classes, next)
	vm.classIndex[next.ID] = next
}

// userFuncIndex retrouve une fonction utilisateur par son nom grâce à un index construit au premier appel et tenu à jour par linkDynamic.
func (vm *VM) userFuncIndex(name string) int {
	if vm.funcNames == nil {
		vm.funcNames = make(map[string]int, len(vm.Program.Funcs)+16)
		for i, fn := range vm.Program.Funcs {
			if fn != nil && !bytecode.AnonymousFunc(fn.Name) {
				if _, ok := vm.funcNames[strings.ToLower(fn.Name)]; !ok {
					vm.funcNames[strings.ToLower(fn.Name)] = i
				}
			}
		}
	}
	if i, ok := vm.funcNames[strings.ToLower(name)]; ok {
		return i
	}
	return -1
}

// fatalAt écrit une erreur fatale au format de PHP et arrête la requête.
func (vm *VM) fatalAt(msg, file string, line uint32) {
	if vm.stop {
		return
	}
	vm.control = true
	if vm.Zone != nil {
		stdlib.RecordError(vm.Zone, eError, msg, file, int64(line))
	}
	fmt.Fprintf(vm.Out, "\nFatal error: %s in %s on line %d\n", msg, file, line)
	vm.exit = 255
	vm.stop = true
}

type mergedClass struct {
	proto   *bytecode.ClassProto
	id      uint32
	site    declSite
	replace *bytecode.ClassProto
}

// linkDynamic fusionne dans le programme courant les fonctions, classes, interfaces et traits d'une compilation dynamique : identifiants réattribués, opérandes décalés, héritage lié tardivement.
func (vm *VM) linkDynamic(prog *bytecode.Program, decls *fileDecls, filename string) bool {
	vm.cloneProgram()
	funcMap := make([]int, len(prog.Funcs))
	keep := make([]bool, len(prog.Funcs))
	early := make([]bool, len(prog.Funcs))
	for i, fn := range prog.Funcs {
		funcMap[i] = -1
		if fn == nil {
			continue
		}
		if bytecode.AnonymousFunc(fn.Name) {
			keep[i] = true
			continue
		}
		key := strings.ToLower(fn.Name)
		site, ok := decls.funcs[key]
		if !ok {
			site = declSite{file: filename}
		}
		existing := vm.userFuncIndex(fn.Name)
		_, native := lookupNative(fn.Name)
		native = native || intrinsicExists(fn.Name)
		if existing < 0 && !native {
			keep[i], early[i] = true, site.topLevel
			continue
		}
		if site.topLevel {
			msg := "Cannot redeclare " + fn.Name + "()"
			if existing >= 0 {
				prev := vm.funcSite(vm.Program.Funcs[existing])
				msg = "Cannot redeclare " + vm.Program.Funcs[existing].Name + "() (previously declared in " + prev.file + ":" + strconv.FormatUint(uint64(prev.line), 10) + ")"
			}
			vm.fatalAt(msg, site.file, site.line)
			return false
		}
		funcMap[i] = existing
	}
	// PHP lie les fonctions de niveau fichier à la compilation et les fonctions conditionnelles à l'exécution : les premières précèdent les secondes dans la table des fonctions.
	var order []int
	for _, pass := range []bool{true, false} {
		for i := range prog.Funcs {
			if keep[i] && early[i] == pass {
				funcMap[i] = len(vm.Program.Funcs) + len(order)
				order = append(order, i)
			}
		}
	}

	idMap := make(map[uint32]uint32)
	var merged []mergedClass
	for _, list := range [][]*bytecode.ClassProto{prog.Classes, prog.Traits} {
		for _, c := range list {
			if c == nil {
				continue
			}
			key := strings.ToLower(strings.TrimPrefix(c.Name, `\`))
			site, isDecl := decls.classes[key]
			if !isDecl && c.Flags&bytecode.ClassAnonymous != 0 {
				// Une classe anonyme est déclarée par le fichier qui contient son new class, sans être relue dans le source.
				isDecl, site = true, declSite{file: filename}
			}
			if target, aliased := vm.classAliases[key]; aliased {
				if isDecl && site.topLevel {
					vm.fatalAt(declareInUse(c), site.file, site.line)
					return false
				}
				idMap[c.ID] = target
				continue
			}
			if _, builtin := bytecode.BuiltinClassID(key); builtin && isDecl && site.topLevel {
				vm.fatalAt(declareInUse(c), site.file, site.line)
				return false
			}
			existingID, exists := vm.classNames[key]
			if !isDecl {
				if exists {
					idMap[c.ID] = existingID
					continue
				}
				id := vm.allocClassID()
				ph := &bytecode.ClassProto{Name: strings.TrimPrefix(c.Name, `\`), ID: id}
				vm.Program.Classes = append(vm.Program.Classes, ph)
				vm.indexClass(ph)
				vm.ownedClasses[ph] = true
				vm.classState[id] = false
				idMap[c.ID] = id
				continue
			}
			if exists {
				idMap[c.ID] = existingID
				ex := vm.classByID(existingID)
				if ex == nil || vm.classDeclared(ex) || vm.pendingIndex(existingID) >= 0 {
					// Une déclaration inconditionnelle d'un nom déjà pris est fatale comme en PHP ; une déclaration conditionnelle, hissée par le compilateur, garde la première.
					if ex != nil && site.topLevel {
						vm.fatalAt(declareInUse(c), site.file, site.line)
						return false
					}
					continue
				}
				merged = append(merged, mergedClass{proto: c, id: existingID, site: site, replace: ex})
				continue
			}
			id := vm.allocClassID()
			idMap[c.ID] = id
			merged = append(merged, mergedClass{proto: c, id: id, site: site})
		}
	}
	mapID := func(id uint32) uint32 {
		if v, ok := idMap[id]; ok {
			return v
		}
		return id
	}
	for _, m := range merged {
		c := m.proto
		c.ID = m.id
		c.ParentID = mapID(c.ParentID)
		for i := range c.Props {
			c.Props[i].ClassID = mapID(c.Props[i].ClassID)
		}
		for i := range c.ResolvedProps {
			c.ResolvedProps[i].ClassID = mapID(c.ResolvedProps[i].ClassID)
		}
		for i := range c.Methods {
			c.Methods[i].ClassID = mapID(c.Methods[i].ClassID)
		}
	}
	seen := make(map[*bytecode.OpArray]bool)
	rewrite := func(fn *bytecode.OpArray) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		fn.FuncID = vm.allocFuncID()
		for i := range fn.Code {
			op := &fn.Code[i]
			if _, dynamic := bytecode.ClassOperandCV(*op); dynamic {
				// L'opérande désigne une variable compilée, pas un identifiant de classe du fichier inclus.
				continue
			}
			switch op.Op {
			case bytecode.OpNop:
				if op.Flags&bytecode.FlagDeclareClass != 0 {
					op.Op1 = mapID(op.Op1)
				}
			case bytecode.OpInitNew, bytecode.OpInstanceof:
				op.Op1 = mapID(op.Op1)
			case bytecode.OpInitStaticMethodCall, bytecode.OpFetchClassConst, bytecode.OpFetchStaticPropR, bytecode.OpAssignStaticProp:
				op.Op2 = mapID(op.Op2)
			case bytecode.OpBindClosure:
				if int(op.Op1) < len(funcMap) {
					op.Op1 = uint32(funcMap[op.Op1])
				}
			case bytecode.OpInitCall:
				if op.Flags&(bytecode.FlagNative|bytecode.FlagClosure) != 0 || int(op.Op1) >= len(funcMap) {
					continue
				}
				if target := funcMap[op.Op1]; target >= 0 {
					op.Op1 = uint32(target)
				} else {
					name := prog.Funcs[op.Op1].Name
					op.Flags |= bytecode.FlagNative
					op.Op1 = uint32(len(fn.Strings))
					fn.Strings = append(fn.Strings, name)
				}
			}
		}
		for t := range fn.TryTable {
			for k := range fn.TryTable[t].CatchTargets {
				classes := fn.TryTable[t].CatchTargets[k].Classes
				for j := range classes {
					classes[j] = mapID(classes[j])
				}
			}
		}
	}
	rewrite(prog.Main)
	for i, fn := range prog.Funcs {
		if keep[i] {
			rewrite(fn)
		}
	}
	for _, m := range merged {
		for _, meth := range m.proto.Methods {
			rewrite(meth.Func)
		}
	}
	for _, i := range order {
		fn := prog.Funcs[i]
		vm.Program.Funcs = append(vm.Program.Funcs, fn)
		if !bytecode.AnonymousFunc(fn.Name) {
			if _, ok := vm.funcNames[strings.ToLower(fn.Name)]; !ok {
				vm.funcNames[strings.ToLower(fn.Name)] = len(vm.Program.Funcs) - 1
			}
			if vm.funcSites == nil {
				vm.funcSites = make(map[string]declSite)
			}
			if site, ok := decls.funcs[strings.ToLower(fn.Name)]; ok {
				vm.funcSites[strings.ToLower(fn.Name)] = site
			}
		}
	}
	for _, m := range merged {
		c := m.proto
		vm.ownedClasses[c] = true
		vm.classState[c.ID] = false
		if m.replace != nil {
			vm.replaceClass(m.replace, c)
		} else if c.Kind == bytecode.KindTrait {
			vm.Program.Traits = append(vm.Program.Traits, c)
			vm.indexClass(c)
		} else {
			vm.Program.Classes = append(vm.Program.Classes, c)
			vm.indexClass(c)
		}
		vm.pendingClasses = append(vm.pendingClasses, pendingClass{id: c.ID, topLevel: m.site.topLevel, file: m.site.file, line: m.site.line})
		// PHP réserve la place d'une classe dans sa table dès la compilation du fichier, même si la liaison est différée : get_declared_classes suit cet ordre.
		vm.declOrder = append(vm.declOrder, declEntry{id: c.ID})
	}
	if prog.NeedsSuperglobals {
		vm.Program.NeedsSuperglobals = true
	}
	vm.bindPendingAll()
	vm.autoloadPendingInterfaces()
	return !vm.stop && !vm.control
}

// declareInUse formule l'erreur fatale de PHP pour une classe, une interface, un trait ou un enum dont le nom est déjà pris.
func declareInUse(c *bytecode.ClassProto) string {
	kind := "class"
	switch c.Kind {
	case bytecode.KindInterface:
		kind = "interface"
	case bytecode.KindTrait:
		kind = "trait"
	case bytecode.KindEnum:
		kind = "enum"
	}
	return "Cannot declare " + kind + " " + strings.TrimPrefix(c.Name, `\`) + ", because the name is already in use"
}

// unmodeledInterfaces liste les interfaces internes de PHP que la VM ne modélise pas : elles ne retardent jamais une liaison et ne déclenchent jamais l'autoloader, comme en PHP où elles sont toujours déclarées.
var unmodeledInterfaces = map[string]bool{
	"serializable": true, "splobserver": true, "splsubject": true, "outeriterator": true, "recursiveiterator": true,
	"sessionhandlerinterface": true, "sessionidinterface": true, "sessionupdatetimestamphandlerinterface": true,
	"internaliterator": true,
}

// interfaceReady indique qu'une interface citée par implements ou extends est déclarée ou interne. Une
// interface absente après la tentative d'autoload laisse la classe en attente : settlePending lève alors
// l'Error de PHP à la fin du fichier qui la déclare.
func (vm *VM) interfaceReady(name string) bool {
	clean := strings.ToLower(strings.TrimPrefix(name, `\`))
	if _, builtin := bytecode.BuiltinClassID(clean); builtin || unmodeledInterfaces[clean] {
		return true
	}
	_, ok := vm.declaredClassByName(clean, false)
	return ok
}

// autoloadPendingInterfaces tente l'autoload des interfaces manquantes des classes en attente dont le parent et les traits sont prêts, puis relie : PHP autocharge ces interfaces à la déclaration de la classe, avant le code qui suit.
func (vm *VM) autoloadPendingInterfaces() {
	for progress := true; progress && !vm.stop && !vm.control; {
		progress = false
		var names []string
		for _, p := range vm.pendingClasses {
			c := vm.classByID(p.id)
			if c == nil || !vm.classDepsReadyStructural(c) {
				continue
			}
			for _, iname := range c.Interfaces {
				if !vm.interfaceReady(iname) {
					names = append(names, iname)
				}
			}
		}
		for _, iname := range names {
			clean := strings.ToLower(strings.TrimPrefix(iname, `\`))
			if vm.ifaceTried[clean] {
				continue
			}
			if vm.ifaceTried == nil {
				vm.ifaceTried = make(map[string]bool)
			}
			vm.ifaceTried[clean] = true
			progress = true
			vm.declaredClassByName(iname, true)
			if vm.stop || vm.control {
				return
			}
		}
		if progress {
			vm.bindPendingAll()
		}
	}
}

// funcSite donne le lieu de déclaration d'une fonction utilisateur pour le message de redéclaration.
func (vm *VM) funcSite(fn *bytecode.OpArray) declSite {
	key := strings.ToLower(fn.Name)
	if site, ok := vm.funcSites[key]; ok {
		return site
	}
	if !vm.mainDeclDone {
		vm.mainDeclDone = true
		vm.mainDecl = mainFileDeclarations(vm.Program.Main)
	}
	if vm.mainDecl != nil {
		if site, ok := vm.mainDecl.funcs[key]; ok {
			return site
		}
	}
	line := uint32(0)
	if len(fn.Code) > 0 {
		line = fn.Code[0].Line
	}
	return declSite{file: fn.Filename, line: line}
}

func (vm *VM) pendingIndex(id uint32) int {
	for i, p := range vm.pendingClasses {
		if p.id == id {
			return i
		}
	}
	return -1
}

// classDepsReady vérifie que le parent, les traits et les interfaces d'une classe sont déclarés.
func (vm *VM) classDepsReady(c *bytecode.ClassProto) bool {
	if !vm.classDepsReadyStructural(c) {
		return false
	}
	for _, iname := range c.Interfaces {
		if !vm.interfaceReady(iname) {
			return false
		}
	}
	return true
}

// classDepsReadyStructural vérifie que le parent et les traits d'une classe sont déclarés.
func (vm *VM) classDepsReadyStructural(c *bytecode.ClassProto) bool {
	if c.ParentName != "" {
		if _, builtin := bytecode.BuiltinClassID(c.ParentName); !builtin {
			if _, ok := vm.declaredClassByName(c.ParentName, false); !ok {
				return false
			}
		}
	}
	for _, t := range c.Traits {
		if _, ok := vm.declaredClassByName(t, false); !ok {
			return false
		}
	}
	return true
}

// bindPendingAll lie jusqu'au point fixe les classes en attente dont les dépendances sont devenues disponibles, puis rafraîchit leurs descendantes.
func (vm *VM) bindPendingAll() {
	if len(vm.pendingClasses) == 0 {
		return
	}
	// Une classe du programme principal en attente reste un prototype partagé : la copie du programme précède toute liaison.
	vm.cloneProgram()
	var bound []uint32
	for progress := true; progress; {
		progress = false
		for i := 0; i < len(vm.pendingClasses); i++ {
			c := vm.classByID(vm.pendingClasses[i].id)
			if c == nil {
				vm.pendingClasses = slices.Delete(vm.pendingClasses, i, i+1)
				i--
				continue
			}
			if !vm.classDepsReady(c) {
				continue
			}
			site := vm.pendingClasses[i]
			vm.pendingClasses = slices.Delete(vm.pendingClasses, i, i+1)
			i--
			own := vm.ownClass(c)
			vm.bindClass(own)
			if msg := vm.unimplementedAbstract(own); msg != "" {
				vm.fatalAt(msg, site.file, site.line)
				return
			}
			bound = append(bound, c.ID)
			progress = true
		}
	}
	if len(bound) > 0 {
		vm.refreshDescendants(bound)
	}
}

func (vm *VM) bindClass(c *bytecode.ClassProto) {
	if c.ParentName != "" {
		if id, ok := bytecode.BuiltinClassID(c.ParentName); ok {
			c.ParentID = id
		} else if p, ok := vm.declaredClassByName(c.ParentName, false); ok {
			c.ParentID = p.ID
		}
	}
	vm.flattenTraitsInto(c)
	vm.classState[c.ID] = true
	vm.resolveClassProps(c)
}

// traitMembers rassemble les méthodes et propriétés d'un trait, y compris celles des traits qu'il utilise.
func (vm *VM) traitMembers(t *bytecode.ClassProto, depth int) ([]bytecode.MethodProto, []bytecode.PropInfo) {
	methods := slices.Clone(t.Methods)
	props := slices.Clone(t.Props)
	if depth >= 16 {
		return methods, props
	}
	for _, name := range t.Traits {
		inner, ok := vm.declaredClassByName(name, false)
		if !ok || inner.Kind != bytecode.KindTrait {
			continue
		}
		im, ip := vm.traitMembers(inner, depth+1)
		for _, m := range im {
			if !slices.ContainsFunc(methods, func(x bytecode.MethodProto) bool { return strings.EqualFold(x.Name, m.Name) }) {
				methods = append(methods, m)
			}
		}
		for _, p := range ip {
			if !slices.ContainsFunc(props, func(x bytecode.PropInfo) bool { return x.Name == p.Name }) {
				props = append(props, p)
			}
		}
	}
	return methods, props
}

// flattenTraitsInto copie dans une classe les membres de ses traits déclarés ailleurs, comme bytecode.Link le fait pour les traits du même fichier.
func (vm *VM) flattenTraitsInto(c *bytecode.ClassProto) {
	for _, name := range c.Traits {
		t, ok := vm.declaredClassByName(name, false)
		if !ok || t.Kind != bytecode.KindTrait {
			continue
		}
		methods, props := vm.traitMembers(t, 0)
		for _, m := range methods {
			if slices.ContainsFunc(c.Methods, func(x bytecode.MethodProto) bool { return strings.EqualFold(x.Name, m.Name) }) {
				continue
			}
			m.ClassID = c.ID
			c.Methods = append(c.Methods, m)
		}
		base := uint32(len(c.Props))
		for _, p := range props {
			if slices.ContainsFunc(c.Props, func(x bytecode.PropInfo) bool { return x.Name == p.Name }) {
				continue
			}
			p.Index = base
			base++
			c.Props = append(c.Props, p)
		}
	}
}

// resolveClassProps recalcule les propriétés d'instance résolues d'une classe sur sa chaîne d'héritage courante, comme bytecode.resolveClassMeta.
func (vm *VM) resolveClassProps(c *bytecode.ClassProto) {
	var chain []*bytecode.ClassProto
	for cur := c; cur != nil && len(chain) < 64; {
		chain = append(chain, cur)
		if cur.ParentID == bytecode.ClassNone || cur.ParentID == cur.ID || cur.ParentID < bytecode.ClassUser {
			break
		}
		if _, builtin := bytecode.BuiltinClassName(cur.ParentID); builtin {
			break
		}
		cur = vm.classByID(cur.ParentID)
	}
	index := uint32(0)
	if c.ID >= bytecode.ClassUser && vm.isExceptionFamily(c.ID) {
		index = 4
	}
	resolved := make([]bytecode.PropInfo, 0, len(c.Props))
	for _, cl := range slices.Backward(chain) {
		for _, prop := range cl.Props {
			if prop.Static {
				continue
			}
			replaced := false
			for j := range resolved {
				if resolved[j].Name == prop.Name && resolved[j].Visibility != bytecode.VisPrivate {
					prop.Index = resolved[j].Index
					resolved[j] = prop
					replaced = true
					break
				}
			}
			if !replaced {
				prop.Index = index
				index++
				resolved = append(resolved, prop)
			}
		}
	}
	c.ResolvedProps = resolved
	c.InstancePropCount = index
	c.HasDestructor = false
	for _, cl := range chain {
		for _, m := range cl.Methods {
			if strings.EqualFold(m.Name, "__destruct") && m.Func != nil {
				c.HasDestructor = true
			}
		}
	}
	c.PropsResolved = true
}

// refreshDescendants recalcule les classes déclarées dont un ancêtre ou un trait vient d'être lié ; un prototype partagé est cloné avant modification.
func (vm *VM) refreshDescendants(bound []uint32) {
	boundSet := make(map[uint32]bool, len(bound))
	traitNames := make(map[string]bool)
	for _, id := range bound {
		boundSet[id] = true
		if c := vm.classByID(id); c != nil && c.Kind == bytecode.KindTrait {
			traitNames[strings.ToLower(c.Name)] = true
		}
	}
	touches := func(c *bytecode.ClassProto) bool {
		for cur, depth := c, 0; cur != nil && depth < 64; depth++ {
			if cur != c && boundSet[cur.ID] {
				return true
			}
			for _, t := range cur.Traits {
				if traitNames[strings.ToLower(strings.TrimPrefix(t, `\`))] {
					return true
				}
			}
			if cur.ParentID < bytecode.ClassUser || cur.ParentID == cur.ID {
				return false
			}
			cur = vm.classByID(cur.ParentID)
		}
		return false
	}
	for i, c := range vm.Program.Classes {
		if c == nil || boundSet[c.ID] || c.Kind == bytecode.KindTrait || !vm.classDeclared(c) || !touches(c) {
			continue
		}
		if !vm.ownedClasses[c] {
			c = vm.ownClassAt(i, c)
		}
		vm.flattenTraitsInto(c)
		vm.resolveClassProps(c)
	}
}

// ownClass rend une copie propre à la requête d'un prototype partagé avant sa modification, et la substitue dans la tranche de programme et l'index.
func (vm *VM) ownClass(c *bytecode.ClassProto) *bytecode.ClassProto {
	if vm.ownedClasses[c] {
		return c
	}
	for _, list := range [][]*bytecode.ClassProto{vm.Program.Classes, vm.Program.Traits} {
		for i, cur := range list {
			if cur == c {
				cp := vm.copyClass(c)
				list[i] = cp
				vm.classIndex[cp.ID] = cp
				return cp
			}
		}
	}
	return c
}

func (vm *VM) ownClassAt(i int, c *bytecode.ClassProto) *bytecode.ClassProto {
	cp := vm.copyClass(c)
	vm.Program.Classes[i] = cp
	vm.classIndex[cp.ID] = cp
	return cp
}

func (vm *VM) copyClass(c *bytecode.ClassProto) *bytecode.ClassProto {
	cp := *c
	cp.Methods = slices.Clone(c.Methods)
	cp.Props = slices.Clone(c.Props)
	cp.Traits = slices.Clone(c.Traits)
	cp.Interfaces = slices.Clone(c.Interfaces)
	cp.ResolvedProps = nil
	vm.ownedClasses[&cp] = true
	return &cp
}

// bindPendingNamed tente de lier une classe en attente de ce nom en autochargeant ses dépendances manquantes.
func (vm *VM) bindPendingNamed(name string) bool {
	for _, p := range vm.pendingClasses {
		c := vm.classByID(p.id)
		if c == nil || !strings.EqualFold(c.Name, strings.TrimPrefix(name, `\`)) {
			continue
		}
		guard := "\x00pending:" + strings.ToLower(c.Name)
		if vm.autoloading[guard] {
			return false
		}
		if vm.autoloading == nil {
			vm.autoloading = make(map[string]bool)
		}
		vm.autoloading[guard] = true
		vm.autoloadDeps(c)
		delete(vm.autoloading, guard)
		if vm.stop || vm.control {
			return false
		}
		vm.bindPendingAll()
		return vm.classState[p.id]
	}
	return false
}

func (vm *VM) autoloadDeps(c *bytecode.ClassProto) {
	names := make([]string, 0, 1+len(c.Traits)+len(c.Interfaces))
	if c.ParentName != "" {
		names = append(names, c.ParentName)
	}
	names = append(names, c.Traits...)
	for _, iname := range c.Interfaces {
		// Une interface déjà soumise à l'autoloader par autoloadPendingInterfaces ne l'est pas une seconde fois.
		clean := strings.ToLower(strings.TrimPrefix(iname, `\`))
		if !vm.ifaceTried[clean] {
			names = append(names, iname)
		}
	}
	for _, name := range names {
		if _, builtin := bytecode.BuiltinClassID(name); builtin {
			continue
		}
		vm.declaredClassByName(name, true)
		if vm.stop || vm.control {
			return
		}
	}
}

// autoloadClass invoque dans l'ordre les rappels de spl_autoload_register jusqu'à ce que la classe soit déclarée, avec une garde contre la réentrance sur un même nom.
func (vm *VM) autoloadClass(name string) bool {
	if len(vm.autoloaders) == 0 || vm.stop {
		return false
	}
	name = strings.Clone(strings.TrimPrefix(name, `\`))
	key := strings.ToLower(name)
	if vm.autoloading[key] {
		return false
	}
	if vm.autoloading == nil {
		vm.autoloading = make(map[string]bool)
	}
	vm.autoloading[key] = true
	defer delete(vm.autoloading, key)
	for i := 0; i < len(vm.autoloaders); i++ {
		call, ok := vm.callback(vm.autoloaders[i])
		if !ok {
			continue
		}
		arg, err := types.NewStringLiteral(vm.Zone, name)
		if err != nil {
			vm.fatalFeature("autoload")
			return false
		}
		line := vm.line
		ret, threw := vm.invokeCallback(call, []types.Value{arg}, line)
		vm.line = line
		vmRelease(vm.Zone, &ret)
		if threw || vm.stop || vm.control {
			return false
		}
		if _, found := vm.declaredClassByName(name, false); found {
			return true
		}
	}
	return false
}

// settlePending tranche, à la fin d'un fichier inclus, les classes de niveau fichier restées sans parent ni trait : autoload des dépendances puis Error comme PHP.
// opDeclareClass exécute la déclaration d'une classe au point où PHP la déclare : une classe restée en attente de
// liaison voit ses dépendances autochargées puis est liée ; une dépendance toujours absente lève l'Error de PHP
// avant l'instruction suivante. Une classe déjà liée, ou hissée au début de son fichier, est sans effet.
func (vm *VM) opDeclareClass(id uint32) {
	c := vm.classByID(id)
	if c == nil || c.ID != id {
		vm.bump()
		return
	}
	// Pour le programme principal, l'attente de liaison est établie au premier examen de la classe.
	vm.classDeclared(c)
	if vm.pendingIndex(id) < 0 {
		vm.bump()
		return
	}
	vm.autoloadDeps(c)
	if vm.stop || vm.control {
		return
	}
	vm.bindPendingAll()
	i := vm.pendingIndex(id)
	if i < 0 {
		vm.bump()
		return
	}
	missing, kind := vm.missingDependency(c)
	if missing == "" {
		// Dépendances présentes mais liaison refusée : la fin du fichier tranche comme auparavant.
		vm.bump()
		return
	}
	vm.pendingClasses = slices.Delete(vm.pendingClasses, i, i+1)
	vm.raiseBuiltin(bytecode.ClassError, kind+` "`+strings.TrimPrefix(missing, `\`)+`" not found`, 0)
}

// missingDependency nomme la première dépendance d'une classe encore absente : parent, puis trait, puis interface.
func (vm *VM) missingDependency(c *bytecode.ClassProto) (string, string) {
	if c.ParentName != "" {
		if _, ok := vm.declaredClassByName(c.ParentName, false); !ok {
			return c.ParentName, "Class"
		}
	}
	for _, t := range c.Traits {
		if _, ok := vm.declaredClassByName(t, false); !ok {
			return t, "Trait"
		}
	}
	for _, iname := range c.Interfaces {
		if !vm.interfaceReady(iname) {
			return iname, "Interface"
		}
	}
	return "", ""
}

func (vm *VM) settlePending(file string) {
	var ids []uint32
	for _, p := range vm.pendingClasses {
		if p.file == file {
			ids = append(ids, p.id)
		}
	}
	for _, id := range ids {
		i := vm.pendingIndex(id)
		if i < 0 {
			continue
		}
		p := vm.pendingClasses[i]
		c := vm.classByID(id)
		if c == nil {
			continue
		}
		vm.autoloadDeps(c)
		if vm.stop || vm.control {
			return
		}
		vm.bindPendingAll()
		i = vm.pendingIndex(id)
		if i < 0 || !p.topLevel {
			continue
		}
		missing, kind := vm.missingDependency(c)
		if kind == "" {
			missing, kind = c.ParentName, "Class"
		}
		vm.pendingClasses = slices.Delete(vm.pendingClasses, i, i+1)
		obj := vm.makeException(bytecode.ClassError, kind+` "`+strings.TrimPrefix(missing, `\`)+`" not found`, 0, p.file, p.line)
		vm.excTrace = nil
		if len(vm.frames) == 0 {
			vm.uncaught(obj)
			return
		}
		vm.throwAt(vm.frame().pc, obj)
		return
	}
}

// resolveIncludePath applique l'ordre de recherche de PHP : chemin absolu ou relatif explicite, puis include_path, puis répertoire du script appelant, puis répertoire courant.
func (vm *VM) resolveIncludePath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	exists := func(p string) (string, bool) {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			return "", false
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", false
		}
		if canonical, err := filepath.EvalSymlinks(abs); err == nil {
			abs = canonical
		}
		return abs, true
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
		return exists(path)
	}
	for dir := range strings.SplitSeq(vm.iniValue("include_path"), ":") {
		if dir == "" {
			continue
		}
		if p, ok := exists(filepath.Join(dir, path)); ok {
			return p, true
		}
	}
	if caller := vm.file(); filepath.IsAbs(caller) {
		if p, ok := exists(filepath.Join(filepath.Dir(caller), path)); ok {
			return p, true
		}
	}
	return exists(path)
}

// includeFailure reproduit les diagnostics de PHP pour un fichier introuvable : deux Warning et false pour include, un Warning puis Error pour require.
func (vm *VM) includeFailure(kind, path string, line uint32) {
	vm.emitWarning(kind+"("+path+"): Failed to open stream: No such file or directory", line)
	if vm.stop || vm.control {
		return
	}
	incPath := vm.iniValue("include_path")
	if strings.HasPrefix(kind, "require") {
		vm.raiseBuiltin(bytecode.ClassError, "Failed opening required '"+path+"' (include_path='"+incPath+"')", 0)
		return
	}
	vm.emitWarning(kind+"(): Failed opening '"+path+"' for inclusion (include_path='"+incPath+"')", line)
	if vm.stop || vm.control {
		return
	}
	vm.push(types.NewBool(false))
	vm.bump()
}

// globalRefFor rend une référence retenue sur la variable globale name, créée au besoin comme le fait bindGlobal.
func (vm *VM) globalRefFor(name string) (types.Value, bool) {
	root := &vm.frames[0]
	for i, n := range root.fn.CVNames {
		if n != name {
			continue
		}
		slot := &vm.slots[root.cvBase+i]
		if slot.Type != types.TypeReference {
			old := *slot
			v := old
			if v.IsUndef() {
				v = types.NewNull()
			}
			r, err := types.NewReference(vm.Zone, v)
			if err != nil {
				vm.fatalFeature("reference globale")
				return types.Value{}, false
			}
			*slot = r
			vmRelease(vm.Zone, &old)
		}
		materializeGlobal(vm.Zone, *slot)
		return types.RetainReference(vm.Zone, *slot), true
	}
	ref := vm.globals[name]
	if ref.Type != types.TypeReference {
		initial := ref
		if initial.IsUndef() {
			initial = types.NewNull()
		}
		r, err := types.NewReference(vm.Zone, initial)
		if err != nil {
			vm.fatalFeature("reference globale")
			return types.Value{}, false
		}
		vmRelease(vm.Zone, &ref)
		vm.globals[name] = r
		ref = r
	}
	materializeGlobal(vm.Zone, ref)
	return types.RetainReference(vm.Zone, ref), true
}

// scopeRef rend une référence retenue sur la variable name de la portée du cadre owner : variable compilée, sinon table globale à la racine, sinon variables dynamiques de la fonction.
func (vm *VM) scopeRef(owner int, name string) (types.Value, bool) {
	ofr := &vm.frames[owner]
	for i, n := range ofr.fn.CVNames {
		if n != name {
			continue
		}
		slot := &vm.slots[ofr.cvBase+i]
		if slot.Type != types.TypeReference {
			ref, err := types.NewReference(vm.Zone, *slot)
			if err != nil {
				vm.fatalFeature("reference de portee")
				return types.Value{}, false
			}
			*slot = ref
		}
		return types.RetainReference(vm.Zone, *slot), true
	}
	var locals map[string]types.Value
	if owner == 0 {
		locals = vm.globals
	} else {
		if ofr.dynamicLocals == nil {
			ofr.dynamicLocals = make(map[string]types.Value)
		}
		locals = ofr.dynamicLocals
	}
	cur, ok := locals[name]
	if !ok || cur.Type != types.TypeReference {
		ref, err := types.NewReference(vm.Zone, cur)
		if err != nil {
			vm.fatalFeature("reference de portee")
			return types.Value{}, false
		}
		locals[name] = ref
		cur = ref
	}
	return types.RetainReference(vm.Zone, cur), true
}

// bindOwnerGlobal applique par avance à la portée propriétaire un global $name que le fichier inclus déclare, pour que la fonction appelante voie la liaison.
func (vm *VM) bindOwnerGlobal(owner int, name string) bool {
	g, ok := vm.globalRefFor(name)
	if !ok {
		return false
	}
	ofr := &vm.frames[owner]
	for i, n := range ofr.fn.CVNames {
		if n == name {
			slot := &vm.slots[ofr.cvBase+i]
			old := *slot
			*slot = g
			vmRelease(vm.Zone, &old)
			return true
		}
	}
	if ofr.dynamicLocals == nil {
		ofr.dynamicLocals = make(map[string]types.Value)
	}
	if old, had := ofr.dynamicLocals[name]; had {
		vmRelease(vm.Zone, &old)
	}
	ofr.dynamicLocals[name] = g
	return true
}

func (vm *VM) opSource(in bytecode.Instr) {
	input := vm.pop()
	derefIn := types.Deref(vm.Zone, input)
	tainted := (input.Flags|derefIn.Flags)&types.FlagTainted != 0
	s, owned := vm.stringify(input)
	if s.Flags&types.FlagTainted != 0 {
		tainted = true
	}
	source := strings.Clone(types.StringData(vm.Zone, s))
	if owned {
		types.ReleaseValue(vm.Zone, &s)
	}
	types.ReleaseValue(vm.Zone, &input)
	if vm.stop || vm.control {
		return
	}
	if tainted && vm.Context != nil {
		vm.Context.TaintedSinks++
		if vm.Context.BlockTaintedSinks {
			vm.raiseBuiltin(bytecode.ClassValueError, "Tainted input rejected at "+in.Op.String(), 0)
			return
		}
	}
	filename := fmt.Sprintf("%s(%d) : eval()'d code", vm.file(), in.Line)
	isInclude := in.Op == bytecode.OpInclude
	var prog *bytecode.Program
	var decls *fileDecls
	if isInclude {
		kind := vm.frame().fn.Strings[in.Op1]
		resolved, ok := vm.resolveIncludePath(source)
		if !ok {
			vm.includeFailure(kind, source, in.Line)
			return
		}
		filename = resolved
		if strings.HasSuffix(kind, "_once") && vm.included[filename] {
			vm.push(types.NewBool(true))
			vm.bump()
			return
		}
		tmpl, readable, err := compiledIncludeFor(filename)
		if !readable {
			vm.includeFailure(kind, source, in.Line)
			return
		}
		if err != nil {
			if pe, patched := errors.AsType[includeReturnError](err); patched {
				vm.fatalFeature(pe.err.Error())
			} else {
				fmt.Fprintf(vm.Out, "\n%s\n", err)
				vm.exit, vm.stop = 255, true
			}
			return
		}
		prog, decls = cloneCompiledInclude(tmpl.prog), tmpl.decls
		if vm.included == nil {
			vm.included = make(map[string]bool)
		}
		vm.included[filename] = true
	} else {
		file, err := parser.Parse("<?php "+source, filename)
		if err != nil {
			fmt.Fprintf(vm.Out, "\n%s\n", err)
			vm.exit, vm.stop = 255, true
			return
		}
		decls = collectDecls(file)
		prog, err = compiler.Compile(file)
		if err != nil {
			fmt.Fprintf(vm.Out, "\n%s\n", err)
			vm.exit, vm.stop = 255, true
			return
		}
	}
	if (prog.NeedsSuperglobals || !prog.Main.CVMetaResolved) && !vm.superglobalsInit {
		vm.initSuperglobals()
		vm.superglobalsInit = true
	}
	if !vm.linkDynamic(prog, decls, filename) {
		return
	}
	if vm.stop {
		return
	}
	callerIndex := len(vm.frames) - 1
	caller := vm.frame()
	owner := callerIndex
	if caller.includeScope > 0 {
		owner = caller.includeScope - 1
	}
	if owner != 0 {
		for _, ins := range prog.Main.Code {
			if ins.Op == bytecode.OpBindGlobal && int(ins.Op1) < len(prog.Main.CVNames) {
				if !vm.bindOwnerGlobal(owner, prog.Main.CVNames[ins.Op1]) {
					return
				}
			}
		}
	}
	caller = vm.frame()
	if !vm.pushFrame(prog.Main, caller.calledClass, caller.scopeClass, caller.this, caller.hasThis, in.Line) {
		return
	}
	vm.frame().includeScope = owner + 1
	for i, name := range prog.Main.CVNames {
		if name == "" || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "$") || isSuperglobal(name) || name == "GLOBALS" {
			continue
		}
		if name == "this" && !slices.Contains(vm.frames[owner].fn.CVNames, "this") {
			continue
		}
		ref, ok := vm.scopeRef(owner, name)
		if !ok {
			return
		}
		slot := &vm.slots[vm.frame().cvBase+i]
		if slot.Type != types.TypeUndef {
			types.ReleaseValue(vm.Zone, slot)
		}
		*slot = ref
	}
	vm.execFrames(callerIndex + 1)
	val, threw := vm.takeReturn()
	if vm.stop || threw {
		return
	}
	if len(vm.pendingClasses) > 0 {
		vm.control = false
		vm.settlePending(filename)
		if vm.stop || vm.control {
			vmRelease(vm.Zone, &val)
			return
		}
	}
	vm.control = false
	vm.push(val)
	vm.bump()
}

// checkMainClasses applique avant la première instruction du programme principal le contrôle que linkDynamic
// applique aux classes d'un fichier inclus. Une classe dont le parent, un trait ou une interface n'est pas encore
// déclaré attend sa liaison par un fichier inclus ou par l'autoloader, et settleMainPending la tranche au retour
// du programme ; une classe concrète déjà liée qui laisse sans corps une méthode abstraite héritée ou imposée par
// une interface arrête la requête avec l'erreur fatale de PHP. Faux si la requête est arrêtée.
func (vm *VM) checkMainClasses() bool {
	for _, c := range vm.Program.Classes {
		if c == nil || c.Kind != bytecode.KindClass || len(c.Interfaces) == 0 && c.ParentName == "" {
			continue
		}
		if _, builtin := bytecode.BuiltinClassName(c.ID); builtin || c.ID < bytecode.ClassUser {
			continue
		}
		if !vm.classDeclared(c) {
			continue
		}
		if msg := vm.unimplementedAbstract(c); msg != "" {
			site := vm.mainPendingEntry(c)
			vm.fatalAt(msg, site.file, site.line)
			return false
		}
	}
	return !vm.stop
}

// settleMainPending tranche, au retour du programme principal, ses classes de niveau fichier restées en attente :
// autoload des dépendances puis liaison, ou Error « Interface/Class/Trait "X" not found » comme PHP à la déclaration.
func (vm *VM) settleMainPending() {
	main := vm.Program.Main
	if !vm.mainReturned || len(vm.frames) != 1 || main == nil {
		return
	}
	if !slices.ContainsFunc(vm.pendingClasses, func(p pendingClass) bool { return p.topLevel && p.file == main.Filename }) {
		return
	}
	vm.stop, vm.control = false, false
	vm.settlePending(main.Filename)
	if !vm.stop {
		vm.stop, vm.control, vm.exit = true, true, 0
	}
}

// unimplementedAbstract rend le message fatal de PHP pour une classe concrète qui laisse sans corps une
// méthode imposée par une interface utilisateur ou une méthode abstraite héritée ; vide si la classe est complète.
func (vm *VM) unimplementedAbstract(c *bytecode.ClassProto) string {
	if c == nil || c.Kind != bytecode.KindClass || c.Flags&bytecode.ClassAbstract != 0 {
		return ""
	}
	var missing []string
	seen := map[string]bool{}
	require := func(owner, method string) {
		key := strings.ToLower(method)
		if seen[key] {
			return
		}
		seen[key] = true
		if m, _, ok := vm.findMethod(c.ID, method); ok && !m.Abstract {
			return
		}
		missing = append(missing, strings.TrimPrefix(owner, `\`)+"::"+method)
	}
	var visitIface func(name string, depth int)
	visitIface = func(name string, depth int) {
		id, ok := vm.resolveName(name)
		if !ok || id < bytecode.ClassUser || depth > 32 {
			return
		}
		ic := vm.classByID(id)
		if ic == nil || ic.Kind != bytecode.KindInterface {
			return
		}
		for _, m := range ic.Methods {
			require(ic.Name, m.Name)
		}
		for _, parent := range ic.Interfaces {
			visitIface(parent, depth+1)
		}
	}
	for cur, depth := c, 0; cur != nil && depth < 64; depth++ {
		for _, m := range cur.Methods {
			if m.Abstract && cur != c {
				require(cur.Name, m.Name)
			}
		}
		for _, iname := range cur.Interfaces {
			visitIface(iname, 0)
		}
		if cur.ParentID == bytecode.ClassNone || cur.ParentID < bytecode.ClassUser || cur.ParentID == cur.ID {
			break
		}
		cur = vm.classByID(cur.ParentID)
	}
	if len(missing) == 0 {
		return ""
	}
	plural := ""
	if len(missing) > 1 {
		plural = "s"
	}
	list := missing
	if len(list) > 3 {
		list = append(slices.Clone(list[:3]), "...")
	}
	return "Class " + strings.TrimPrefix(c.Name, `\`) + " contains " + strconv.Itoa(len(missing)) + " abstract method" + plural +
		" and must therefore be declared abstract or implement the remaining methods (" + strings.Join(list, ", ") + ")"
}
