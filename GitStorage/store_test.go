package main

import (
	"sync"
	"testing"
	"time"
)

func TestPutObjectIsIdempotentUnderRace(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	data := []byte("same bytes from every writer")
	ids := make([]string, 64)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, err := s.PutObject(data)
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id != hashOf(data) {
			t.Fatalf("writers disagreed on the id: %s", id)
		}
	}
	got, err := s.GetObject(ids[0])
	if err != nil || string(got) != string(data) {
		t.Fatalf("object unreadable or changed: %q %v", got, err)
	}
}

func TestCASRefOnlyOneWinner(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	s.SetRefBlind(refName, "base")
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := s.CASRef(refName, "base", string(rune('a'+i)))
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("expected exactly 1 CAS winner, got %d", wins)
	}
}

func TestNoLostUpdates(t *testing.T) {
	for _, m := range []Mode{ModeGlobal, ModeRef, ModeRefTuned} {
		t.Run(string(m), func(t *testing.T) {
			r, err := RunOnce(Config{Mode: m, Writers: 16, Commits: 320, BlobSize: 256,
				ObjLatency: 200 * time.Microsecond, RefLatency: 200 * time.Microsecond, Dir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if r.VerifyErr != nil {
				t.Fatal(r.VerifyErr)
			}
		})
	}
}

// The control: proves Verify can actually detect a lost update.
func TestBlindRefOverwriteLosesUpdates(t *testing.T) {
	r, err := RunOnce(Config{Mode: ModeBlind, Writers: 8, Commits: 200, BlobSize: 256,
		ObjLatency: time.Millisecond, RefLatency: time.Millisecond, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if r.VerifyErr == nil {
		t.Fatal("blind overwrite should have lost updates, but verifier found none")
	}
}
