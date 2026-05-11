# CLAUDE.md

openGemini is a CNCF sandbox cloud-native distributed time-series database written in Go (1.24+). Compatible with InfluxDB v1.x Line Protocol, InfluxQL, and read/write APIs. MPP architecture.

## Critical Facts

- **NEVER edit files under `lib/util/lifted/`** — vendored/forked upstream code with separate licensing.
- **NEVER hand-edit generated files** (`.gen.go`, `.pb.go`, yacc output). Regenerate via `make go-generate`.
- Every `.go` file (outside `lifted/`) must carry an Apache 2.0 copyright header. `make license-check` validates.
- Tests use **failpoint injection** (`github.com/pingcap/failpoint`). `-gcflags "all=-N -l"` is required. `make gotest` handles failpoint enable/disable automatically.

## Architecture

```
ts-sql ──────► coordinator ──────► ts-store (engine/)
   │                │                    │
   │                ▼                    ▼
   └──────► ts-meta (Raft)          engine/immutable (TSM)
                                     engine/index (indexes)
                                     engine/executor (query)
                                     engine/mutable (WAL/memtable)
```

| Binary | Role |
|--------|------|
| `ts-meta` | Metadata — schema, shard topology, Raft consensus |
| `ts-sql` | Query frontend — parses InfluxQL/PromQL, plans, coordinates execution |
| `ts-store` | Storage — shards, WAL, Raft replication |
| `ts-server` | Standalone (meta + sql + store in one process) |

### Key directories

- **`coordinator/`** — Distributed coordination: write/read routing, DDL execution, subscribers
- **`engine/`** — LSM-based storage engine with columnar compression. `immutable/` (TSM files), `mutable/` (WAL → memtable), `index/` (inverted indexes: tsi, ski, fhi, mergeindex, bloomfilter, clv, textindex), `executor/` (volcano-style query pipeline with graph/RCA transforms)
- **`lib/`** — Shared libs: `config/`, `netstorage/`, `metaclient/`, `errno/` (error codes), `record/` (columnar format), `spdy/` (RPC transport), `raftlog/`, `raftconn/`, `codec/`, `compress/`, `tracing/`
- **`services/`** — Background tasks: continuousquery, downsample, hierarchical, retention, stream, etc.

### Data path

**Write**: Line Protocol → ts-sql → coordinator (shard mapping) → ts-store → WAL → memtable → flush to TSM.

**Read**: InfluxQL/PromQL → ts-sql (parse → plan → optimize) → ts-store executor (scan, filter, aggregate) → streamed via SPDY.

## Build & Test

```bash
python build.py --clean                              # build all → ./build/
make gotest                                          # all unit tests
go test -v -count=1 -gcflags "all=-N -l" ./engine/executor/...  # single package
go test -v -count=1 -gcflags "all=-N -l" -run TestFoo ./engine/ # single test
make integration-test                                # needs instance at 127.0.0.1:8086
```

## Lint

```bash
make style-check        # goimports-reviser (import ordering)
make static-check       # staticcheck
make go-vet-check       # go vet
make license-check      # copyright headers
```

Import ordering is enforced by `goimports-reviser` — let the tool handle it, don't manually sort. Configuration is TOML-based (`config/openGemini.conf`), well-commented — read the file directly for options.

## Commit Conventions

Conventional Commits enforced by commitlint. Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`. Sign with DCO: `git commit -s -m "feat(engine): description"`. Header 10–100 chars.

## Conventions

- **Errors**: defined in `lib/errno/code.go` by module range (network 1xxx, meta 2xxx, engine 3xxx, SQL 4xxx…). Use `errno.NewError(errno.SomeCode, ...args)`. Assert with `errno.Equal()`.
- **Tests**: unit tests alongside code (`*_test.go`). Integration tests in `tests/` use server suite pattern (`server_suite.go`).
- **Security**: don't commit credentials, secrets, or generated runtime data. Use sample configs in `config/`.

## Common Failure Modes

- **Concurrency bugs** — `sync.Mutex`/`sync.RWMutex` usage is pervasive (~288 sites in core packages). Past bugs: concurrent map writes in metrics, unprotected graph mutations, raft logger panics. Any new shared state needs explicit synchronization review.
- **Index staleness** — Engine indexes (graph edge, series, bloom, mergeindex, text) must be rebuilt/refreshed when their backing data changes. Stale indexes produce wrong query results, not errors.
- **Raft log panics** — Out-of-range access in raft log code is a recurring issue (`lib/raftlog/`, `lib/raftconn/`). Validate index bounds before any raft log slice/index operation.
- **`-gcflags "all=-N -l"` omission** — Tests using failpoints will silently pass (failpoints become no-ops) without this flag, producing false-negatives.
- **Parsing ambiguity** — Schema/measurement parsing is fragile. Changes to the parsing path (`lib/util/lifted/influx/influxql/`) should not be done locally — extend via coordinator/engine layers instead.

## High Risk Areas

- **`engine/index/`** — 9 sub-packages, 88+ files, multiple index types with interleaved concurrency. Recent mutex and staleness fixes concentrated here.
- **`engine/executor/`** — Volcano-style pipeline with graph/RCA transforms. Complex DAG-shaped operator graphs; incorrect edge propagation or transform ordering silently corrupts query results.
- **Raft layer (`lib/raftlog/`, `lib/raftconn/`)** — Recurring panics, out-of-range bugs. Data durability path; any defect here risks data loss or split-brain.
- **WAL & replication** — Write path correctness is critical. Panics during WAL write or replication cause data loss. Recent panic fixes in replication paths.
- **Memory management** — Multiple fixes for low-kernel compatibility and memory monitoring. Kernel version-specific behavior; test on target kernel.
- **`coordinator/`** — Central routing point for all reads/writes. Failures cascade to all nodes. DDL execution must be atomic across shards.
