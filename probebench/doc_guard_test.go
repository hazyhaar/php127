//go:build linux && amd64

package probebench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
)

// TestDocIntegrityAndAnchors enforces the documentary and structural invariants
// established during the Mythos audit remediation (B5, B6, M4, M8, M12):
//  1. types.Value is strictly 16 bytes (two 64-bit registers), and no article/report
//     claims a 24-byte / 3-register Value layout (B5).
//  2. No synthetic CMOS energy / Joule / electron model appears in the articles or
//     probebench reports (B6).
//  3. HighN iteration counts and buffer sizes cited in the articles match probebench_test.go
//     (M4: 100 100 / 20 100 / 2 010 / 5 050 / 50 100 / 10 100 / 1 010, 1 290 bytes).
//  4. Code anchors cited in the articles exist in the php127 source tree (M12).
func TestDocIntegrityAndAnchors(t *testing.T) {
	if got := unsafe.Sizeof(types.Value{}); got != 16 {
		t.Fatalf("unsafe.Sizeof(types.Value{}) = %d, want 16", got)
	}

	docsDir := filepath.Clean("../docs")
	docxPath := filepath.Join(docsDir, "article_x_micro_design_for_cybersafety.docx")
	if info, err := os.Stat(docxPath); err != nil || info.Size() == 0 {
		t.Fatalf("missing or empty %s: %v", docxPath, err)
	}
	if _, err := os.Stat(filepath.Join(docsDir, "article_x_micro_design_for_cybersafety.md")); err != nil {
		docsDir = "/devhoros/php127/docs"
	}
	docFiles := []string{
		"article_x_micro_design_for_cybersafety_fr.md",
		"article_x_micro_design_for_cybersafety.md",
		"article_micro_design_for_cybersafety_fr.html",
		"rapport_probebench_go_vs_rust.md",
	}

	forbiddenSubstrings := []string{
		"24-Byte",
		"24 bytes",
		"24 octets",
		"trois registres",
		"three 64-bit",
		"CMOS",
		"électrons",
		"electrons",
		"Te⁻",
		"Ge⁻",
		"V_dd",
		"q_e",
		"Aux uint16",
		"parsePairs",
		"ErrTaintedSink",
		"2 969",
		"2,969",
		"5 848",
		"5,848",
		"15 473 616",
	}

	for _, name := range docFiles {
		path := filepath.Join(docsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(data)
		for _, bad := range forbiddenSubstrings {
			if strings.Contains(content, bad) {
				t.Errorf("%s contains forbidden substring %q (B5/B6/M6/B3/B4 regression)", name, bad)
			}
		}
	}

	// Check required accurate metrics/counts (B5, M4, M8, M12, M1, M6) in FR and EN articles.
	requiredFR := []string{
		"16 octets",
		"deux registres",
		"Extra uint16",
		"insertParsedPair",
		"ValueError",
		"100 100",
		"20 100",
		"2 010",
		"5 050",
		"50 100",
		"1 010",
		"10 100",
		"1 290 octets",
		"195 instructions",
		"ResolvedProps",
		"FlagTainted",
		"OpEval",
		"OpInclude",
		"1,25",
		"10,56",
	}
	frData, err := os.ReadFile(filepath.Join(docsDir, "article_x_micro_design_for_cybersafety_fr.md"))
	if err != nil {
		t.Fatalf("read FR article: %v", err)
	}
	frText := string(frData)
	for _, req := range requiredFR {
		if !strings.Contains(frText, req) {
			t.Errorf("article_x_micro_design_for_cybersafety_fr.md missing required exact token %q", req)
		}
	}

	requiredEN := []string{
		"16-Byte",
		"two 64-bit",
		"Extra uint16",
		"insertParsedPair",
		"ValueError",
		"100,100",
		"20,100",
		"2,010",
		"5,050",
		"50,100",
		"1,010",
		"10,100",
		"1,290-byte",
		"195 instructions",
		"ResolvedProps",
		"FlagTainted",
		"OpEval",
		"OpInclude",
		"1.25x to 10.56x",
	}
	enData, err := os.ReadFile(filepath.Join(docsDir, "article_x_micro_design_for_cybersafety.md"))
	if err != nil {
		t.Fatalf("read EN article: %v", err)
	}
	enText := string(enData)
	for _, req := range requiredEN {
		if !strings.Contains(enText, req) {
			t.Errorf("article_x_micro_design_for_cybersafety.md missing required exact token %q", req)
		}
	}

	// Verify code anchors in the repository (M12).
	codeAnchors := []struct {
		relPath string
		symbol  string
	}{
		{"../types/value.go", "FlagTainted"},
		{"../sapi/errors.go", "FlagTainted"},
		{"../sapi/url.go", "newTaintedStr"},
		{"../vm/source.go", "FlagTainted"},
		{"../vm/source.go", "OpInclude"},
		{"../vm/vm.go", "OpEval"},
		{"../bytecode/program.go", "ResolvedProps"},
		{"../bytecode/program.go", "PropIC"},
		{"../bytecode/program.go", "CallIC"},
		{"../bytecode/opcode.go", "FlagAssignDimCV"},
		{"../vm/vmcheck_on.go", "checkSlotsAfterPop"},
	}
	for _, anchor := range codeAnchors {
		src, err := os.ReadFile(filepath.Clean(anchor.relPath))
		if err != nil {
			t.Fatalf("read code anchor file %s: %v", anchor.relPath, err)
		}
		if !strings.Contains(string(src), anchor.symbol) {
			t.Errorf("code anchor %q not found in %s (M12 regression)", anchor.symbol, anchor.relPath)
		}
	}
}
