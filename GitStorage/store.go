package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Store is a toy Git-like object store on a local directory.
//
//	objects/ab/cdef...   immutable, named by sha256 of their content
//	refs/main            a single mutable pointer: the id of the head commit
//	tmp/                 staging area (same filesystem, so rename is atomic)
//
// Objects need no coordination: two writers storing the same bytes produce the
// same name, and the name never changes meaning. Only the ref is contended.
type Store struct {
	root string

	// Fsync makes every object and ref write durable before it is published.
	Fsync bool
	// ObjLatency / RefLatency simulate a remote storage round trip per write
	// (applied just before the atomic publish). Zero means local disk only.
	ObjLatency time.Duration
	RefLatency time.Duration

	refMu  sync.Mutex // guards ref compare-and-swap: the only lock on the ref path
	tmpSeq atomic.Uint64

	ObjWrites atomic.Int64 // objects actually written
	ObjDedup  atomic.Int64 // puts that found the object already present
}

func NewStore(root string) (*Store, error) {
	for _, d := range []string{"objects", "refs", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{root: root}, nil
}

func (s *Store) objPath(id string) string {
	return filepath.Join(s.root, "objects", id[:2], id[2:])
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeAtomic stages data in tmp/ and renames it into place. Readers never see
// a partial file. Safe to call concurrently for the same destination: rename
// replaces atomically, and for objects both writers carry identical bytes.
func (s *Store) writeAtomic(dst string, data []byte, latency time.Duration) error {
	tmp := filepath.Join(s.root, "tmp", fmt.Sprintf("%d-%d", os.Getpid(), s.tmpSeq.Add(1)))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if s.Fsync {
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if latency > 0 {
		time.Sleep(latency)
	}
	return os.Rename(tmp, dst)
}

// PutObject stores data under its content hash. No lock is taken.
func (s *Store) PutObject(data []byte) (string, error) {
	id := hashOf(data)
	dst := s.objPath(id)
	if _, err := os.Stat(dst); err == nil {
		s.ObjDedup.Add(1)
		return id, nil // already present; immutable, so nothing to do
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := s.writeAtomic(dst, data, s.ObjLatency); err != nil {
		return "", err
	}
	s.ObjWrites.Add(1)
	return id, nil
}

func (s *Store) GetObject(id string) ([]byte, error) {
	return os.ReadFile(s.objPath(id))
}

func (s *Store) refPath(name string) string { return filepath.Join(s.root, "refs", name) }

// ReadRef returns "" if the ref does not exist yet. Lock-free: the file is only
// ever replaced by rename, so a read sees the old or the new value, never a mix.
func (s *Store) ReadRef(name string) (string, error) {
	b, err := os.ReadFile(s.refPath(name))
	if os.IsNotExist(err) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

// CASRef moves the ref from old to new only if it still points at old.
// Returns false (and changes nothing) if somebody else moved it first.
func (s *Store) CASRef(name, old, new string) (bool, error) {
	s.refMu.Lock()
	defer s.refMu.Unlock()
	cur, err := s.ReadRef(name)
	if err != nil {
		return false, err
	}
	if cur != old {
		return false, nil
	}
	return true, s.writeAtomic(s.refPath(name), []byte(new+"\n"), s.RefLatency)
}

// SetRefBlind overwrites the ref with no comparison. A deliberately broken
// control: it exists to show what the CAS is protecting against.
func (s *Store) SetRefBlind(name, new string) error {
	return s.writeAtomic(s.refPath(name), []byte(new+"\n"), s.RefLatency)
}
