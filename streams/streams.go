// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/streams: Flux virtuels PHP (php://input, memory) et opérations de fichiers étendues

package streams

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vfs"
)

type StreamManager struct {
	mu          sync.RWMutex
	vfs         *vfs.VFS
	memoryPool  map[string]*bytes.Buffer
	inputBuffer []byte
	httpClient  *http.Client
}

func NewStreamManager(v *vfs.VFS) *StreamManager {
	if v == nil {
		v = vfs.NewVFS()
	}
	return &StreamManager{
		vfs:        v,
		memoryPool: make(map[string]*bytes.Buffer),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (sm *StreamManager) SetInputBuffer(b []byte) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.inputBuffer = b
}

func (sm *StreamManager) FileGetContents(pathOrUrl types.Value) types.Value {
	target := pathOrUrl.String()

	// 1. PHP streams: php://input, php://memory
	if strings.HasPrefix(target, "php://") {
		streamName := strings.TrimPrefix(target, "php://")
		if streamName == "input" {
			sm.mu.RLock()
			defer sm.mu.RUnlock()
			return types.NewStringBytes(sm.inputBuffer)
		}
		if strings.HasPrefix(streamName, "memory") || strings.HasPrefix(streamName, "temp") {
			sm.mu.RLock()
			defer sm.mu.RUnlock()
			if buf, ok := sm.memoryPool[target]; ok {
				return types.NewStringBytes(buf.Bytes())
			}
			return types.NewString("")
		}
	}

	// 2. HTTP URL
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		resp, err := sm.httpClient.Get(target)
		if err != nil {
			return types.NewBool(false)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		if err != nil {
			return types.NewBool(false)
		}
		return types.NewStringBytes(b)
	}

	// 3. VFS first
	if data, err := sm.vfs.ReadFile(target); err == nil {
		return types.NewStringBytes(data)
	}

	// 4. OS filesystem
	b, err := os.ReadFile(target)
	if err != nil {
		return types.NewBool(false)
	}
	return types.NewStringBytes(b)
}

func (sm *StreamManager) FilePutContents(pathVal, dataVal types.Value) types.Value {
	path := pathVal.String()
	data := dataVal.Bytes()

	if strings.HasPrefix(path, "php://memory") || strings.HasPrefix(path, "php://temp") {
		sm.mu.Lock()
		defer sm.mu.Unlock()
		buf := bytes.NewBuffer(data)
		sm.memoryPool[path] = buf
		return types.NewLong(int64(len(data)))
	}

	sm.vfs.RegisterFile(path, data)
	return types.NewLong(int64(len(data)))
}

func (sm *StreamManager) FileExists(pathVal types.Value) types.Value {
	path := pathVal.String()
	if _, err := sm.vfs.ReadFile(path); err == nil {
		return types.NewBool(true)
	}
	if _, err := os.Stat(path); err == nil {
		return types.NewBool(true)
	}
	return types.NewBool(false)
}

func Dirname(pathVal types.Value) types.Value {
	p := pathVal.String()
	return types.NewString(filepath.Dir(p))
}

func Basename(pathVal types.Value) types.Value {
	p := pathVal.String()
	return types.NewString(filepath.Base(p))
}
