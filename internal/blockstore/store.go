// Package blockstore keeps 1 MiB video blocks on disk.
//
// Pinned blocks are never evicted. They hold what a media probe reads, so later probes
// need no Telegram call. Other blocks form a size-limited LRU.
package blockstore

import (
	"container/list"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type key struct {
	doc   int64
	block int64
}

type entry struct {
	key    key
	size   int64
	pinned bool
	elem   *list.Element // nil when pinned
}

// Store is safe for concurrent use. A single mutex also serialises file access, because
// Windows refuses to rename or delete a file another goroutine has open.
type Store struct {
	mu       sync.Mutex
	dir      string
	maxBytes int64
	used     int64
	index    map[key]*entry
	lru      *list.List // front is most recently used
}

// Open loads the existing blocks below dir.
func Open(dir string, maxBytes int64) (*Store, error) {
	s := &Store{dir: dir, maxBytes: maxBytes, index: map[key]*entry{}, lru: list.New()}
	for _, kind := range []string{"pin", "lru"} {
		if err := os.MkdirAll(filepath.Join(dir, kind), 0o700); err != nil {
			return nil, err
		}
	}
	if err := s.load("pin", true); err != nil {
		return nil, err
	}
	if err := s.load("lru", false); err != nil {
		return nil, err
	}
	s.evict()
	return s, nil
}

// Has reports whether a block is stored, without reading it.
func (s *Store) Has(doc, block int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.index[key{doc, block}]
	return ok
}

// IsPinned reports whether a block is stored and protected from eviction.
func (s *Store) IsPinned(doc, block int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.index[key{doc, block}]
	return ok && e.pinned
}

// Get returns a stored block.
func (s *Store) Get(doc, block int64) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.index[key{doc, block}]
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(s.path(e.key, e.pinned))
	if err != nil {
		s.forget(e)
		return nil, false
	}
	if e.elem != nil {
		s.lru.MoveToFront(e.elem)
	}
	return data, true
}

// Put stores a block in the evictable area, unless it is already stored.
func (s *Store) Put(doc, block int64, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{doc, block}
	if _, ok := s.index[k]; ok {
		return nil
	}
	final := s.path(k, false)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), "*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	e := &entry{key: k, size: int64(len(data))}
	e.elem = s.lru.PushFront(e)
	s.index[k] = e
	s.used += e.size
	s.evict()
	return nil
}

// Pin protects a stored block from eviction. A missing block is ignored.
func (s *Store) Pin(doc, block int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.index[key{doc, block}]
	if !ok || e.pinned {
		return nil
	}
	final := s.path(e.key, true)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return err
	}
	if err := os.Rename(s.path(e.key, false), final); err != nil {
		return err
	}
	s.lru.Remove(e.elem)
	e.elem = nil
	e.pinned = true
	s.used -= e.size
	return nil
}

// RemoveDoc deletes every block of a document.
func (s *Store) RemoveDoc(doc int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.index {
		if k.doc == doc {
			s.forget(e)
		}
	}
	name := strconv.FormatInt(doc, 10)
	return errors.Join(
		os.RemoveAll(filepath.Join(s.dir, "pin", name)),
		os.RemoveAll(filepath.Join(s.dir, "lru", name)),
	)
}

// Used returns the bytes held by evictable blocks.
func (s *Store) Used() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

func (s *Store) evict() {
	for s.used > s.maxBytes {
		back := s.lru.Back()
		if back == nil {
			return
		}
		e := back.Value.(*entry)
		_ = os.Remove(s.path(e.key, false))
		s.forget(e)
	}
}

func (s *Store) forget(e *entry) {
	if e.elem != nil {
		s.lru.Remove(e.elem)
		s.used -= e.size
	}
	delete(s.index, e.key)
}

func (s *Store) path(k key, pinned bool) string {
	kind := "lru"
	if pinned {
		kind = "pin"
	}
	return filepath.Join(s.dir, kind, strconv.FormatInt(k.doc, 10), strconv.FormatInt(k.block, 10))
}

func (s *Store) load(kind string, pinned bool) error {
	root := filepath.Join(s.dir, kind)
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".tmp") {
			return os.Remove(p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 2 {
			return fmt.Errorf("unexpected cache file %s", p)
		}
		doc, err1 := strconv.ParseInt(parts[0], 10, 64)
		block, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("unexpected cache file %s", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := &entry{key: key{doc, block}, size: info.Size(), pinned: pinned}
		if _, dup := s.index[e.key]; dup {
			return os.Remove(p)
		}
		if !pinned {
			e.elem = s.lru.PushBack(e)
			s.used += e.size
		}
		s.index[e.key] = e
		return nil
	})
}
