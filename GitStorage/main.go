package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

type Scenario struct {
	Name       string
	Commits    int
	Fsync      bool
	ObjLatency time.Duration
	RefLatency time.Duration
}

func main() {
	reps := flag.Int("reps", 5, "repetitions per cell (median reported)")
	size := flag.Int("blob", 4096, "blob size in bytes")
	dir := flag.String("dir", "", "scratch dir for stores (default: system temp)")
	quick := flag.Bool("quick", false, "small smoke run")
	flag.Parse()

	scenarios := []Scenario{
		{"A. local disk, no fsync", 4000, false, 0, 0},
		{"B. local disk, fsync every write", 300, true, 0, 0},
		{"C. simulated 2ms storage round trip per write", 400, false, 2 * time.Millisecond, 2 * time.Millisecond},
	}
	if *quick {
		*reps = 1
		for i := range scenarios {
			scenarios[i].Commits /= 10
		}
	}

	failed := false
	fmt.Printf("GOMAXPROCS cores: see README. reps=%d blob=%dB\n", *reps, *size)
	for _, sc := range scenarios {
		fmt.Printf("\n## %s  (%d commits per run)\n\n", sc.Name, sc.Commits)
		fmt.Println("| writers | mode | commits/s | p50 | p99 | CAS retries | verify |")
		fmt.Println("|---:|---|---:|---:|---:|---:|---|")
		for _, w := range []int{1, 8, 32} {
			for _, m := range []Mode{ModeGlobal, ModeRef, ModeRefTuned} {
				cfg := Config{m, w, sc.Commits, *size, sc.Fsync, sc.ObjLatency, sc.RefLatency, *dir}
				var rs []Result
				for i := 0; i < *reps; i++ {
					r, err := RunOnce(cfg)
					if err != nil {
						fmt.Fprintln(os.Stderr, "run error:", err)
						os.Exit(1)
					}
					if r.VerifyErr != nil {
						failed = true
					}
					rs = append(rs, r)
				}
				r := median(rs)
				v := "ok"
				if r.VerifyErr != nil {
					v = "FAIL: " + r.VerifyErr.Error()
				}
				fmt.Printf("| %d | %s | %.0f | %v | %v | %d | %s |\n", w, m, r.Throughput,
					r.P50.Round(time.Microsecond), r.P99.Round(time.Microsecond), r.Retries, v)
			}
		}
	}

	// Control: a blind ref overwrite must lose updates, proving the verifier can see them.
	fmt.Println("\n## Control: ref overwritten with no compare-and-swap (should lose updates)")
	r, err := RunOnce(Config{ModeBlind, 8, 400, *size, false, 2 * time.Millisecond, 2 * time.Millisecond, *dir})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("\n8 writers, 400 commits submitted, %d reachable from head -> %v\n", r.ChainLen-1, r.VerifyErr)
	if r.VerifyErr == nil {
		fmt.Println("UNEXPECTED: control did not lose updates")
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}
