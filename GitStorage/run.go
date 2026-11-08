package main

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

type Config struct {
	Mode       Mode
	Writers    int
	Commits    int // total across all writers
	BlobSize   int
	Fsync      bool
	ObjLatency time.Duration
	RefLatency time.Duration
	Dir        string // parent dir for the scratch store
}

type Result struct {
	Cfg        Config
	Elapsed    time.Duration
	Throughput float64 // commits/sec
	P50, P99   time.Duration
	Retries    int64
	Orphans    int64
	ChainLen   int
	VerifyErr  error
}

func RunOnce(cfg Config) (Result, error) {
	dir, err := os.MkdirTemp(cfg.Dir, "store-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	s, err := NewStore(dir)
	if err != nil {
		return Result{}, err
	}
	s.Fsync, s.ObjLatency, s.RefLatency = cfg.Fsync, cfg.ObjLatency, cfg.RefLatency

	// Root commit so every writer has a parent to race on.
	blob, _ := s.PutObject([]byte("root\n"))
	tree, _ := s.PutObject(treeBytes(blob))
	root, _ := s.PutObject(commitBytes(tree, "", -1, 0))
	if err := s.SetRefBlind(refName, root); err != nil {
		return Result{}, err
	}

	st := &Stats{}
	var global sync.Mutex
	errs := make(chan error, cfg.Writers)
	var wg, ready sync.WaitGroup
	start := make(chan struct{})
	ready.Add(cfg.Writers)
	for w := 0; w < cfg.Writers; w++ {
		n := cfg.Commits / cfg.Writers
		if w < cfg.Commits%cfg.Writers {
			n++
		}
		wg.Add(1)
		go func(w, n int) {
			defer wg.Done()
			ready.Done()
			<-start
			for i := 0; i < n; i++ {
				if err := Commit(s, cfg.Mode, &global, w, i, cfg.BlobSize, st); err != nil {
					errs <- err
					return
				}
			}
		}(w, n)
	}
	ready.Wait()
	t0 := time.Now()
	close(start)
	wg.Wait()
	elapsed := time.Since(t0)
	close(errs)
	if err := <-errs; err != nil {
		return Result{}, err
	}

	sort.Slice(st.Latencies, func(i, j int) bool { return st.Latencies[i] < st.Latencies[j] })
	pct := func(p float64) time.Duration { return st.Latencies[int(float64(len(st.Latencies)-1)*p)] }
	chain, verr := Verify(s, cfg.Commits+1) // +1 for the root commit
	return Result{
		Cfg: cfg, Elapsed: elapsed,
		Throughput: float64(cfg.Commits) / elapsed.Seconds(),
		P50:        pct(0.50), P99: pct(0.99),
		Retries: st.Retries, Orphans: st.Orphans,
		ChainLen: chain, VerifyErr: verr,
	}, nil
}

func median(rs []Result) Result {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Throughput < rs[j].Throughput })
	return rs[len(rs)/2]
}

func (r Result) String() string {
	return fmt.Sprintf("%-18s w=%-2d %8.0f commits/s  p50=%-9v p99=%-9v retries=%d",
		r.Cfg.Mode, r.Cfg.Writers, r.Throughput, r.P50.Round(time.Microsecond), r.P99.Round(time.Microsecond), r.Retries)
}
