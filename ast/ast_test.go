package ast

import "testing"

func TestNodeCategoriesAndLocations(t *testing.T) {
	pos := Position{Line: 7, Pos: 42}
	statements := []Stmt{
		&EchoStmt{Position: pos}, &FunctionDecl{Position: pos},
		&ClassDecl{Position: pos}, &EnumDecl{Position: pos},
	}
	expressions := []Expr{
		&VarExpr{Position: pos}, &BinaryExpr{Position: pos},
		&ClosureExpr{Position: pos}, &MatchExpr{Position: pos},
	}
	members := []ClassMember{
		&MethodDecl{Position: pos}, &PropertyDecl{Position: pos},
		&ClassConstDecl{Position: pos}, &EnumCaseDecl{Position: pos},
	}
	for _, stmt := range statements {
		if stmt.Location() != pos {
			t.Errorf("%T: %+v", stmt, stmt.Location())
		}
		if _, ok := any(stmt).(Expr); ok {
			t.Errorf("%T est aussi une expression", stmt)
		}
	}
	for _, expr := range expressions {
		if expr.Location() != pos {
			t.Errorf("%T: %+v", expr, expr.Location())
		}
		if _, ok := any(expr).(Stmt); ok {
			t.Errorf("%T est aussi une instruction", expr)
		}
	}
	for _, member := range members {
		if member.Location() != pos {
			t.Errorf("%T: %+v", member, member.Location())
		}
		if _, ok := any(member).(Stmt); ok {
			t.Errorf("%T est aussi une instruction", member)
		}
	}
}
