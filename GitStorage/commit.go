package main

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"
)

type Mode string

const (
	// ModeGlobal: one mutex around the whole commit (read head, write all
	// objects, move the ref). The "just lock everything" baseline.
	ModeGlobal Mode = "global-lock"
	// ModeRef: objects are written with no lock; only the ref move is a CAS.
	// On CAS failure the writer re-reads the head and rewrites only the commit.
	ModeRef Mode = "ref-cas"
	// ModeRefTuned: same as ModeRef plus two standard optimistic-concurrency
	// fixes: fail fast without queueing on the ref mutex when the head has
	// already moved, and back off with jitter before retrying.
	ModeRefTuned Mode = "ref-cas-tuned"
	// ModeBlind: like ModeRef but the ref is overwritten with no check.
	// Incorrect on purpose; used as a control for the verifier.
	ModeBlind Mode = "blind-ref(broken)"
)

const refName = "main"

type Stats struct {
	mu        sync.Mutex
	Latencies []time.Duration
	Retries   int64 // failed CAS attempts
	Orphans   int64 // commit objects written but never became head
}

func (st *Stats) add(d time.Duration, retries, orphans int64) {
	st.mu.Lock()
	st.Latencies = append(st.Latencies, d)
	st.Retries += retries
	st.Orphans += orphans
	st.mu.Unlock()
}

func payload(writer, seq, size int) []byte {
	head := fmt.Sprintf("writer=%d seq=%d\n", writer, seq)
	return []byte(head + strings.Repeat("x", size-len(head)))
}

func treeBytes(blob string) []byte { return []byte("blob " + blob + " data\n") }

func commitBytes(tree, parent string, writer, seq int) []byte {
	return []byte(fmt.Sprintf("tree %s\nparent %s\nauthor w%d\n\ncommit %d\n", tree, parent, writer, seq))
}

// Commit adds one commit on top of the current head using the given mode.
func Commit(s *Store, mode Mode, global *sync.Mutex, writer, seq, size int, st *Stats) error {
	start := time.Now()
	var retries, orphans int64

	if mode == ModeGlobal {
		global.Lock()
		defer global.Unlock()
	}

	parent, err := s.ReadRef(refName)
	if err != nil {
		return err
	}
	blob, err := s.PutObject(payload(writer, seq, size))
	if err != nil {
		return err
	}
	tree, err := s.PutObject(treeBytes(blob))
	if err != nil {
		return err
	}

	for {
		c, err := s.PutObject(commitBytes(tree, parent, writer, seq))
		if err != nil {
			return err
		}
		if mode == ModeBlind {
			if err := s.SetRefBlind(refName, c); err != nil {
				return err
			}
			break
		}
		if mode == ModeRefTuned {
			// fast fail: don't queue on the mutex just to learn we are stale
			if cur, err := s.ReadRef(refName); err != nil {
				return err
			} else if cur != parent {
				retries++
				orphans++
				backoff(int(retries))
				parent = cur
				continue
			}
		}
		ok, err := s.CASRef(refName, parent, c)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		// Lost the race. blob and tree are content-addressed and still valid;
		// only the commit (which names the parent) has to be rebuilt.
		retries++
		orphans++
		if mode == ModeRefTuned {
			backoff(int(retries))
		}
		if parent, err = s.ReadRef(refName); err != nil {
			return err
		}
	}
	st.add(time.Since(start), retries, orphans)
	return nil
}

// backoff sleeps a random time in [0, min(20ms, 200us * 2^n)) ("full jitter").
func backoff(n int) {
	if n > 7 {
		n = 7
	}
	max := 200 * time.Microsecond << uint(n)
	if max > 20*time.Millisecond {
		max = 20 * time.Millisecond
	}
	time.Sleep(time.Duration(rand.Int63n(int64(max))))
}
