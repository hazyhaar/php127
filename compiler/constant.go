package compiler

import (
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/ast"
	bc "code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func typeName(t ast.TypeRef) string {
	name := t.Name
	parts, sep := t.Union, "|"
	if len(t.Intersection) > 0 {
		parts, sep = t.Intersection, "&"
	}
	if len(parts) > 0 {
		names := make([]string, len(parts))
		for i, part := range parts {
			names[i] = typeName(part)
		}
		name = strings.Join(names, sep)
	}
	if t.Nullable {
		name += "|null"
	}
	return name
}

func (e *emitter) magicConstText(kind string, line int) (int64, string, bool) {
	switch strings.ToUpper(kind) {
	case "__LINE__":
		if line <= 0 {
			line = int(e.line)
		}
		return int64(line), "", false
	case "__FILE__":
		return 0, e.file.Filename, true
	case "__DIR__":
		if e.file.Filename == "" {
			return 0, ".", true
		}
		return 0, filepath.Dir(e.file.Filename), true
	case "__FUNCTION__":
		if e.fn != nil && e.fn.Name != "{main}" {
			return 0, e.fn.Name, true
		}
		return 0, "", true
	case "__CLASS__":
		if e.class != nil && e.class.Kind != bc.KindTrait {
			return 0, e.class.Name, true
		}
		return 0, "", true
	case "__TRAIT__":
		if e.class != nil && e.class.Kind == bc.KindTrait {
			return 0, e.class.Name, true
		}
		return 0, "", true
	case "__METHOD__":
		if e.class != nil && e.fn != nil && e.fn.Name != "{main}" {
			return 0, e.class.Name + "::" + e.fn.Name, true
		}
		if e.fn != nil && e.fn.Name != "{main}" {
			return 0, e.fn.Name, true
		}
		return 0, "", true
	case "__NAMESPACE__":
		return 0, e.namespace, true
	}
	return 0, "", true
}

func constToInt(c *bc.Constant) int64 {
	if c == nil {
		return 0
	}
	if c.IsString {
		n, _ := strconv.ParseInt(strings.TrimSpace(c.Text), 10, 64)
		return n
	}
	switch c.Value.Type {
	case types.TypeLong:
		return c.Value.ToLong()
	case types.TypeDouble:
		return int64(c.Value.ToDouble())
	case types.TypeTrue:
		return 1
	}
	return 0
}

func constToFloat(c *bc.Constant) (float64, bool) {
	if c == nil {
		return 0, false
	}
	if c.IsString {
		f, err := strconv.ParseFloat(strings.TrimSpace(c.Text), 64)
		if err == nil && strings.ContainsAny(c.Text, ".eE") {
			return f, true
		}
		return float64(constToInt(c)), false
	}
	switch c.Value.Type {
	case types.TypeDouble:
		return c.Value.ToDouble(), true
	case types.TypeLong:
		return float64(c.Value.ToLong()), false
	case types.TypeTrue:
		return 1, false
	}
	return 0, false
}

func constToString(c *bc.Constant) string {
	if c == nil {
		return ""
	}
	if c.IsString {
		return c.Text
	}
	switch c.Value.Type {
	case types.TypeLong:
		return strconv.FormatInt(c.Value.ToLong(), 10)
	case types.TypeDouble:
		return strconv.FormatFloat(c.Value.ToDouble(), 'g', -1, 64)
	case types.TypeTrue:
		return "1"
	}
	return ""
}

func constToBool(c *bc.Constant) bool {
	if c == nil {
		return false
	}
	if c.IsArray {
		return len(c.Items) > 0
	}
	if c.IsString {
		return c.Text != "" && c.Text != "0"
	}
	switch c.Value.Type {
	case types.TypeLong:
		return c.Value.ToLong() != 0
	case types.TypeDouble:
		return c.Value.ToDouble() != 0
	case types.TypeTrue:
		return true
	}
	return false
}

func (e *emitter) constant(x ast.Expr) *bc.Constant {
	if x == nil {
		return &bc.Constant{Value: types.NewNull()}
	}
	switch n := x.(type) {
	case *ast.ArrayExpr:
		c := &bc.Constant{IsArray: true, Items: make([]bc.ConstantItem, 0, len(n.Items))}
		for _, item := range n.Items {
			if item.Value == nil {
				continue
			}
			val := e.constant(item.Value)
			if item.Unpack && val.IsArray {
				c.Items = append(c.Items, val.Items...)
				continue
			}
			entry := bc.ConstantItem{Value: *val}
			if item.Key != nil {
				entry.Key = e.constant(item.Key)
			}
			c.Items = append(c.Items, entry)
		}
		return c
	case *ast.MagicConstExpr:
		iv, sv, isStr := e.magicConstText(n.Name, n.Line)
		if isStr {
			return &bc.Constant{Text: sv, IsString: true}
		}
		return &bc.Constant{Value: types.NewLong(iv)}
	case *ast.EncapsedStringExpr:
		var sb strings.Builder
		for _, part := range n.Parts {
			sb.WriteString(constToString(e.constant(part)))
		}
		return &bc.Constant{Text: sb.String(), IsString: true}
	case *ast.ClassConstFetchExpr:
		cname := e.name(n.Class)
		mname := e.name(n.Const)
		if strings.EqualFold(mname, "class") {
			switch strings.ToLower(cname) {
			case "self", "static":
				if e.class != nil {
					return &bc.Constant{Text: e.class.Name, IsString: true}
				}
			case "parent":
				if e.class != nil && e.class.ParentName != "" {
					return &bc.Constant{Text: e.class.ParentName, IsString: true}
				}
			}
			return &bc.Constant{Text: strings.TrimPrefix(cname, `\`), IsString: true}
		}
		var target *bc.ClassProto
		switch strings.ToLower(cname) {
		case "self", "static":
			target = e.class
		case "parent":
			if e.class != nil {
				target = e.classes[strings.ToLower(strings.TrimPrefix(e.class.ParentName, `\`))]
			}
		default:
			target = e.classes[strings.ToLower(strings.TrimPrefix(cname, `\`))]
		}
		for cur := target; cur != nil; cur = e.classes[strings.ToLower(strings.TrimPrefix(cur.ParentName, `\`))] {
			for _, cst := range cur.Constants {
				if cst.Name == mname && cst.Initial != nil {
					cp := *cst.Initial
					return &cp
				}
			}
			if cur.ParentName == "" {
				break
			}
		}
		return &bc.Constant{Value: types.NewLong(0)}
	case *ast.UnaryExpr:
		c := e.constant(n.Expr)
		switch n.Op {
		case "+":
			if f, isF := constToFloat(c); isF {
				return &bc.Constant{Value: types.NewDouble(f)}
			}
			return &bc.Constant{Value: types.NewLong(constToInt(c))}
		case "-":
			if f, isF := constToFloat(c); isF {
				return &bc.Constant{Value: types.NewDouble(-f)}
			}
			return &bc.Constant{Value: types.NewLong(-constToInt(c))}
		case "~":
			return &bc.Constant{Value: types.NewLong(^constToInt(c))}
		case "!":
			return &bc.Constant{Value: types.NewBool(!constToBool(c))}
		case "@":
			return c
		}
		return c
	case *ast.BinaryExpr:
		l := e.constant(n.Left)
		if n.Op == "??" {
			if !l.IsString && !l.IsArray && l.Value.IsNull() {
				return e.constant(n.Right)
			}
			return l
		}
		r := e.constant(n.Right)
		switch n.Op {
		case ".":
			return &bc.Constant{Text: constToString(l) + constToString(r), IsString: true}
		case "+":
			if l.IsArray && r.IsArray {
				out := &bc.Constant{IsArray: true, Items: append([]bc.ConstantItem(nil), l.Items...)}
				out.Items = append(out.Items, r.Items...)
				return out
			}
			lf, lisF := constToFloat(l)
			rf, risF := constToFloat(r)
			if lisF || risF {
				return &bc.Constant{Value: types.NewDouble(lf + rf)}
			}
			return &bc.Constant{Value: types.NewLong(constToInt(l) + constToInt(r))}
		case "-":
			lf, lisF := constToFloat(l)
			rf, risF := constToFloat(r)
			if lisF || risF {
				return &bc.Constant{Value: types.NewDouble(lf - rf)}
			}
			return &bc.Constant{Value: types.NewLong(constToInt(l) - constToInt(r))}
		case "*":
			lf, lisF := constToFloat(l)
			rf, risF := constToFloat(r)
			if lisF || risF {
				return &bc.Constant{Value: types.NewDouble(lf * rf)}
			}
			return &bc.Constant{Value: types.NewLong(constToInt(l) * constToInt(r))}
		case "/":
			lf, _ := constToFloat(l)
			rf, _ := constToFloat(r)
			if rf == 0 {
				return &bc.Constant{Value: types.NewLong(0)}
			}
			li, ri := constToInt(l), constToInt(r)
			if ri != 0 && float64(li) == lf && float64(ri) == rf && li%ri == 0 {
				return &bc.Constant{Value: types.NewLong(li / ri)}
			}
			return &bc.Constant{Value: types.NewDouble(lf / rf)}
		case "%":
			ri := constToInt(r)
			if ri == 0 {
				return &bc.Constant{Value: types.NewLong(0)}
			}
			return &bc.Constant{Value: types.NewLong(constToInt(l) % ri)}
		case "**":
			lf, _ := constToFloat(l)
			rf, _ := constToFloat(r)
			res := math.Pow(lf, rf)
			if float64(int64(res)) == res {
				return &bc.Constant{Value: types.NewLong(int64(res))}
			}
			return &bc.Constant{Value: types.NewDouble(res)}
		case "|":
			return &bc.Constant{Value: types.NewLong(constToInt(l) | constToInt(r))}
		case "&":
			return &bc.Constant{Value: types.NewLong(constToInt(l) & constToInt(r))}
		case "^":
			return &bc.Constant{Value: types.NewLong(constToInt(l) ^ constToInt(r))}
		case "<<":
			shift := constToInt(r)
			if shift < 0 || shift >= 64 {
				return &bc.Constant{Value: types.NewLong(0)}
			}
			return &bc.Constant{Value: types.NewLong(constToInt(l) << uint(shift))}
		case ">>":
			shift := constToInt(r)
			if shift < 0 {
				return &bc.Constant{Value: types.NewLong(0)}
			}
			a := constToInt(l)
			if shift >= 64 {
				if a < 0 {
					return &bc.Constant{Value: types.NewLong(-1)}
				}
				return &bc.Constant{Value: types.NewLong(0)}
			}
			return &bc.Constant{Value: types.NewLong(a >> uint(shift))}
		case "==", "===":
			if l.IsString || r.IsString {
				return &bc.Constant{Value: types.NewBool(constToString(l) == constToString(r))}
			}
			return &bc.Constant{Value: types.NewBool(constToInt(l) == constToInt(r))}
		case "!=", "!==", "<>":
			if l.IsString || r.IsString {
				return &bc.Constant{Value: types.NewBool(constToString(l) != constToString(r))}
			}
			return &bc.Constant{Value: types.NewBool(constToInt(l) != constToInt(r))}
		case "<":
			return &bc.Constant{Value: types.NewBool(constToInt(l) < constToInt(r))}
		case "<=":
			return &bc.Constant{Value: types.NewBool(constToInt(l) <= constToInt(r))}
		case ">":
			return &bc.Constant{Value: types.NewBool(constToInt(l) > constToInt(r))}
		case ">=":
			return &bc.Constant{Value: types.NewBool(constToInt(l) >= constToInt(r))}
		case "&&", "and":
			return &bc.Constant{Value: types.NewBool(constToBool(l) && constToBool(r))}
		case "||", "or":
			return &bc.Constant{Value: types.NewBool(constToBool(l) || constToBool(r))}
		case "xor":
			return &bc.Constant{Value: types.NewBool(constToBool(l) != constToBool(r))}
		}
	case *ast.TernaryExpr:
		cond := e.constant(n.Cond)
		if constToBool(cond) {
			if n.If != nil {
				return e.constant(n.If)
			}
			return cond
		}
		return e.constant(n.Else)
	case *ast.DimFetchExpr:
		base := e.constant(n.Var)
		dim := e.constant(n.Dim)
		if base.IsArray {
			idx := int64(0)
			for _, item := range base.Items {
				if item.Key != nil {
					if constToString(item.Key) == constToString(dim) {
						cp := item.Value
						return &cp
					}
				} else {
					if !dim.IsString && idx == constToInt(dim) {
						cp := item.Value
						return &cp
					}
					idx++
				}
			}
		}
		return &bc.Constant{Value: types.NewNull()}
	case *ast.NewExpr:
		return &bc.Constant{Value: types.NewNull()}
	}
	v, s, text := e.literal(x)
	return &bc.Constant{Value: v, Text: s, IsString: text}
}
