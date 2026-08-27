// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/vfs: Tests unitaires du système de fichiers virtuel et PSR-4

package vfs

import (
	"testing"
)

func TestVFSFileOperations(t *testing.T) {
	v := NewVFS()
	v.RegisterFile("/app/config.php", []byte("<?php return ['env' => 'prod'];"))

	content, err := v.ReadFile("/app/config.php")
	if err != nil {
		t.Fatalf("Lecture fichier échouée: %v", err)
	}
	if string(content) != "<?php return ['env' => 'prod'];" {
		t.Fatalf("Contenu inattendu: %s", string(content))
	}

	// Path traversal protection
	_, err = v.ReadFile("/app/../etc/passwd")
	if err != ErrPathTraversal {
		t.Fatalf("Attendu ErrPathTraversal, obtenu: %v", err)
	}

	// Include once tracking
	first := v.MarkIncluded("/app/config.php")
	if first != false {
		t.Fatalf("Premier include doit renvoyer false (non inclus auparavant)")
	}
	second := v.MarkIncluded("/app/config.php")
	if second != true {
		t.Fatalf("Deuxième include doit renvoyer true (déjà inclus)")
	}
}

func TestVFSPSR4Autoloading(t *testing.T) {
	v := NewVFS()
	v.RegisterPSR4("App\\Services\\", "/var/www/src/Services")
	v.RegisterFile("/var/www/src/Services/UserService.php", []byte("<?php class UserService {}"))

	path, content, err := v.ResolvePSR4("App\\Services\\UserService")
	if err != nil {
		t.Fatalf("Résolution PSR-4 échouée: %v", err)
	}
	if path != "/var/www/src/Services/UserService.php" {
		t.Fatalf("Chemin résolu incorrect: %s", path)
	}
	if len(content) == 0 {
		t.Fatalf("Contenu vide")
	}

	// Unresolved class
	_, _, err = v.ResolvePSR4("App\\Unknown\\NotFound")
	if err != ErrClassUnresolved {
		t.Fatalf("Attendu ErrClassUnresolved, got: %v", err)
	}
}
