GOMAXPROCS cores: see README. reps=5 blob=4096B

## A. local disk, no fsync  (4000 commits per run)

| writers | mode | commits/s | p50 | p99 | CAS retries | verify |
|---:|---|---:|---:|---:|---:|---|
| 1 | global-lock | 1974 | 419µs | 811µs | 0 | ok |
| 1 | ref-cas | 1963 | 413µs | 762µs | 0 | ok |
| 1 | ref-cas-tuned | 1917 | 424µs | 803µs | 0 | ok |
| 8 | global-lock | 2003 | 3.635ms | 5.294ms | 0 | ok |
| 8 | ref-cas | 1197 | 4.228ms | 29.113ms | 24890 | ok |
| 8 | ref-cas-tuned | 1851 | 434µs | 2.398ms | 899 | ok |
| 32 | global-lock | 1968 | 14.999ms | 48.759ms | 0 | ok |
| 32 | ref-cas | 385 | 53.407ms | 380.342ms | 102729 | ok |
| 32 | ref-cas-tuned | 1536 | 505µs | 517.904ms | 5547 | ok |

## B. local disk, fsync every write  (300 commits per run)

| writers | mode | commits/s | p50 | p99 | CAS retries | verify |
|---:|---|---:|---:|---:|---:|---|
| 1 | global-lock | 62 | 16.028ms | 18.731ms | 0 | ok |
| 1 | ref-cas | 62 | 16.045ms | 18.938ms | 0 | ok |
| 1 | ref-cas-tuned | 61 | 16.034ms | 18.847ms | 0 | ok |
| 8 | global-lock | 61 | 131.033ms | 151.828ms | 0 | ok |
| 8 | ref-cas | 31 | 208.83ms | 803.922ms | 1859 | ok |
| 8 | ref-cas-tuned | 31 | 106.232ms | 1.906016s | 1742 | ok |
| 32 | global-lock | 62 | 514.765ms | 532.075ms | 0 | ok |
| 32 | ref-cas | 12 | 1.634478s | 9.633246s | 7038 | ok |
| 32 | ref-cas-tuned | 13 | 552.324ms | 12.195145s | 6687 | ok |

## C. simulated 2ms storage round trip per write  (400 commits per run)

| writers | mode | commits/s | p50 | p99 | CAS retries | verify |
|---:|---|---:|---:|---:|---:|---|
| 1 | global-lock | 91 | 10.932ms | 12.057ms | 0 | ok |
| 1 | ref-cas | 91 | 10.92ms | 12.107ms | 0 | ok |
| 1 | ref-cas-tuned | 88 | 11.347ms | 13.21ms | 0 | ok |
| 8 | global-lock | 84 | 95.201ms | 98.232ms | 0 | ok |
| 8 | ref-cas | 172 | 30.221ms | 140.048ms | 2272 | ok |
| 8 | ref-cas-tuned | 162 | 15.981ms | 648.931ms | 1200 | ok |
| 32 | global-lock | 92 | 347.383ms | 368.049ms | 0 | ok |
| 32 | ref-cas | 176 | 77.926ms | 893.596ms | 9837 | ok |
| 32 | ref-cas-tuned | 173 | 14.839ms | 1.678234s | 3588 | ok |

## Control: ref overwritten with no compare-and-swap (should lose updates)

8 writers, 400 commits submitted, 50 reachable from head -> lost updates: chain has 51 commits, expected 401
