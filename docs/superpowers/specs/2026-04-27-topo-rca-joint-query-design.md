# Topo RCA Joint Query Design

## Scope

This design targets the RCA scenario where the user already has a core alarm or anomalous entity and needs to query nearby topology, metrics, and events to find likely root causes.

The current implementation path is:

1. Execute a `GRAPH` CTE.
2. Fetch topology from topo manager or mock data.
3. Build a local graph and run multi-hop filtering.
4. Convert the graph node set to `uid IN (...)`.
5. Rewrite the outer metric or event query.
6. Pass event chunks and the filtered subgraph into RCA fault demarcation.

The short-term plan keeps this path but makes it more explicit, observable, and performant. The longer-term plan introduces an RCA-specific query abstraction so planner and executor logic can reason about topology, metrics, events, and evidence as one workflow.

## Current Implementation Summary

- Topo manager access is implemented through `TopoProvider` in `lib/util/graph_client.go`.
- Query-local topo response reuse is attached to `PipelineExecutor` context.
- Graph traversal is implemented in `engine/executor/graph.go`.
- Graph CTE execution is implemented in `engine/executor/graph_transform.go`.
- Graph-backed `IN` filtering is implemented in `engine/executor/in_transform.go`.
- RCA fault demarcation is implemented in `engine/executor/rca.go`.
- Existing limits include topo request timeout, max response bytes, max graph nodes, max graph edges, and max UID set size.

## Business Design Problems

### RCA Semantics Are Not First-Class

The current query path reduces topology to a UID set. This supports basic filtering but does not directly represent RCA concepts:

- core entity
- propagation direction
- topology edge type
- event type
- metric anomaly window
- event correlation window
- evidence path
- reason a node was included or excluded

As a result, RCA intent is distributed across Graph SQL, outer query filters, `algoParams`, and RCA implementation details.

### Time Window Semantics Are Split

Graph execution parses additional conditions into topo manager `time`, `startTime`, and `endTime` parameters. RCA event matching separately uses fixed half-hour and two-hour windows. Metric query windows, topology snapshot time, and event correlation windows are not modeled as one contract.

For RCA, the query should explicitly define:

- topology snapshot or topology interval
- metric query interval
- event search interval
- event-to-anomaly correlation window
- fallback behavior when the core entity has no anomaly timestamp

### Direction and Path Semantics Are Weak

`MultiHopFilter` traverses both incoming and outgoing edges. This is useful for neighborhood discovery, but RCA often needs to distinguish upstream suspected causes from downstream impact. The query layer should support `income`, `outgo`, and `both`, with `both` as the compatibility default.

### Event and Metric Inputs Lack a Stable RCA Contract

RCA code currently depends on chunk columns, annotation JSON, and `algoParams`. This makes RCA sensitive to schema changes and malformed event data. Inputs should be validated before the algorithm runs and converted into typed RCA records.

### RCA Output Lacks Evidence

The result should explain why a node or edge was included. Useful evidence includes matching event IDs, metric anomaly timestamps, topology path, direction, time delta, and exclusion reasons for filtered candidates.

## Query Design Problems

### `uid IN (...)` Hides Topology Intent

The optimizer only sees a large `IN` filter, not a topology-neighborhood filter. This prevents topology-aware cost estimation, scan strategy selection, and RCA-specific planning.

### Topology Predicate Pushdown Is Limited

Current topo manager requests support only `applicationId`, `time`, `startTime`, and `endTime`. Node predicates, edge predicates, start node, hop count, and direction are evaluated locally. For large applications this can make response size, JSON parsing, and memory usage dominate query latency.

### Graph CTE Schema Is Too Narrow

The current graph output is effectively consumed as `uid`. RCA and downstream queries need fields such as:

- node `uid`
- node `kind`
- node `region`
- node tags
- edge `kind`
- source and target UID
- traversal depth
- traversal direction
- path identity

### Empty Result and Error Semantics Need Classification

The system should distinguish these cases:

- topo manager unavailable
- invalid topo manager response
- start node not found
- topology graph exceeds configured limit
- UID set exceeds configured limit
- topology query returned an empty graph
- graph filter produced an empty subgraph
- event query returned no matching events
- metric query returned no matching series
- RCA event schema is invalid

These cases should have distinct errors or structured statuses so RCA failures are diagnosable.

## Code Performance Problems

### Large UID Sets Inflate Expressions

`InTransform.rewriteInCondition` converts the UID map into an `influxql.SetLiteral`. This is acceptable for small neighborhoods but expensive for large UID sets because it increases memory usage, expression matching cost, and planner/executor overhead.

### UID Filtering Needs Storage/Index Pushdown

The current remaining work already identifies storage/index-scan-specific UID filter pushdown. This should be treated as a priority because a graph UID set is only useful if it avoids scanning unrelated series and shards.

### Full Graph JSON Parsing Is Expensive

`Graph.CreateGraph` unmarshals the complete topo response before building maps and adjacency indexes. Large responses can cause high CPU usage, memory spikes, and GC pressure.

### RCA Rebuilds Graph Indexes

`FaultDemarcation` builds source and target edge indexes from the subgraph, even though `Graph` already maintains adjacency indexes during graph construction. The RCA path should reuse graph indexes through read-only accessors or a graph view interface.

### Event Annotation Parsing Is Repeated and Fragile

RCA repeatedly unmarshals annotation JSON and uses `interface{}` assertions. This increases CPU cost and can panic on malformed data. Event rows should be decoded once into typed RCA event records with validation errors returned before algorithm execution.

### Observability Is Insufficient

The current path lacks metrics that show where time and memory are spent. This makes production RCA latency hard to debug.

## Recommended Approach

Use a phased approach:

1. Short term: strengthen the current `GRAPH CTE -> uid IN -> metric/event scan -> RCA` path.
2. Medium term: add topology-aware planning and UID filter pushdown.
3. Long term: introduce an RCA-specific logical query node that represents topology, metrics, events, and evidence as a single workflow.

This keeps near-term changes small while avoiding a permanent dependency on `uid IN (...)` as the only topology join representation.

## P0 Plan: Stabilize and Instrument Current Path

### Define the RCA Query Contract

Document and enforce default semantics for:

- core entity ID
- hop count
- direction, defaulting to `both`
- topology time or interval
- metric query interval
- event query interval
- event correlation window
- empty result handling

### Add Error Classification

Introduce explicit errors or status codes for topo fetch, graph construction, graph filtering, UID set creation, event decoding, metric query, and RCA execution failures.

### Add Query Metrics

Collect at least:

- topo request latency
- topo response bytes
- full graph node count
- full graph edge count
- subgraph node count
- subgraph edge count
- UID set size
- UID set rejection count
- graph JSON parse duration
- graph traversal duration
- `IN` rewrite duration
- outer metric/event scan duration
- RCA event decode duration
- RCA algorithm duration

### Reduce RCA Duplicate Work

Expose graph adjacency indexes through read-only methods or an interface and use them in `FaultDemarcation`. Decode event annotations once into typed RCA records before entering the algorithm.

## P1 Plan: Improve Query Performance and Expressiveness

### Add UID Filter Pushdown

Mark graph-backed `uid IN` conditions so the planner and storage/index scan can treat them as topology UID filters. Push them to series/tag index paths whenever possible.

### Extend Graph CTE Schema

Expose graph fields needed by RCA and downstream filters:

- node UID
- node kind
- node region
- node tags
- edge kind
- source UID
- target UID
- traversal depth
- traversal direction
- path ID

### Add Direction Support

Support `income`, `outgo`, and `both` at the SQL or Cypher-like syntax layer. Keep `both` as the default to preserve current behavior.

### Unify Time Window Configuration

Move RCA event windows out of hard-coded algorithm logic into query options or algorithm parameters. Keep current half-hour and two-hour values as defaults.

## P2 Plan: Add RCA-Specific Joint Query Capability

### Add an RCA Logical Node

Represent the workflow as:

`CoreEntity -> TopoNeighborhood -> Metric/Event Inputs -> RCA Evidence Graph`

This gives the planner direct visibility into RCA intent and avoids hiding topology inside a normal `IN` expression.

### Add Cost-Based Strategy Selection

Use hop count, graph size, UID set size, time range, event type, and index selectivity to choose among:

- expand topology first
- filter events first
- filter metrics first
- batch UID scans
- fail fast when configured limits predict excessive cost

### Push More Filters to Topo Manager

When topo manager supports it, push down:

- start node
- hop count
- direction
- node predicates
- edge predicates
- topology time range

### Return RCA Evidence

Return a graph plus structured evidence:

- matched event IDs
- matched anomaly timestamps
- related metric series
- path from core entity
- direction
- time delta
- inclusion reason
- exclusion reason

## Testing Plan

### Unit Tests

- Graph traversal with `income`, `outgo`, and `both`.
- Graph CTE schema fields.
- UID set limit and empty set semantics.
- Error classification for topo failures and invalid graph data.
- Typed event annotation decoding.
- RCA behavior with malformed, missing, and valid event annotations.

### Planner and Executor Tests

- Graph-backed UID filter is marked and pushed down.
- Empty graph result filters all points without panic.
- Start node not found returns a classified error.
- Oversized topology and oversized UID set fail before outer scans.
- Query-local topo cache still reuses identical requests.

### Performance Tests

- Large graph JSON parse and traversal benchmarks.
- UID set rewrite benchmark before and after pushdown.
- RCA event decode benchmark before and after typed parsing.
- End-to-end RCA query benchmark with small, medium, and large neighborhoods.

### Integration Tests

- Core alarm entity with N-hop topology and related events.
- Core entity with no matching events.
- Core entity missing from topology.
- Topo manager timeout.
- Large graph rejected by configured limits.

## Acceptance Criteria

P0 is complete when:

- RCA query semantics are documented.
- Major failure modes return classified errors.
- Topo, graph, UID, scan, and RCA timings are observable.
- RCA avoids duplicate graph indexing and repeated annotation parsing.

P1 is complete when:

- Graph-backed UID filtering can use storage/index pushdown.
- Graph CTE exposes RCA-relevant fields.
- Direction and time windows are explicit and tested.

P2 is complete when:

- RCA has a first-class logical query representation.
- Planner can choose among topology-first, event-first, metric-first, and batched strategies.
- RCA output includes structured evidence.

## Open Assumptions

- Strong consistency remains required for topology, so cross-query topo cache is not introduced until topo manager supports a version, ETag, or snapshot contract.
- `uid` remains the first supported join key between topology, metrics, and events.
- The first production target is the core-entity-driven RCA scenario, not metric-first or event-first RCA.
