// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/streams: Tests unitaires des flux virtuels et fonctions de fichiers

package streams

import (
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vfs"
)

func TestStreamOperations(t *testing.T) {
	v := vfs.NewVFS()
	v.RegisterFile("/app/views/home.blade.php", []byte("<h1>Welcome {{ $name }}</h1>"))

	sm := NewStreamManager(v)
	sm.SetInputBuffer([]byte(`{"action":"login","user":"admin"}`))

	// php://input
	in := sm.FileGetContents(types.NewString("php://input"))
	if in.String() != `{"action":"login","user":"admin"}` {
		t.Fatalf("php://input inattendu: %s", in.String())
	}

	// VFS file_get_contents
	tpl := sm.FileGetContents(types.NewString("/app/views/home.blade.php"))
	if tpl.String() != "<h1>Welcome {{ $name }}</h1>" {
		t.Fatalf("Contenu template inattendu: %s", tpl.String())
	}

	// php://memory
	sm.FilePutContents(types.NewString("php://memory/cache"), types.NewString("cached_payload_data"))
	cache := sm.FileGetContents(types.NewString("php://memory/cache"))
	if cache.String() != "cached_payload_data" {
		t.Fatalf("php://memory inattendu: %s", cache.String())
	}

	// Dirname & Basename
	dir := Dirname(types.NewString("/var/www/html/index.php"))
	if dir.String() != "/var/www/html" {
		t.Fatalf("Dirname inattendu: %s", dir.String())
	}

	base := Basename(types.NewString("/var/www/html/index.php"))
	if base.String() != "index.php" {
		t.Fatalf("Basename inattendu: %s", base.String())
	}
}
