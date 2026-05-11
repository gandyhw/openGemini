# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
# Build all binaries (output in ./build/)
python build.py --clean

# Build for specific platform
python build.py --platform linux --arch amd64

# Run all unit tests
make gotest

# Run a single package's tests
go test -count=1 -gcflags "all=-N -l" -v ./engine/executor/...

# Run a single test
go test -count=1 -gcflags "all=-N -l" -v -run TestEngine_ExpiredShards ./engine/

# Integration tests (requires a running openGemini at 127.0.0.1:8086)
make integration-test

# Code generation (protobuf, tmpl, msgp, yacc)
make go-generate

# Lint checks
make style-check        # import ordering via goimports-reviser
make golangci-lint-check
make static-check       # staticcheck
```

Tests use the `-gcflags "all=-N -l"` flag to support failpoint injection (`github.com/pingcap/failpoint`). Failpoints must be enabled (`make failpoint-enable`) before running tests and disabled afterwards. The `make gotest` target handles this automatically.

## Architecture

openGemini is a distributed time series database (Go) compatible with InfluxDB v1.x Line Protocol, InfluxQL, and read/write APIs. It uses an MPP (Massively Parallel Processing) architecture.

### Binaries (`app/`)

| Binary | Purpose |
|--------|---------|
| `ts-meta` | Metadata service — schema, shard topology, Raft-based consensus |
| `ts-sql` | Query frontend — parses InfluxQL/PromQL, generates query plans, coordinates execution across ts-store nodes |
| `ts-store` | Storage node — manages shards, storage engine instances, WAL, Raft replication |
| `ts-server` | Combined standalone binary (meta + sql + store in one process) |
| `ts-monitor` | Monitoring daemon |
| `ts-data` | Data import/export tool |
| `ts-recover` | Recovery tool |

### Core packages

- **`coordinator/`** — Distributed coordination layer. Routes write/read requests across shards and ts-store nodes, handles DDL statement execution with metadata interaction, manages subscribers for continuous queries and downsample.

- **`engine/`** — Storage engine (LSM-based with columnar compression). Key sub-packages:
  - `engine/immutable/` — Persistent TSM files (read-only compressed columnar data)
  - `engine/mutable/` — In-memory write buffer (WAL → memtable → flush to immutable)
  - `engine/index/` — Inverted indexes: `tsi/` (time series index), `ski/` (series key index), `fhi/` (high cardinality), `mergeindex/`, `bloomfilter/`, `clv/`, `textindex/`
  - `engine/executor/` — Query execution engine (volcano-style pipeline). Transforms include aggregate, filter, topn, graph (for RCA), sort, merge, arrow-flight, Prom functions. Also contains RCA/topo event processing (`rca.go`, `graph_transform.go`).
  - `engine/hybridqp/` — Hybrid query plan definitions bridging SQL and storage
  - `engine/op/` — Logical relational operators (aggregate, project, filter)

- **`lib/`** — Shared libraries. Notable packages:
  - `lib/config/` — Configuration parsing (openGemini.conf)
  - `lib/netstorage/` — Storage engine interface consumed by ts-sql to talk to ts-store
  - `lib/metaclient/` — Metadata client for ts-sql ↔ ts-meta communication
  - `lib/errno/` — Centralized error codes (by module: network 1xxx, storage 2xxx, etc.)
  - `lib/record/` — Columnar record format used throughout the query engine
  - `lib/spdy/` — Custom RPC transport protocol for inter-node communication
  - `lib/raftlog/` / `lib/raftconn/` — Raft RPC wrappers
  - `lib/codec/` / `lib/compress/` — Encoding and compression
  - `lib/tracing/` — Distributed tracing
  - `lib/util/lifted/` — Vendored/lifted code from InfluxDB, VictoriaMetrics, Prometheus, HashiCorp, etc. Heavy import base for compatibility.

- **`services/`** — Background services: `continuousquery/`, `downsample/`, `hierarchical/` (hot/warm/cold data tiering), `retention/`, `stream/`, `series/`, `shardMerge/`, `castor/`, `sherlock/` (anomaly detection).

### Data path

**Write path**: Client (Line Protocol) → ts-sql → coordinator (shard mapping) → ts-store → WAL → in-memory memtable → flush to immutable TSM files (columnar, compressed).

**Read path**: Client (InfluxQL/PromQL) → ts-sql (parse → logical plan → optimize) → ts-store executor (scan TSM files, filter, aggregate) → results streamed back via SPDY.

### Error handling conventions

Error codes are defined in `lib/errno/code.go` organized by module range (network 1xxx, meta 2xxx, engine 3xxx, SQL 4xxx, etc.). Use `errno.NewError(errno.SomeCode, ...args)` for structured errors. Test assertions on error codes use `errno.Equal()`.

### Test conventions

- Unit tests live alongside code (`*_test.go`). Use failpoints (`failpoint.Inject`/`failpoint.Return`) for fault injection — these require `-gcflags "all=-N -l"`.
- Integration/functional tests in `tests/` use a test server suite pattern (`server_suite.go`) — tests send HTTP requests against a running openGemini instance.
- Test coverage outputs: `coverage_*.txt` files generated per package by `make gotest`.
