package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Verify checks the store after a run:
//  1. every object file's content hashes to its own name (no corruption)
//  2. tmp/ is empty (no half-published writes)
//  3. walking parent links from the head reaches `want` commits (no lost updates)
//
// It returns the chain length found.
func Verify(s *Store, want int) (int, error) {
	err := filepath.Walk(filepath.Join(s.root, "objects"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		id := filepath.Base(filepath.Dir(p)) + filepath.Base(p)
		if hashOf(b) != id {
			return fmt.Errorf("object %s has wrong hash", id)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if ents, _ := os.ReadDir(filepath.Join(s.root, "tmp")); len(ents) != 0 {
		return 0, fmt.Errorf("%d leftover files in tmp/", len(ents))
	}

	head, err := s.ReadRef(refName)
	if err != nil {
		return 0, err
	}
	n := 0
	for id := head; id != ""; n++ {
		b, err := s.GetObject(id)
		if err != nil {
			return n, fmt.Errorf("chain broken at %s: %v", id, err)
		}
		id = ""
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "parent ") {
				id = strings.TrimPrefix(line, "parent ")
			}
		}
	}
	if n != want {
		return n, fmt.Errorf("lost updates: chain has %d commits, expected %d", n, want)
	}
	return n, nil
}
