package parser_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/parser"
)

func TestWordPressFullSweep(t *testing.T) {
	wpDir := "/devhoros/php127/testdata/wordpress"
	if _, err := os.Stat(wpDir); os.IsNotExist(err) {
		t.Skip("wordpress non present")
	}
	var files []string
	err := filepath.WalkDir(wpDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".php") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)

	parseOK := 0
	parseFail := 0
	compileOK := 0
	compileFail := 0
	parseReasons := make(map[string]int)
	compileReasons := make(map[string]int)
	var sampleParseFails []string

	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(wpDir, f)
		astFile, perr := parser.Parse(string(src), rel)
		if perr != nil {
			parseFail++
			msg := perr.Error()
			if idx := strings.Index(msg, " in "); idx > 0 {
				msg = msg[:idx]
			}
			parseReasons[msg]++
			if len(sampleParseFails) < 25 {
				sampleParseFails = append(sampleParseFails, rel+": "+perr.Error())
			}
			continue
		}
		parseOK++

		_, cerr := compiler.Compile(astFile)
		if cerr != nil {
			compileFail++
			msg := cerr.Error()
			if idx := strings.Index(msg, " in "); idx > 0 {
				msg = msg[:idx]
			}
			compileReasons[msg]++
			continue
		}
		compileOK++
	}

	t.Logf("=== BILAN WORDPRESS (%d fichiers .php) ===", len(files))
	t.Logf("1. PARSER (Lexer + AST + Validate) : %d / %d OK (%.2f%%), %d rejets",
		parseOK, len(files), float64(parseOK)*100/float64(len(files)), parseFail)
	for _, s := range sampleParseFails {
		t.Logf("   [PARSE FAIL] %s", s)
	}

	t.Logf("2. COMPILATEUR BYTECODE : %d / %d OK sur les fichiers parses (%.2f%%), %d rejets",
		compileOK, parseOK, float64(compileOK)*100/float64(max(1, parseOK)), compileFail)

	// Regrouper les "classe WP_*" et "classe parente absente: *" (qui viennent du fait que chaque fichier est compilé isolément sans autoloader/include)
	missingCrossFileClass := 0
	langReasons := make(map[string]int)
	for k, v := range compileReasons {
		if strings.Contains(k, "classe parente absente:") || strings.Contains(k, "fonctionnalite absente: classe ") {
			missingCrossFileClass += v
		} else {
			langReasons[k] += v
		}
	}
	t.Logf("   - Dont %d fichiers faisant reference a une classe/parente definie dans un autre fichier WordPress (compilation fichier par fichier sans include)", missingCrossFileClass)
	type kv struct {
		k string
		v int
	}
	var topLang []kv
	for k, v := range langReasons {
		topLang = append(topLang, kv{k, v})
	}
	sort.Slice(topLang, func(i, j int) bool { return topLang[i].v > topLang[j].v })
	for _, item := range topLang {
		t.Logf("   [LANG FEATURE x%d] %s", item.v, item.k)
	}
	if parseFail > 0 || compileFail > 0 {
		t.Fatalf("echec du sweep WordPress: parseFail=%d, compileFail=%d", parseFail, compileFail)
	}
}
