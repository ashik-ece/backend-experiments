# Git-style storage: lock the whole write, or only the ref?

Experiment inspired by GitHub's Nov 2026 post on their new Git storage. Their claim, as I read it:
Git objects are immutable and named by their content, so writers never need to agree about them.
Only **ref updates** need agreement. So: write objects with no lock, and move the ref with a
compare-and-swap (CAS).

This is a toy that tests how far that idea gets you, by comparing two designs at 1, 8 and 32 writers.

## The toy

- `store.go` — object store on a local directory.
  - `objects/ab/cdef…` is immutable and named by the sha256 of its bytes. A write stages a file in `tmp/`
    and `rename`s it into place. **No lock.** Two writers storing the same bytes produce the same name and
    the same file, so races are harmless (tested).
  - `refs/main` holds the head commit id. `ReadRef` is lock-free (the file is only replaced by rename).
    `CASRef(old, new)` takes a mutex, checks the ref still equals `old`, and publishes `new`.
- `commit.go` — one commit = blob + tree + commit object + ref move. Three modes:

| mode | what is locked |
|---|---|
| `global-lock` | One mutex around the entire commit: read head, write 3 objects, move ref. |
| `ref-cas` | Objects written lock-free. Only the ref move is under the CAS mutex. On CAS failure: re-read head, rewrite only the commit object (blob and tree are still valid, since they don't name a parent), retry. |
| `ref-cas-tuned` | `ref-cas` plus two standard optimistic-concurrency fixes: fail fast with a lock-free check before queueing on the mutex, and jittered exponential backoff. Added after the first smoke run showed plain `ref-cas` losing in some cases. |

- `verify.go` — after every run: every object's content hashes to its name, `tmp/` is empty, and walking
  parent links from head finds exactly `1 + commits` commits (no lost updates).
- A control run overwrites the ref **blindly** (no CAS). Verify catches it: 8 writers, 400 commits submitted,
  only 50 reachable from head. So the "ok" in the tables is a real check, not a rubber stamp.
- `store_test.go` — idempotent concurrent puts, exactly one CAS winner among 32, no lost updates in all
  three modes, and the blind-overwrite control. Passes under `go test -race`.

## Setup

Apple M5 Pro, 18 logical cores, macOS (APFS), Go 1.27. Every run uses a fresh store; each cell is the
**median of 5 runs**; each commit writes a 4 KB blob. Writers all hammer one ref. Total commits per run
is fixed, so throughput is comparable across writer counts. Latency is measured per commit including time
spent waiting for locks.

Three scenarios, because the answer depends on what a write costs:

- **A. local disk, no fsync** — writes land in the page cache. 4000 commits per run.
- **B. local disk, fsync on every write** — durable writes. 300 commits per run.
- **C. simulated 2 ms storage round trip per write** — a `sleep` before each atomic publish, objects and
  ref alike. This stands in for a networked storage backend. **It is simulated**, not measured against
  anything real. 400 commits per run.

Reproduce: `go test -race ./... && go run . -reps 5` (about 15 minutes). Raw output: `results/results.md`.

## Results (commits/sec, higher is better)

| scenario | writers | global-lock | ref-cas | ref-cas-tuned |
|---|---:|---:|---:|---:|
| A. no fsync | 1 | 1974 | 1963 | 1917 |
| | 8 | **2003** | 1197 | 1851 |
| | 32 | **1968** | 385 | 1536 |
| B. fsync | 1 | 62 | 62 | 61 |
| | 8 | **61** | 31 | 31 |
| | 32 | **62** | 12 | 13 |
| C. 2 ms round trip | 1 | 91 | 91 | 88 |
| | 8 | 84 | **172** | 162 |
| | 32 | 92 | **176** | 173 |

Retries per commit for plain `ref-cas` at 32 writers: about 26 in A (102,729 retries / 4000 commits),
23 in B, 25 in C. Every CAS failure also leaves an orphaned commit object behind.

## What it shows

1. **The idea works, but only when the object writes are the slow, parallelizable part.** In C, where each
   write costs a 2 ms round trip, ref-only locking is about 2x the global lock (172 vs 84 at 8 writers,
   176 vs 92 at 32). It scales from 1 to 8 writers and then flattens. That fits: the global lock serializes
   4 writes per commit, the ref design serializes 1 (plus the ref write sits inside the mutex). The ceiling
   is the ref, as GitHub says, but I measured a 2x gain, not the ~4x the arithmetic would allow, because
   losers redo work (next point).

2. **Where writes are cheap or can't overlap, the global lock wins.** In A, a commit is ~0.5 ms of local
   file operations. There's little to parallelize, so the optimistic design just adds retries: plain
   `ref-cas` falls to 385/s at 32 writers vs 1968 for the global lock. In B, fsync didn't overlap on this
   machine at all (4 fsyncs ≈ 16 ms per commit regardless of writer count; I suspect a full device cache
   flush on macOS but did not verify that), so there was nothing to gain, and `ref-cas` is 5x slower at 32
   writers because losing writers redo an fsync'd commit write every attempt.

3. **The retry storm is the cost of a hot ref.** All writers race for one ref, so with N writers most CAS
   attempts lose. A global lock does zero wasted work; the CAS design burns I/O on attempts that were
   doomed. Backoff plus fail-fast (`ref-cas-tuned`) recovers most of the loss in A (1536 vs 385 at 32)
   but not in B, and it doesn't beat the global lock anywhere except tie-ish in C.

4. **Throughput hides fairness.** `ref-cas-tuned` has a tiny p50 (about 0.5 ms in A) but p99 of 518 ms in A
   and up to 12 s in B: a few writers get starved while lucky ones win repeatedly. The global lock's tail is
   much tighter in A and B (p99 49 ms and 532 ms at 32 writers). Don't read the tuned p50 as "faster".

5. **At 1 writer all modes tie**, as expected: no contention, same work.

## Caveats

- The CAS here is an in-process mutex around a file write, not a distributed consensus operation. A real
  ref update across machines would likely cost more than an object write, which would shrink the CAS
  design's advantage in C (the ref step is the serial part). I didn't model that; `RefLatency` is a
  separate knob to try.
- Scenario C's latency is a `sleep`, so it has no bandwidth limits, queueing or jitter. Treat it as
  "what if writes were slow and perfectly parallel", the best case for the CAS design.
- One hot ref is the worst case for optimistic CAS. Real workloads spread across many refs/repos, where
  the conflict rate would be far lower and the CAS design would look better than here.
- One machine, one filesystem, 5 reps per cell. I only kept the median, not the spread, so small
  differences (roughly 10%, e.g. the A rows at 1 writer) shouldn't be trusted; the 2x-5x gaps are large
  enough that I'm confident in the direction.
- sha256 and a flat file layout, not Git's real format or packfiles.

## Takeaway

"Only the ref needs agreement" is true and the lock-free object path is correct and cheap (idempotent
by construction). But shrinking the critical section only pays off when the work moved *out* of it is
expensive and parallel. With cheap or non-overlapping writes, a single global lock is simpler and faster,
and an optimistic ref needs backoff to avoid a retry storm.

Next experiments: vary objects per commit (the parallel/serial ratio), spread writers over many refs,
and give the ref step its own higher latency.
