//go:build js

package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hack-pad/go-indexeddb/idb"
	idbfs "github.com/hack-pad/hackpadfs/indexeddb"
	"github.com/hack-pad/hackpadfs"
)

// Store is implemented for js/wasm builds using an IndexedDB-backed
// filesystem (via hackpadfs). The interface matches storage_default.go.
type Store interface {
	Load(name string) ([]byte, error)
	Save(name string, data []byte) error
	List() ([]string, error)
}

var (
	defaultStore Store
)

// Default returns the browser-backed Store using an IndexedDB filesystem.
func Default() Store {
	if defaultStore != nil {
		println("storage_js.Default: reusing existing store")
		return defaultStore
	}
	println("storage_js.Default: creating new IndexedDB-backed store")
	fs, err := newIndexedDBFS()
	if err != nil {
		println("storage_js.Default: newIndexedDBFS failed:", err.Error())
		// In the worst case, fall back to an in-memory implementation so
		// the UI keeps working, even if persistence is lost.
		defaultStore = &memStore{files: map[string][]byte{}}
		return defaultStore
	}
	println("storage_js.Default: newIndexedDBFS succeeded")
	defaultStore = &wasmStore{fs: fs}
	return defaultStore
}

// newIndexedDBFS creates (or opens) an IndexedDB-backed filesystem.
func newIndexedDBFS() (*idbfs.FS, error) {
	println("storage_js.newIndexedDBFS: opening IndexedDB FS")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fs, err := idbfs.NewFS(ctx, "cocoon-winds", idbfs.Options{
		Factory:               idb.Global(),
		TransactionDurability: idb.DurabilityRelaxed,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: NewFS: %w", err)
	}
	println("storage_js.newIndexedDBFS: NewFS ok, ensuring winds dir")
	// Ensure root and winds dir exist.
	if err := fs.MkdirAll("winds", 0o755); err != nil {
		return nil, fmt.Errorf("storage: MkdirAll winds: %w", err)
	}
	println("storage_js.newIndexedDBFS: MkdirAll winds ok")
	return fs, nil
}

// wasmStore implements Store on top of a hackpadfs filesystem.
type wasmStore struct {
	fs *idbfs.FS
}

func (s *wasmStore) fullPath(name string) string {
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name = name + ".json"
	}
	return "winds/" + name
}

func (s *wasmStore) Load(name string) ([]byte, error) {
	fp := s.fullPath(name)
	println("storage_js.Load: opening", fp)
	f, err := s.fs.Open(fp)
	if err != nil {
		println("storage_js.Load: open error:", err.Error())
		return nil, fmt.Errorf("storage: open %q: %w", fp, err)
	}
	defer f.Close()

	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, rerr := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			// For hackpadfs, EOF is reported via the standard io.EOF,
			// which we treat as a normal end-of-file.
			break
		}
	}
	return buf, nil
}

func (s *wasmStore) Save(name string, data []byte) error {
	fp := s.fullPath(name)
	println("storage_js.Save: saving", fp, "len", len(data))
	// Ensure parent dir exists.
	if err := s.fs.MkdirAll("winds", 0o755); err != nil {
		println("storage_js.Save: MkdirAll error:", err.Error())
		return fmt.Errorf("storage: MkdirAll winds: %w", err)
	}
	f, err := s.fs.OpenFile(fp, hackpadfs.FlagCreate|hackpadfs.FlagTruncate|hackpadfs.FlagWriteOnly, 0o644)
	if err != nil {
		println("storage_js.Save: openFile error:", err.Error())
		return fmt.Errorf("storage: openfile %q: %w", fp, err)
	}
	defer f.Close()

	n, err := hackpadfs.WriteFile(f, data)
	if err != nil {
		println("storage_js.Save: write error:", err.Error())
		return fmt.Errorf("storage: write %q: %w", fp, err)
	}
	if n < len(data) {
		println("storage_js.Save: short write: wrote", n, "of", len(data))
		return fmt.Errorf("storage: short write %q: wrote %d of %d bytes", fp, n, len(data))
	}
	println("storage_js.Save: success for", fp)
	return nil
}

func (s *wasmStore) List() ([]string, error) {
	// Use hackpadfs.ReadDir-style helper over the FS.
	println("storage_js.List: listing winds/")
	entries, err := hackpadfs.ReadDir(s.fs, "winds")
	if err != nil {
		println("storage_js.List: readdir error:", err.Error())
		return nil, fmt.Errorf("storage: readdir winds: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".json") {
			out = append(out, name)
		}
	}
	return out, nil
}

// memStore is a simple in-memory fallback used only if IndexedDB fails.
type memStore struct {
	files map[string][]byte
}

func (m *memStore) Load(name string) ([]byte, error) {
	data, ok := m.files[name]
	if !ok {
		return nil, fmt.Errorf("storage: mem: not found: %s", name)
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

func (m *memStore) Save(name string, data []byte) error {
	cp := make([]byte, len(data))
	copy(cp, data)
	m.files[name] = cp
	return nil
}

func (m *memStore) List() ([]string, error) {
	out := make([]string, 0, len(m.files))
	for name := range m.files {
		out = append(out, name)
	}
	return out, nil
}

