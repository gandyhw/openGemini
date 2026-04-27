# Topo RCA Joint Query Development Stories

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Break the approved topo RCA joint query design into independently deliverable development stories for the core-entity-driven RCA scenario.

**Architecture:** Keep the current `GRAPH CTE -> uid IN -> metric/event scan -> RCA` execution path for P0, but make it diagnosable, typed where RCA consumes event data, and cheaper by removing duplicate RCA graph indexing. P1 adds topology-aware query expressiveness and UID filter pushdown. P2 introduces a first-class RCA logical workflow after the current path is stable.

**Tech Stack:** Go, openGemini executor/planner, InfluxQL parser, existing `errno` package, existing statistics and tracing patterns, Go unit tests and benchmarks.

---

## Story Map

### P0: Production Diagnosis and Stability

P0 stories are the first implementation slice. Each story should pass independently and can be merged without requiring P1 or P2.

| Story | Outcome | Primary Files | Dependencies |
| --- | --- | --- | --- |
| P0-S1 | Topo/RCA errors are classified and testable | `lib/errno`, `engine/executor/graph_transform.go`, `engine/executor/graph.go`, `engine/executor/in_transform.go`, `engine/executor/rca.go` | None |
| P0-S2 | Topo/RCA query metrics are emitted | `engine/executor/topo_stats.go`, graph/in/RCA transforms, statistics tests | P0-S1 optional |
| P0-S3 | RCA event annotations are decoded once into typed records | `engine/executor/rca_event.go`, `engine/executor/rca.go`, RCA tests | None |
| P0-S4 | RCA reuses graph adjacency indexes | `engine/executor/graph.go`, `engine/executor/rca.go`, graph/RCA tests | None |
| P0-S5 | RCA query contract is documented and guarded by validation tests | docs, graph transform tests, RCA tests | P0-S1, P0-S3 |
| P0-S6 | Baseline performance benchmarks exist | `engine/executor/graph_test.go`, `engine/executor/rca_test.go`, benchmark docs | P0-S3, P0-S4 |

### P1: Query Expressiveness and Scan Efficiency

| Story | Outcome | Primary Files | Dependencies |
| --- | --- | --- | --- |
| P1-S1 | Graph traversal supports explicit direction | `engine/executor/graph.go`, parser AST, graph tests | P0-S4 |
| P1-S2 | Graph CTE exposes RCA-relevant fields | graph schema/rows, graph transform tests | P1-S1 |
| P1-S3 | Graph-backed UID filters are marked in planner/executor | `engine/executor/in_transform.go`, logical plan tests | P0-S1 |
| P1-S4 | UID filter pushdown reaches storage/index scan path | planner/index scan/storage tests | P1-S3 |
| P1-S5 | RCA time windows are explicit and configurable | RCA params/types/tests | P0-S3 |

### P2: First-Class RCA Workflow

| Story | Outcome | Primary Files | Dependencies |
| --- | --- | --- | --- |
| P2-S1 | RCA logical node skeleton represents topology, events, metrics, and evidence | logical plan, codec, planner tests | P1-S2, P1-S3 |
| P2-S2 | RCA evidence model is returned with graph output | executor RCA result model, receiver/row conversion tests | P2-S1 |
| P2-S3 | Strategy selector chooses topology-first, event-first, metric-first, or batched execution | planner cost module/tests | P2-S1 |
| P2-S4 | Topo manager predicate pushdown adapter supports future API capabilities | `lib/util/graph_client.go`, graph transform tests | P1-S1 |

---

## File Ownership Plan

- `lib/errno/code.go` and `lib/errno/message.go`: define query-engine errno codes and messages for topo/RCA classified failures.
- `engine/executor/topo_stats.go`: new focused helper for topo/RCA counters and timings.
- `engine/executor/graph.go`: graph traversal, adjacency index accessors, UID set behavior, direction support.
- `engine/executor/graph_transform.go`: topo fetch, graph parse, graph limit, traversal instrumentation and classified errors.
- `engine/executor/in_transform.go`: graph-backed UID set creation, UID set rejection metrics, graph UID filter marker.
- `engine/executor/rca_event.go`: new typed RCA event annotation decoder and validation helpers.
- `engine/executor/rca.go`: consume typed event records and graph index accessors; keep fault demarcation behavior unchanged in P0.
- `engine/executor/*_test.go`: focused unit tests and benchmarks near existing executor tests.
- `docs/topo_joint_query_optimization_plan.md`: update only when implementation behavior changes.

---

## P0 Stories

### Story P0-S1: Classify Topo and RCA Failures

**User Story:** As an RCA operator, I need topo/RCA failures to tell me whether topology, graph filtering, UID filtering, event decoding, or RCA execution failed, so I can diagnose failed root-cause queries quickly.

**Files:**
- Modify: `lib/errno/code.go`
- Modify: `lib/errno/message.go`
- Modify: `engine/executor/graph_transform.go`
- Modify: `engine/executor/graph.go`
- Modify: `engine/executor/in_transform.go`
- Modify: `engine/executor/rca.go`
- Test: `lib/errno/error_test.go`
- Test: `engine/executor/graph_transform_test.go`
- Test: `engine/executor/graph_test.go`
- Test: `engine/executor/rca_test.go`

**Acceptance Criteria:**
- Topo manager unavailable returns a topo-fetch classified error.
- Invalid topo response returns a topo-graph-parse classified error.
- Start node missing returns a topo-start-node-not-found classified error.
- Graph node/edge limit rejection returns a topo-limit classified error.
- UID set limit rejection returns a topo-uid-set-limit classified error.
- Invalid RCA event schema returns an RCA-event-schema classified error.
- Existing `FilterAllPoints` behavior for empty UID set remains unchanged.

**Implementation Steps:**
- [ ] Add query-engine errno codes for topo fetch failure, topo parse failure, topo start node not found, topo graph limit exceeded, topo UID set limit exceeded, RCA event schema invalid, and RCA execution failure.
- [ ] Add messages in `lib/errno/message.go` with enough context fields to include the failed limit, UID, or phase.
- [ ] Update `GraphTransform.Work` to wrap topo fetch, graph creation, graph limit, and traversal failures with the new errors.
- [ ] Update `Graph.MultiHopFilter` and `Graph.UIDSet` to return classified errors rather than raw `fmt.Errorf` for planned failure modes.
- [ ] Update RCA event parsing call sites to return RCA classified errors for invalid schema.
- [ ] Run `go test ./lib/errno ./engine/executor -run 'TestGraphTransform|TestGraph|TestRCA|TestError'`.

**Suggested Commit:** `feat: classify topo RCA query errors`

---

### Story P0-S2: Add Topo/RCA Query Metrics

**User Story:** As an SRE, I need per-query topo/RCA timings and sizes, so I can identify whether latency comes from topo manager, JSON parsing, traversal, UID filtering, outer scan, or RCA algorithm execution.

**Files:**
- Create: `engine/executor/topo_stats.go`
- Create: `engine/executor/topo_stats_test.go`
- Modify: `engine/executor/graph_transform.go`
- Modify: `engine/executor/in_transform.go`
- Modify: `engine/executor/rca.go`

**Acceptance Criteria:**
- Metrics helper can record count, duration, and size values without data races.
- `GraphTransform.Work` records topo fetch duration, response bytes, graph parse duration, full graph size, traversal duration, and subgraph size.
- `InTransform.AddGraphChunkToBufColumn` records UID set size and UID set rejection.
- `FaultDemarcation` records RCA algorithm duration.
- Unit tests can reset and inspect metric values deterministically.

**Implementation Steps:**
- [ ] Create a focused `TopoRCAStats` helper with atomic counters and reset methods for tests.
- [ ] Add `RecordTopoFetch`, `RecordGraphParse`, `RecordGraphTraversal`, `RecordUIDSet`, `RecordUIDSetReject`, and `RecordRCAAlgorithm` methods.
- [ ] Instrument `GraphTransform.Work` around fetch, parse, limit check, and traversal.
- [ ] Instrument `InTransform.AddGraphChunkToBufColumn` around UID set creation.
- [ ] Instrument `FaultDemarcation` with a deferred duration recorder.
- [ ] Add tests for recording and reset behavior.
- [ ] Run `go test ./engine/executor -run 'TestTopoRCAStats|TestGraphTransform|TestRCA'`.

**Suggested Commit:** `feat: add topo RCA query metrics`

---

### Story P0-S3: Decode RCA Event Annotations Into Typed Records

**User Story:** As an RCA algorithm maintainer, I need event annotations decoded once into typed records, so RCA logic is faster and invalid event data fails before graph traversal logic runs.

**Files:**
- Create: `engine/executor/rca_event.go`
- Create: `engine/executor/rca_event_test.go`
- Modify: `engine/executor/rca.go`
- Modify: `engine/executor/rca_test.go`

**Acceptance Criteria:**
- Alarm annotations decode `start_time`, optional `end_time`, and `create_time`.
- Event annotations decode `start_time`, optional `end_time`, and `create_time`.
- Anomaly annotations decode timestamp arrays.
- Missing required fields return RCA event schema errors.
- Malformed JSON returns RCA event schema errors.
- Existing `TestRCA` expected node and edge results remain unchanged.

**Implementation Steps:**
- [ ] Add `RCAEventRecord` with ID, entity ID, type, and typed timestamps.
- [ ] Add `DecodeRCAEventRecord` for one row annotation payload.
- [ ] Add `BuildRCAEventRecords` to convert chunk rows using the existing `colMap`.
- [ ] Replace repeated annotation JSON parsing in `extractCoreAnomalyTimestamps` and `isAnomaly` with typed record traversal.
- [ ] Keep current default matching windows unchanged: half hour for ended events, two hours for active or created events.
- [ ] Add unit tests for valid alarm, event, anomaly, malformed JSON, and missing timestamp cases.
- [ ] Run `go test ./engine/executor -run 'TestRCAEvent|TestRCA'`.

**Suggested Commit:** `feat: decode RCA events into typed records`

---

### Story P0-S4: Reuse Graph Adjacency Indexes in RCA

**User Story:** As a query performance owner, I need RCA to reuse graph adjacency indexes already built by graph loading, so large subgraphs do not pay duplicate indexing cost inside the RCA algorithm.

**Files:**
- Modify: `engine/executor/graph.go`
- Modify: `engine/executor/rca.go`
- Modify: `engine/executor/graph_test.go`
- Modify: `engine/executor/rca_test.go`

**Acceptance Criteria:**
- `Graph` exposes read-only source and target edge lookup methods.
- Accessors build missing indexes lazily for graphs created by tests or helper constructors.
- `FaultDemarcation` uses the accessors instead of rebuilding edge indexes locally.
- Existing RCA results do not change.
- Graph tests cover lazy index rebuild after constructing a `Graph` with only `Nodes` and `Edges`.

**Implementation Steps:**
- [ ] Add `EdgesFromSource(uid string) []GraphEdge` and `EdgesToTarget(uid string) []GraphEdge` methods to `Graph`.
- [ ] Ensure both methods call `ensureEdgeIndexes`.
- [ ] Add a `NodeByUID(uid string) (GraphNode, bool)` method if it makes RCA code clearer.
- [ ] Replace `buildGraphIndices` usage in `FaultDemarcation` with graph accessors and direct node lookups.
- [ ] Keep `buildGraphIndices` only if tests or other call sites still require it; otherwise remove it in this story.
- [ ] Run `go test ./engine/executor -run 'TestGraph|TestRCA'`.

**Suggested Commit:** `perf: reuse graph adjacency indexes in RCA`

---

### Story P0-S5: Document and Validate the RCA Query Contract

**User Story:** As a developer integrating RCA queries, I need documented defaults and validation tests for core entity, hop count, time windows, and empty-result behavior, so query behavior is predictable.

**Files:**
- Modify: `docs/topo_joint_query_optimization_plan.md`
- Modify: `docs/superpowers/specs/2026-04-27-topo-rca-joint-query-design.md`
- Modify: `engine/executor/graph_transform_test.go`
- Modify: `engine/executor/rca_event_test.go`
- Modify: `engine/executor/rca_test.go`

**Acceptance Criteria:**
- Docs state that P0 supports core-entity-driven RCA only.
- Docs state default traversal direction remains both directions.
- Docs state current RCA event correlation windows.
- Tests cover missing core entity fallback to task end time.
- Tests cover empty graph/empty UID set filtering behavior.
- Tests cover invalid event schema returning a classified error.

**Implementation Steps:**
- [ ] Update docs with P0 contract and examples of expected failure categories.
- [ ] Add graph transform tests for start-node missing and empty subgraph behavior.
- [ ] Add RCA event tests for core entity missing with `end_time` fallback.
- [ ] Add RCA event tests for core entity missing without `end_time` returning a classified error.
- [ ] Run `go test ./engine/executor -run 'TestGraphTransform|TestRCAEvent|TestRCA'`.

**Suggested Commit:** `docs: define topo RCA query contract`

---

### Story P0-S6: Add Baseline Benchmarks

**User Story:** As a performance engineer, I need repeatable graph and RCA benchmarks, so performance changes in P1 and P2 can be measured against P0.

**Files:**
- Modify: `engine/executor/graph_test.go`
- Modify: `engine/executor/rca_test.go`
- Modify: `docs/topo_joint_query_optimization_plan.md`

**Acceptance Criteria:**
- Benchmarks cover multi-hop filtering on existing test graph data.
- Benchmarks cover UID set generation for small and large subgraphs.
- Benchmarks cover RCA event decoding and fault demarcation.
- Benchmark command and expected benchmark names are documented.

**Implementation Steps:**
- [ ] Add or extend `BenchmarkMultiHopFilter` cases for small, medium, and large graph fixtures.
- [ ] Add `BenchmarkGraphUIDSet` for UID set creation under the configured limit.
- [ ] Add `BenchmarkRCAEventDecode` for typed event decoding.
- [ ] Add `BenchmarkFaultDemarcation` using existing RCA fixtures.
- [ ] Document the benchmark command: `go test ./engine/executor -run '^$' -bench 'Benchmark(MultiHopFilter|GraphUIDSet|RCAEventDecode|FaultDemarcation)' -benchmem`.
- [ ] Run the benchmark command and record baseline results in the docs.

**Suggested Commit:** `test: add topo RCA baseline benchmarks`

---

## P1 Stories

### Story P1-S1: Add Explicit Graph Traversal Direction

**User Story:** As an RCA user, I need to query upstream, downstream, or both directions, so suspected causes and impacted neighbors can be separated.

**Acceptance Criteria:**
- Direction defaults to both directions.
- Engine-level graph API supports income, outgo, and both.
- Parser or statement representation can carry direction before P1-S2 consumes it.
- Tests verify direction-specific nodes and edges.

**Suggested Commit:** `feat: support graph traversal direction`

### Story P1-S2: Expand Graph CTE Output Schema

**User Story:** As a query author, I need Graph CTE to expose node and path fields, so outer queries can filter by node kind, region, edge kind, direction, and traversal depth.

**Acceptance Criteria:**
- Graph rows expose node UID, node kind, node region, edge kind, source UID, target UID, depth, direction, and path ID.
- Existing graph consumers that only need `uid` remain compatible.
- Tests cover row conversion and graph-backed `IN` behavior.

**Suggested Commit:** `feat: expose RCA graph CTE fields`

### Story P1-S3: Mark Graph-Backed UID Filters

**User Story:** As a query optimizer, I need to distinguish graph-generated UID filters from normal `IN` literals, so later storage/index pushdown can choose a topology-aware path.

**Acceptance Criteria:**
- Graph-backed `IN` rewrite marks the condition or query options with UID filter metadata.
- Normal `IN` queries are unaffected.
- Logical plan tests can distinguish graph-backed UID filters from literal UID filters.

**Suggested Commit:** `feat: mark graph UID filters in planner`

### Story P1-S4: Push Graph UID Filters to Storage/Index Scan

**User Story:** As an RCA user querying large shards, I need graph UID filters pushed to index scan, so unrelated series are skipped before row scanning.

**Acceptance Criteria:**
- Index scan receives graph UID filter metadata.
- Tag or series index path filters candidate series by UID.
- Tests prove graph UID filter reduces scan candidates.
- Large UID set behavior remains bounded by `max-uid-set-size`.

**Suggested Commit:** `perf: push graph UID filters to index scan`

### Story P1-S5: Make RCA Time Windows Explicit

**User Story:** As an RCA user, I need configurable event correlation windows, so different event types and domains can tune matching without changing algorithm code.

**Acceptance Criteria:**
- Default windows preserve current half-hour and two-hour behavior.
- Algorithm parameters can override event-ended and event-active windows.
- Tests cover default and overridden windows.

**Suggested Commit:** `feat: configure RCA event windows`

---

## P2 Stories

### Story P2-S1: Add RCA Logical Workflow Node

**User Story:** As a planner developer, I need an RCA logical node that represents core entity, topology, metrics, events, and RCA output, so the optimizer can reason about the full RCA workflow.

**Acceptance Criteria:**
- Logical node can be constructed, cloned, explained, and encoded/decoded.
- Current P1 path can still execute without using the new node.
- Planner tests cover the new node lifecycle.

**Suggested Commit:** `feat: add RCA logical workflow node`

### Story P2-S2: Return Structured RCA Evidence

**User Story:** As an RCA consumer, I need evidence in the result, so the UI or API can explain why a node or edge was identified as related to the root cause.

**Acceptance Criteria:**
- RCA result includes matched event IDs, anomaly timestamps, path, direction, time delta, and inclusion reason.
- Existing graph-only output remains available.
- Tests cover evidence for at least one included node and one included edge.

**Suggested Commit:** `feat: return RCA evidence`

### Story P2-S3: Add RCA Strategy Selection

**User Story:** As a query optimizer, I need to choose topology-first, event-first, metric-first, or batched UID execution, so RCA queries can adapt to graph size and event selectivity.

**Acceptance Criteria:**
- Strategy selector accepts graph estimate, UID estimate, event estimate, time window, and configured limits.
- Selector chooses fail-fast for estimated limit violations.
- Unit tests cover each strategy.

**Suggested Commit:** `feat: select RCA execution strategy`

### Story P2-S4: Add Topo Manager Predicate Pushdown Adapter

**User Story:** As a topo integration owner, I need a client-side adapter for future topo manager filters, so start node, hop, direction, and predicates can be pushed down once the remote API supports them.

**Acceptance Criteria:**
- Client request builder can represent supported and unsupported remote filter capabilities.
- Unsupported filters remain local without changing semantics.
- Tests verify URL construction for both current and future capability sets.

**Suggested Commit:** `feat: prepare topo predicate pushdown`

---

## Recommended Sprint Slicing

### Sprint 1: P0 Stability Slice

Implement P0-S1, P0-S3, and P0-S4. This makes failures clearer, makes RCA input typed, and removes duplicate graph indexing without changing query behavior.

### Sprint 2: P0 Observability Slice

Implement P0-S2, P0-S5, and P0-S6. This adds production diagnostics, documents semantics, and establishes benchmark baselines.

### Sprint 3: P1 Query Efficiency Slice

Implement P1-S1, P1-S3, and P1-S4. This gives the engine enough metadata to optimize graph UID filtering.

### Sprint 4: P1 Expressiveness Slice

Implement P1-S2 and P1-S5. This improves query authoring and removes hard-coded RCA time windows.

### Later: P2 Workflow Slice

Implement P2 only after P0 and P1 are stable and benchmarked. P2 changes planner shape and result semantics, so it should not be mixed into P0 stabilization work.

---

## Definition of Done

Every story is done only when:

- The story has focused tests for the new behavior.
- Existing graph, IN transform, and RCA tests still pass.
- Any new config, query behavior, or output behavior is documented.
- Benchmarks are added or updated for performance-sensitive stories.
- The commit message matches the suggested story scope.

## Verification Commands

Use these commands as the default verification suite for P0 stories:

```bash
go test ./lib/errno ./engine/executor -run 'Test(Graph|GraphTransform|RCA|RCAEvent|TopoRCAStats|Error)'
go test ./engine/executor -run '^$' -bench 'Benchmark(MultiHopFilter|GraphUIDSet|RCAEventDecode|FaultDemarcation)' -benchmem
```

Use this wider command before merging a completed sprint:

```bash
go test ./engine/executor ./lib/errno ./lib/util ./lib/config
```

## Self-Review Notes

- P0 covers the approved spec requirements for error classification, observability, typed RCA input, duplicate graph indexing removal, contract documentation, and baseline benchmarks.
- P1 covers direction, graph schema, UID filter marking, UID pushdown, and configurable time windows.
- P2 covers first-class RCA logical workflow, evidence output, strategy selection, and future topo predicate pushdown.
- This plan intentionally does not implement metric-first or event-first RCA as production paths before P2, because the approved target scenario is core-entity-driven RCA.
