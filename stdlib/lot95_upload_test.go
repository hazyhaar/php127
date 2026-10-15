package stdlib

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Seul un chemin inscrit par RegisterUploadedFile satisfait is_uploaded_file ; move_uploaded_file le
// déplace puis le retire de l'ensemble ; z.Reset efface l'ensemble avec le slot de la Zone.
func TestLot95UploadedFiles(t *testing.T) {
	z := zone.New(1 << 20)
	dir := t.TempDir()
	tmp := filepath.Join(dir, "php-upload")
	other := filepath.Join(dir, "autre")
	dest := filepath.Join(dir, "dest.bin")
	for _, p := range []string{tmp, other} {
		if err := os.WriteFile(p, []byte("contenu"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	str := func(s string) types.Value { return lot95Str(t, z, s, false) }
	boolean := func(name string, args ...types.Value) bool {
		t.Helper()
		v, err := lot95Call(t, z, name, args...)
		if err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		if !v.IsBool() {
			t.Fatalf("%s : type %d", name, v.Type)
		}
		return v.Type == types.TypeTrue
	}
	if boolean("is_uploaded_file", str(tmp)) {
		t.Fatal("is_uploaded_file vrai avant inscription")
	}
	if boolean("move_uploaded_file", str(tmp), str(dest)) {
		t.Fatal("move_uploaded_file accepte un chemin non inscrit")
	}
	if z.Slot(2) != nil {
		t.Fatal("une simple consultation a créé l'état système")
	}
	// Le chemin inscrit est une vue empruntée sur l'arène, réécrite aussitôt : l'ensemble doit en tenir une copie.
	arena := str(tmp)
	RegisterUploadedFile(z, types.StringData(z, arena))
	ab := types.StringBytes(z, arena)
	ab[len(ab)-1] = 'X'
	if !boolean("is_uploaded_file", str(tmp)) {
		t.Fatal("is_uploaded_file faux après inscription (chemin non copié ?)")
	}
	if boolean("is_uploaded_file", str(other)) || boolean("move_uploaded_file", str(other), str(dest)) {
		t.Fatal("un fichier non inscrit passe pour téléversé")
	}
	if _, err := lot95Call(t, z, "is_uploaded_file", str("a\x00b")); !errors.As(err, new(ValueError)) ||
		err.Error() != "is_uploaded_file(): Argument #1 ($filename) must not contain any null bytes" {
		t.Fatalf("is_uploaded_file avec octet nul : %v", err)
	}
	if _, err := lot95Call(t, z, "move_uploaded_file", str(tmp), str("a\x00b")); !errors.As(err, new(ValueError)) ||
		err.Error() != "move_uploaded_file(): Argument #2 ($to) must not contain any null bytes" {
		t.Fatalf("move_uploaded_file avec octet nul : %v", err)
	}
	if boolean("move_uploaded_file", str(tmp), str(filepath.Join(dir, "absent", "x"))) {
		t.Fatal("move_uploaded_file vers un répertoire absent rend true")
	}
	if !boolean("is_uploaded_file", str(tmp)) {
		t.Fatal("un échec de déplacement a retiré le chemin de l'ensemble")
	}
	if !boolean("move_uploaded_file", str(tmp), str(dest)) {
		t.Fatal("move_uploaded_file a échoué")
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "contenu" {
		t.Fatalf("destination : %q, %v", b, err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("source encore présente : %v", err)
	}
	if boolean("is_uploaded_file", str(tmp)) || boolean("move_uploaded_file", str(tmp), str(dest)) {
		t.Fatal("le chemin déplacé reste inscrit")
	}
	RegisterUploadedFile(z, other)
	z.Reset(nil)
	if z.Slot(2) != nil || boolean("is_uploaded_file", str(other)) {
		t.Fatal("l'ensemble des fichiers téléversés survit à z.Reset")
	}
	z.Reset(nil)
}

// Le repli copie puis suppression sert lorsque rename(2) échoue ; un répertoire source le fait échouer
// des deux côtés et la fonction rend false sans retirer le chemin.
func TestLot95MoveUploadedFileCopyFallback(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("octets"), 0o600); err != nil {
		t.Fatal(err)
	}
	z := zone.New(1 << 16)
	defer z.Reset(nil)
	if err := copyFile(z, src, dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "octets" {
		t.Fatalf("copie : %q, %v", b, err)
	}
	srcDir := filepath.Join(dir, "rep")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "occupe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "occupe", "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	RegisterUploadedFile(z, srcDir)
	v, err := lot95Call(t, z, "move_uploaded_file", lot95Str(t, z, srcDir, false), lot95Str(t, z, filepath.Join(dir, "occupe"), false))
	if err != nil || v.Type != types.TypeFalse {
		t.Fatalf("move_uploaded_file vers un répertoire non vide = %v, %v ; false attendu", v, err)
	}
	if _, ok := sysStatePeek(z).uploads[srcDir]; !ok {
		t.Fatal("chemin retiré après échec")
	}
}
