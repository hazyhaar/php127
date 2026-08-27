// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Arbre Syntaxique Abstrait (AST)

package compiler

type Node interface {
	isNode()
}

// Expressions
type IntLitExpr struct {
	Val int64
}

type FloatLitExpr struct {
	Val float64
}

type StringLitExpr struct {
	Val string
}

type BoolLitExpr struct {
	Val bool
}

type NullLitExpr struct{}

type VarExpr struct {
	Name string
}

type UnaryExpr struct {
	Op   TokenType
	Expr Node
}

type BinaryExpr struct {
	Op    TokenType
	Left  Node
	Right Node
}

type TernaryExpr struct {
	Cond        Node
	TrueBranch  Node
	FalseBranch Node
}

type ArrayElement struct {
	Key Node // Nil if list-indexed
	Val Node
}

type ArrayLitExpr struct {
	Elements []ArrayElement
}

type ArrayAccessExpr struct {
	Target Node
	Index  Node
}

type CallExpr struct {
	Callee string
	Args   []Node
}

type MatchArm struct {
	Conds []Node // nil or empty for default
	Body  Node
}

type MatchExpr struct {
	Expr Node
	Arms []MatchArm
}

type NewExpr struct {
	ClassName string
	Args      []Node
}

type MethodCallExpr struct {
	Target     Node
	Method     string
	Args       []Node
	IsNullsafe bool
}

type PropertyAccessExpr struct {
	Target     Node
	Property   string
	IsNullsafe bool
}

type StaticCallExpr struct {
	ClassName string
	Method    string
	Args      []Node
}

type StaticPropertyExpr struct {
	ClassName string
	Property  string
}

type InstanceOfExpr struct {
	Target    Node
	ClassName string
}

type IncludeExpr struct {
	Path      Node
	IsRequire bool
	IsOnce    bool
}

// Statements
type ExprStmt struct {
	Expr Node
}

type AssignStmt struct {
	Target Node // VarExpr, ArrayAccessExpr, PropertyAccessExpr, StaticPropertyExpr
	Op     TokenType
	Val    Node
}

type EchoStmt struct {
	Exprs []Node
}

type PrintStmt struct {
	Expr Node
}

type ReturnStmt struct {
	Val Node
}

type IfStmt struct {
	Cond     Node
	Body     []Node
	ElseBody []Node
}

type WhileStmt struct {
	Cond Node
	Body []Node
}

type ForStmt struct {
	Init []Node
	Cond Node
	Post []Node
	Body []Node
}

type ForeachStmt struct {
	Array  Node
	KeyVar string
	ValVar string
	Body   []Node
}

type SwitchCase struct {
	Values []Node // nil or empty for default
	Body   []Node
}

type SwitchStmt struct {
	Expr  Node
	Cases []SwitchCase
}

type BreakStmt struct{}

type ContinueStmt struct{}

type AttributeDecl struct {
	Name string
	Args []Node
}

type FuncDeclStmt struct {
	Name       string
	Attributes []AttributeDecl
	Params     []string
	Body       []Node
}

type PropertyDecl struct {
	Name       string
	Visibility TokenType // TokPublic, TokProtected, TokPrivate
	IsStatic   bool
	IsReadonly bool
	Attributes []AttributeDecl
	DefaultVal Node
}

type ParamDecl struct {
	Name       string
	TypeHint   string
	DefaultVal Node
	IsPromoted bool
	Visibility string
	IsReadonly bool
}

type MethodDecl struct {
	Name       string
	Visibility TokenType
	IsStatic   bool
	IsAbstract bool
	IsFinal    bool
	Attributes []AttributeDecl
	Params     []string
	ParamDecls []ParamDecl
	Body       []Node
}

type NamespaceStmt struct {
	Name  string
	Stmts []Node
}

type UseImport struct {
	Name  string
	Alias string
}

type UseStmt struct {
	Imports []UseImport
}

type EnumCase struct {
	Name string
	Val  Node
}

type EnumDeclStmt struct {
	Name       string
	BackedType string
	Cases      []EnumCase
	Methods    []MethodDecl
}

type ClassDeclStmt struct {
	Name        string
	Parent      string
	Implements  []string
	IsAbstract  bool
	IsFinal     bool
	IsInterface bool
	IsTrait     bool
	IsReadonly  bool
	Attributes  []AttributeDecl
	Traits      []string
	Constants   []ClassConstDecl
	Properties  []PropertyDecl
	Methods     []MethodDecl
}

type BlockStmt struct {
	Stmts []Node
}

type ThrowStmt struct {
	Expr Node
}

type CatchClause struct {
	Types []string
	Var   string
	Body  []Node
}

type TryCatchStmt struct {
	TryBody     []Node
	Catches     []CatchClause
	FinallyBody []Node
}

type ClosureExpr struct {
	Params []string
	Uses   []string
	Body   []Node
}

type ArrowFuncExpr struct {
	Params []string
	Expr   Node
}

type ClassConstDecl struct {
	Name string
	Val  Node
}

type TraitUseStmt struct {
	Traits []string
}

type GlobalStmt struct {
	Vars []string
}

// Tag methods
func (s NamespaceStmt) isNode() {}
func (s UseStmt) isNode()       {}
func (s EnumDeclStmt) isNode()  {}

// Tag methods
func (e IntLitExpr) isNode()         {}
func (e FloatLitExpr) isNode()       {}
func (e StringLitExpr) isNode()      {}
func (e BoolLitExpr) isNode()        {}
func (e NullLitExpr) isNode()        {}
func (e VarExpr) isNode()            {}
func (e UnaryExpr) isNode()          {}
func (e BinaryExpr) isNode()         {}
func (e TernaryExpr) isNode()        {}
func (e ArrayLitExpr) isNode()       {}
func (e ArrayAccessExpr) isNode()    {}
func (e CallExpr) isNode()           {}
func (e MatchExpr) isNode()          {}
func (e NewExpr) isNode()            {}
func (e MethodCallExpr) isNode()     {}
func (e PropertyAccessExpr) isNode() {}
func (e StaticCallExpr) isNode()     {}
func (e StaticPropertyExpr) isNode() {}
func (e InstanceOfExpr) isNode()     {}
func (e IncludeExpr) isNode()        {}
func (e ClosureExpr) isNode()        {}
func (e ArrowFuncExpr) isNode()      {}

func (s ExprStmt) isNode()       {}
func (s AssignStmt) isNode()     {}
func (s EchoStmt) isNode()       {}
func (s PrintStmt) isNode()      {}
func (s ReturnStmt) isNode()     {}
func (s IfStmt) isNode()         {}
func (s WhileStmt) isNode()      {}
func (s ForStmt) isNode()        {}
func (s ForeachStmt) isNode()    {}
func (s SwitchStmt) isNode()     {}
func (s BreakStmt) isNode()      {}
func (s ContinueStmt) isNode()   {}
func (s FuncDeclStmt) isNode()   {}
func (s ClassDeclStmt) isNode()  {}
func (s BlockStmt) isNode()      {}
func (s ThrowStmt) isNode()      {}
func (s TryCatchStmt) isNode()   {}
func (s ClassConstDecl) isNode() {}
func (s TraitUseStmt) isNode()   {}
func (s GlobalStmt) isNode()     {}
