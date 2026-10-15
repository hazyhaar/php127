package bytecode

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNativeRefSpecsSingleSourceOfTruth vérifie que la table des signatures natives par référence est cohérente
// avec elle-même (signature du compilateur, indices de la VM, présence) et qu'elle est l'unique source : aucun
// fichier des paquets compiler et vm ne déclare de nouveau une liste par nom de fonction native.
func TestNativeRefSpecsSingleSourceOfTruth(t *testing.T) {
	for name, spec := range nativeFuncRefs {
		params := NativeFunctionParams(name)
		if len(params) != len(spec.Params) || !NativeHasRefArgs(name) || !NativeHasRefArgs(`\`+name) {
			t.Fatalf("%s : signature %v, présence %v", name, params, NativeHasRefArgs(name))
		}
		anyRef := false
		for i := range len(params) + 4 {
			want := spec.ArgIsRef(i)
			anyRef = anyRef || want
			if NativeArgIsRef(name, i) != want {
				t.Fatalf("%s : argument %d", name, i)
			}
			// Le compilateur transmet un argument par référence d'après le paramètre de même rang, ou le dernier s'il est variadique.
			byRef := false
			if i < len(params) {
				byRef = params[i].ByRef
			} else if last := params[len(params)-1]; last.Variadic {
				byRef = last.ByRef
			}
			if byRef != want {
				t.Fatalf("%s : argument %d par référence pour la VM %v, pour le compilateur %v", name, i, want, byRef)
			}
		}
		if !anyRef {
			t.Fatalf("%s : aucun argument par référence", name)
		}
	}
	for _, tc := range []struct {
		name string
		refs []bool
	}{
		{"mb_convert_variables", []bool{false, false, true, true, true, true}},
		{"MB_Convert_Variables", []bool{false, false, true, true}},
		{"preg_match", []bool{false, false, true, false, false}},
		{"array_multisort", []bool{true, true, true, true}},
		{"sscanf", []bool{false, false, true, true, true}},
		{"strlen", []bool{false, false}},
		{"pfsockopen", []bool{false, false, true, true, false}},
		{"stream_select", []bool{true, true, true, false, false}},
		{"openssl_sign", []bool{false, true, false, false}},
		{"exec", []bool{false, true, true}},
		{"sort", []bool{true, false}},
		{"array_push", []bool{true, false, false, false}},
		{"array_splice", []bool{true, false, false, false}},
		{"end", []bool{true}},
		{"current", []bool{false}},
		{"key", []bool{false}},
	} {
		for i, want := range tc.refs {
			if NativeArgIsRef(tc.name, i) != want {
				t.Fatalf("%s : argument %d par référence %v, attendu %v", tc.name, i, !want, want)
			}
		}
	}
	if NativeFunctionParams("strlen") != nil || NativeHasRefArgs("strlen") {
		t.Fatal("strlen présente dans la table")
	}
	for method, refs := range map[string][]bool{
		"bindParam":   {false, true, false, false},
		"bindColumn":  {false, true, false},
		"bind_param":  {false, true, true, true},
		"BIND_RESULT": {true, true, true},
	} {
		params, ok := NativeMethodParams(method)
		if !ok || len(params) == 0 {
			t.Fatalf("méthode %s absente", method)
		}
		for i, want := range refs {
			if NativeMethodArgIsRef(method, i) != want {
				t.Fatalf("méthode %s : argument %d par référence %v, attendu %v", method, i, !want, want)
			}
		}
	}
	if _, ok := NativeMethodParams("query"); ok {
		t.Fatal("méthode query présente dans la table")
	}

	// Unicité : les noms qui ne servaient qu'aux listes dupliquées n'apparaissent plus en littéral dans compiler ni vm.
	sentinels := map[string]bool{}
	for _, name := range []string{"mysqli_stmt_bind_param", "mysqli_stmt_bind_result", "getimagesizefromstring", "stream_socket_client",
		"fsockopen", "openssl_random_pseudo_bytes", "headers_sent", "similar_text", "mb_convert_variables"} {
		if _, ok := nativeFuncRefs[name]; !ok {
			t.Fatalf("sentinelle %s absente de la table", name)
		}
		sentinels[name] = true
	}
	fset := token.NewFileSet()
	files := 0
	for _, dir := range []string{"../compiler", "../vm"} {
		paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			files++
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if v, err := strconv.Unquote(lit.Value); err == nil && sentinels[v] {
					t.Errorf("%s : liste de signatures natives dupliquée (%q)", fset.Position(lit.Pos()), v)
				}
				return true
			})
		}
	}
	if files == 0 {
		t.Fatal("aucun fichier des paquets compiler et vm examiné")
	}
}
