# Topo Joint Query Optimization Plan

## Summary

The first implementation phase focuses on the `GRAPH CTE -> uid IN (...) -> time-series scan` path. The external topo service remains the source of truth and is treated as strongly consistent: openGemini does not use cross-query topo cache or stale fallback. The current remote API is assumed to support only `applicationId`, `time`, `startTime`, and `endTime`; graph traversal and node/edge predicates are still evaluated locally.

## Implemented Changes

- Added bounded topo access through `TopoProvider` in `lib/util/graph_client.go`.
  - Requests now support context cancellation, request timeout, response size limit, and explicit error propagation.
  - A per-query topo cache is attached to `PipelineExecutor` context so identical topo requests in one query share one response.
  - Remote failures return errors directly; stale data is not used.
- Added topo resource controls in `[topo]`.
  - `request-timeout`
  - `max-response-bytes`
  - `max-graph-nodes`
  - `max-graph-edges`
  - `max-uid-set-size`
- Reworked local graph traversal.
  - `Graph` now keeps source and target adjacency indexes when edges are loaded.
  - `MultiHopFilter` reuses those indexes instead of rebuilding them for every traversal.
- Tightened Graph+IN semantics.
  - Graph-backed `IN` queries only support `uid IN (SELECT uid FROM graph_cte)` in this phase.
  - The graph node UID set is bounded by `max-uid-set-size`; oversized sets fail fast.
  - Empty UID sets continue to filter all points.

## Remaining Work

- Add storage/index-scan specific UID filter pushdown if the generic `IN` condition path is not selective enough for large shards.
- Add query metrics for topo request latency, response size, graph node/edge count, UID set size, and rejection count.
- Extend remote topo filtering only after the topo service supports start node, hop, and property predicate parameters.
- Consider version/ETag support before introducing cross-query topo cache.

## Test Coverage

- `lib/config/topo_test.go` validates defaults and invalid negative limits.
- `lib/util/graph_client_test.go` covers URL construction, bounded response handling, and per-query cache reuse.
- Existing graph and graph transform tests continue to cover local graph parsing and traversal semantics.

## Assumptions

- Strong consistency is required, so cache reuse is limited to one query execution context.
- The first supported time-series join key is the fixed `uid` field/tag.
- High-risk queries fail explicitly instead of silently degrading to empty results or stale topology.
