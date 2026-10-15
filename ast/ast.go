// Package ast represente la syntaxe PHP sans liens vers les parents.
package ast

type Position struct{ Line, Pos int }

func (p Position) Location() Position { return p }

type Node interface{ Location() Position }
type Stmt interface {
	Node
	stmt()
}
type Expr interface {
	Node
	expr()
}
type ClassMember interface {
	Node
	member()
}
type stmtBase struct{ Position }

func (stmtBase) stmt() {}

type exprBase struct{ Position }

func (exprBase) expr() {}

type memberBase struct{ Position }

func (memberBase) member() {}

// Les bases exportees permettent de construire des arbres hors du paquet.
type StmtBase = stmtBase
type ExprBase = exprBase
type MemberBase = memberBase

type File struct {
	Position
	Filename    string
	Stmts       []Stmt
	StrictTypes bool
}
type Modifiers uint16

const (
	Public Modifiers = 1 << iota
	Protected
	Private
	Static
	Abstract
	Final
	Readonly
)

type TypeRef struct {
	Position
	Name                string
	Nullable            bool
	Union, Intersection []TypeRef
}
type Attribute struct {
	Position
	Name string
	Args []Arg
}
type Param struct {
	Position
	Name            string
	Type            TypeRef
	Default         Expr
	ByRef, Variadic bool
	Modifiers       Modifiers
	Attrs           []Attribute
}
type Arg struct {
	Position
	Name   string
	Value  Expr
	Unpack bool
}
type ArrayItem struct {
	Position
	Key, Value    Expr
	ByRef, Unpack bool
}
type PropertyItem struct {
	Position
	Name    string
	Default Expr
}
type ConstItem struct {
	Position
	Name  string
	Value Expr
}
type ClosureUse struct {
	Position
	Name  string
	ByRef bool
}
type ElseIf struct {
	Position
	Cond Expr
	Body []Stmt
}
type SwitchCase struct {
	Position
	Cond Expr
	Body []Stmt
}
type CatchClause struct {
	Position
	Types []string
	Var   string
	Body  []Stmt
}
type MatchArm struct {
	Position
	Conds   []Expr
	Default bool
	Value   Expr
}
type UseItem struct {
	Position
	Name, Alias, Kind string
}
type TraitAdaptation struct {
	Position
	Trait, Method, Alias string
	InsteadOf            []string
	Modifiers            Modifiers
}

type EchoStmt struct {
	StmtBase
	Exprs []Expr
}
type ExprStmt struct {
	StmtBase
	Expr Expr
}
type ReturnStmt struct {
	StmtBase
	Expr Expr
}
type BreakStmt struct {
	StmtBase
	Level Expr
}
type ContinueStmt struct {
	StmtBase
	Level Expr
}
type GotoStmt struct {
	StmtBase
	Label string
}
type LabelStmt struct {
	StmtBase
	Label string
}
type IfStmt struct {
	StmtBase
	Cond    Expr
	Body    []Stmt
	ElseIfs []ElseIf
	Else    []Stmt
}
type WhileStmt struct {
	StmtBase
	Cond Expr
	Body []Stmt
}
type DoWhileStmt struct {
	StmtBase
	Cond Expr
	Body []Stmt
}
type ForStmt struct {
	StmtBase
	Init, Cond, Loop []Expr
	Body             []Stmt
}
type ForeachStmt struct {
	StmtBase
	Expr, Key, Val Expr
	ByRef          bool
	Body           []Stmt
}
type SwitchStmt struct {
	StmtBase
	Cond  Expr
	Cases []SwitchCase
}
type TryStmt struct {
	StmtBase
	Body    []Stmt
	Catches []CatchClause
	Finally []Stmt
}
type ThrowStmt struct {
	StmtBase
	Expr Expr
}
type FunctionDecl struct {
	StmtBase
	Name       string
	ByRef      bool
	Params     []Param
	ReturnType TypeRef
	Body       []Stmt
	Attrs      []Attribute
}
type ClassDecl struct {
	StmtBase
	Name       string
	Modifiers  Modifiers
	Extends    string
	Implements []string
	Members    []ClassMember
	Attrs      []Attribute
}
type InterfaceDecl struct {
	StmtBase
	Name    string
	Extends []string
	Members []ClassMember
	Attrs   []Attribute
}
type TraitDecl struct {
	StmtBase
	Name    string
	Members []ClassMember
	Attrs   []Attribute
}
type EnumDecl struct {
	StmtBase
	Name, ScalarType string
	Implements       []string
	Members          []ClassMember
	Attrs            []Attribute
}
type NamespaceDecl struct {
	StmtBase
	Name      string
	Body      []Stmt
	Bracketed bool
}
type UseDecl struct {
	StmtBase
	Items []UseItem
}
type ConstDecl struct {
	StmtBase
	Consts []ConstItem
}
type GlobalStmt struct {
	StmtBase
	Vars []Expr
}
type StaticVarStmt struct {
	StmtBase
	Vars []PropertyItem
}
type UnsetStmt struct {
	StmtBase
	Vars []Expr
}
type DeclareStmt struct {
	StmtBase
	Directives []ConstItem
	Body       []Stmt
}
type InlineHTMLStmt struct {
	StmtBase
	Text string
}
type NopStmt struct{ StmtBase }
type BlockStmt struct {
	StmtBase
	Body []Stmt
}
type HaltCompilerStmt struct {
	StmtBase
	Offset int
	Data   string
}

type PropertyDecl struct {
	MemberBase
	Modifiers Modifiers
	Type      TypeRef
	Props     []PropertyItem
	Attrs     []Attribute
}
type ClassConstDecl struct {
	MemberBase
	Modifiers Modifiers
	Type      TypeRef
	Consts    []ConstItem
	Attrs     []Attribute
}
type MethodDecl struct {
	MemberBase
	Modifiers  Modifiers
	Name       string
	ByRef      bool
	Params     []Param
	ReturnType TypeRef
	Body       []Stmt
	HasBody    bool
	Attrs      []Attribute
}
type TraitUseDecl struct {
	MemberBase
	Traits      []string
	Adaptations []TraitAdaptation
}
type EnumCaseDecl struct {
	MemberBase
	Name  string
	Value Expr
	Attrs []Attribute
}

type IntLit struct {
	ExprBase
	Value int64
	Raw   string
}
type FloatLit struct {
	ExprBase
	Value float64
	Raw   string
}
type StringLit struct {
	ExprBase
	Value, Raw string
	Quote      byte
}
type EncapsedStringExpr struct {
	ExprBase
	Parts []Expr
	Raw   string
	Kind  string
}
type ArrayExpr struct {
	ExprBase
	Items []ArrayItem
	List  bool
}
type ConstFetchExpr struct {
	ExprBase
	Name string
}
type MagicConstExpr struct {
	ExprBase
	Name string
}
type VarExpr struct {
	ExprBase
	Name    string
	Dynamic Expr
}
type DimFetchExpr struct {
	ExprBase
	Var, Dim Expr
}
type PropFetchExpr struct {
	ExprBase
	Object, Property Expr
	Nullsafe         bool
}
type StaticPropFetchExpr struct {
	ExprBase
	Class, Property Expr
}
type ClassConstFetchExpr struct {
	ExprBase
	Class, Const Expr
}
type UnaryExpr struct {
	ExprBase
	Op   string
	Expr Expr
}
type IncDecExpr struct {
	ExprBase
	Op   string
	Expr Expr
	Post bool
}
type BinaryExpr struct {
	ExprBase
	Op          string
	Left, Right Expr
}
type AssignExpr struct {
	ExprBase
	Op          string
	Left, Right Expr
	ByRef       bool
}
type ListAssignExpr struct {
	ExprBase
	Items []ArrayItem
	Expr  Expr
}
type TernaryExpr struct {
	ExprBase
	Cond, If, Else Expr
}
type MatchExpr struct {
	ExprBase
	Cond Expr
	Arms []MatchArm
}
type YieldExpr struct {
	ExprBase
	Key, Value Expr
}
type YieldFromExpr struct {
	ExprBase
	Expr Expr
}
type PrintExpr struct {
	ExprBase
	Expr Expr
}
type IncludeExpr struct {
	ExprBase
	Kind string
	Expr Expr
}
type EvalExpr struct {
	ExprBase
	Expr Expr
}
type IssetExpr struct {
	ExprBase
	Exprs []Expr
}
type EmptyExpr struct {
	ExprBase
	Expr Expr
}
type ExitExpr struct {
	ExprBase
	Expr Expr
}
type ThrowExpr struct {
	ExprBase
	Expr Expr
}
type CallExpr struct {
	ExprBase
	Func       Expr
	Args       []Arg
	FirstClass bool
}
type MethodCallExpr struct {
	ExprBase
	Object, Method       Expr
	Args                 []Arg
	Nullsafe, FirstClass bool
}
type StaticCallExpr struct {
	ExprBase
	Class, Method Expr
	Args          []Arg
	FirstClass    bool
}
type NewExpr struct {
	ExprBase
	Class     Expr
	Args      []Arg
	Anonymous *ClassDecl
}
type ClosureExpr struct {
	ExprBase
	Static, ByRef bool
	Params        []Param
	Uses          []ClosureUse
	ReturnType    TypeRef
	Body          []Stmt
	Attrs         []Attribute
}
type ArrowFuncExpr struct {
	ExprBase
	Static, ByRef bool
	Params        []Param
	ReturnType    TypeRef
	Expr          Expr
	Attrs         []Attribute
}
