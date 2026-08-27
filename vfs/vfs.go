// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/vfs: Système de fichiers virtuel hermétique et résolveur PSR-4

package vfs

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrFileNotFound    = errors.New("vfs: fichier introuvable")
	ErrPathTraversal   = errors.New("vfs: tentative de traversée de répertoire non autorisée")
	ErrClassUnresolved = errors.New("vfs: classe introuvable par PSR-4")
)

type VFS struct {
	mu           sync.RWMutex
	files        map[string][]byte
	includedOnce map[string]bool
	psr4Prefixes map[string]string // prefix => virtual dir
	allowedRoots []string
}

func NewVFS() *VFS {
	return &VFS{
		files:        make(map[string][]byte),
		includedOnce: make(map[string]bool),
		psr4Prefixes: make(map[string]string),
		allowedRoots: []string{"/"},
	}
}

// RegisterFile enregistre un fichier virtuel en mémoire
func (v *VFS) RegisterFile(path string, content []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	cleanPath := filepath.Clean(path)
	v.files[cleanPath] = content
}

// ReadFile lit le contenu d'un fichier avec contrôle de traversée
func (v *VFS) ReadFile(path string) ([]byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	cleanPath := filepath.Clean(path)
	if strings.Contains(path, "../") || strings.Contains(path, "..\\") {
		return nil, ErrPathTraversal
	}

	content, ok := v.files[cleanPath]
	if !ok {
		return nil, ErrFileNotFound
	}
	return content, nil
}

// MarkIncluded enregistre l'inclusion d'un fichier et renvoie true s'il était déjà inclus
func (v *VFS) MarkIncluded(path string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	clean := filepath.Clean(path)
	if v.includedOnce[clean] {
		return true
	}
	v.includedOnce[clean] = true
	return false
}

// RegisterPSR4 associe un préfixe de namespace à un répertoire virtuel
func (v *VFS) RegisterPSR4(prefix string, dir string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	cleanPrefix := strings.Trim(prefix, "\\") + "\\"
	cleanDir := filepath.Clean(dir)
	v.psr4Prefixes[cleanPrefix] = cleanDir
}

// ResolvePSR4 résout le chemin virtuel d'une classe selon PSR-4
func (v *VFS) ResolvePSR4(className string) (string, []byte, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	normClass := strings.Trim(className, "\\")
	for prefix, baseDir := range v.psr4Prefixes {
		if strings.HasPrefix(normClass+"\\", prefix) || strings.HasPrefix(normClass, strings.TrimSuffix(prefix, "\\")) {
			relativeClass := strings.TrimPrefix(normClass, strings.TrimSuffix(prefix, "\\"))
			relativeClass = strings.TrimPrefix(relativeClass, "\\")
			filePath := filepath.Join(baseDir, strings.ReplaceAll(relativeClass, "\\", "/")+".php")
			cleanPath := filepath.Clean(filePath)
			if content, ok := v.files[cleanPath]; ok {
				return cleanPath, content, nil
			}
		}
	}

	return "", nil, ErrClassUnresolved
}
