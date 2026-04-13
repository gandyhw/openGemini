# Topo-Correlated Time Series Query for RCA

**Date**: 2026-04-13
**Status**: Draft
**Approach**: Extend existing RCAOp (Option A)

## 1. Background

openGemini already has foundational infrastructure for topology-correlated queries:

- `GraphStatement` / `GraphTransform`: Fetches topology from an external API via HTTP callback, runs `MultiHopFilter` BFS to extract a subgraph.
- `TableFunctionTransform` / `RCAOp`: Combines topo subgraph with event data (ops_events) to perform fault demarcation via BFS anomaly propagation.
- CTE (`WITH` clause): Allows `GraphStatement` as a CTE input to `TableFunction`.

**Current limitation**: RCAOp only processes event data (anomaly/alarm/event types). It cannot correlate metrics time-series data (CPU, memory, latency, etc.) from openGemini measurements with topology nodes.

## 2. Goal

Enable a single query to:
1. Fetch topology subgraph from an external API (existing capability).
2. Run fault demarcation on event data (existing capability).
3. **[NEW]** Correlate metrics time-series data from multiple measurements with topology nodes.
4. **[NEW]** Return a structured result: subgraph + per-node events + per-node metrics.

## 3. Design Overview

### 3.1 Query Syntax

No grammar changes needed. The existing `TABLE_FUNCTION` syntax supports N source params + 1 string param:

```sql
WITH topo AS (
    2 vm1 kind = 'Node' kind = 'communication'
    WHERE applicationId = 'app1' AND time > now() - 1h AND time < now()
)
SELECT * FROM rca(
    topo,
    ops_events,
    cpu_usage,
    mem,
    '{"hop_count":2, "bfs_narrow":false,
      "task":{"metadata":{"core_entity_id":"vm1"}},
      "metrics_config":{
        "tag_key":"host",
        "aggregation":"none",
        "measurements":[
          {"name":"cpu_usage","fields":["usage_idle","usage_system"]},
          {"name":"mem","fields":["used_percent"]}
        ]
      }
    }'
)
WHERE time > now() - 1h AND time < now()
```

- CTE `topo`: GraphStatement fetches topology, filters by hop count / node condition / edge condition.
- `rca(...)`: Table function with N inputs — 1 topo (from CTE), 1 events measurement, N metrics measurements, 1 JSON param string.

### 3.2 Execution Pipeline

```
GraphTransform (callback external API -> MultiHopFilter)
        |
   [topo subgraph chunk]
                            EventReader           MetricsReader(s)
                                |                      |
                           [event chunks]        [metrics chunks]
        |                       |                      |
              TableFunctionTransform (extended RCAOp)
        |
   [structured result: subgraph + node events + node metrics]
```

### 3.3 Data Model Extensions

#### 3.3.1 GraphNode Extension

File: `engine/executor/graph.go`

```go
type GraphNode struct {
    Uid      string            `json:"uid"`
    MetaData NodeMetaData      `json:"metadata"`
    OutEdges []string
    InEdges  []string

    // RCA result attached data
    Events  []NodeEvent        `json:"events,omitempty"`
    Metrics []NodeMetricSeries `json:"metrics,omitempty"`
}

type NodeEvent struct {
    ID          string                 `json:"id"`
    Type        string                 `json:"type"`         // anomaly / alarm / event
    EntityID    string                 `json:"entity_id"`
    Annotations map[string]interface{} `json:"annotations"`
}

type NodeMetricSeries struct {
    Measurement string            `json:"measurement"`
    Field       string            `json:"field"`
    Tags        map[string]string `json:"tags,omitempty"`
    Timestamps  []int64           `json:"timestamps"`
    Values      []float64         `json:"values"`
}
```

#### 3.3.2 AlgoParam Extension

File: `engine/executor/rca.go`

```go
type AlgoParam struct {
    HopCount      int                    `json:"hop_count"`
    BFSNarrow     bool                   `json:"bfs_narrow"`
    Task          map[string]interface{} `json:"task"`
    MetricsConfig *MetricsConfig         `json:"metrics_config,omitempty"`
}

type MetricsConfig struct {
    TagKey       string              `json:"tag_key"`       // tag name matching topo node ID (e.g. "host")
    Aggregation  string              `json:"aggregation"`   // "none", "mean", "max", "min", "sum"
    Measurements []MeasurementConfig `json:"measurements"`
}

type MeasurementConfig struct {
    Name   string   `json:"name"`
    Fields []string `json:"fields"`
}
```

### 3.4 RCAOp Core Logic Changes

File: `engine/executor/table_function_factory.go`

**Current**: Hardcoded `len(ChunkPortsWithMetas) != 2` check, implicit input identification.

**New flow**:

```
1. Classify inputs (>= 2 routes):
   - Has IGraph         -> topo
   - Has "type" column  -> events
   - Otherwise          -> metrics

2. Parse algo_params, extract metrics_config

3. FaultDemarcation(events, topo) -> demarcated subgraph (existing logic, unchanged)

4. [NEW] attachEventsToGraph(subgraph, events, colMap):
   - Iterate event chunks, match entity_id to node UIDs
   - Attach matched events to GraphNode.Events

5. [NEW] attachMetricsToGraph(subgraph, metricsInputs, metricsConfig):
   - Extract all node IDs from subgraph into a set
   - For each metrics input, scan chunks:
     - Match tag_key value against node ID set
     - Filter fields per metricsConfig.measurements[].fields
     - Build NodeMetricSeries and attach to GraphNode.Metrics
   - Apply aggregation if metricsConfig.aggregation != "none"

6. Return subgraph with attached events + metrics
```

**Input classification** (new function):

Input identification uses `SourceName` (the measurement name from the SQL source list) cross-referenced against `metrics_config.measurements[].name`. This avoids ambiguity from column-based heuristics (e.g. a metrics measurement could also have a "type" column).

```go
func classifyInputs(inputs []*ChunkPortsWithMeta, metricsConfig *MetricsConfig) (
    topo IGraph,
    events *ChunkPortsWithMeta,
    metrics []*ChunkPortsWithMeta,
    err error,
) {
    metricNames := make(map[string]struct{})
    if metricsConfig != nil {
        for _, m := range metricsConfig.Measurements {
            metricNames[m.Name] = struct{}{}
        }
    }

    for _, input := range inputs {
        if input.IGraph != nil {
            topo = input.IGraph
        } else if _, isMetric := metricNames[input.SourceName]; isMetric {
            metrics = append(metrics, input)
        } else {
            events = input
        }
    }
    if topo == nil {
        err = errors.New("rca: topo input not found")
    }
    if events == nil {
        err = errors.New("rca: events input not found")
    }
    return
}
```

Note: This requires `algo_params` to be parsed before input classification, so the flow in section 3.4 becomes: parse params (step 2) -> classify inputs (step 1) -> remaining steps.

### 3.5 Output Format

#### 3.5.1 New GraphToJSON method

File: `engine/executor/graph.go`

```go
func (G *Graph) GraphToJSON() ([]byte, error) {
    type jsonResult struct {
        Nodes []GraphNode `json:"nodes"`
        Edges []GraphEdge `json:"edges"`
    }
    result := jsonResult{
        Nodes: make([]GraphNode, 0, len(G.Nodes)),
        Edges: make([]GraphEdge, 0, len(G.Edges)),
    }
    for _, node := range G.Nodes {
        result.Nodes = append(result.Nodes, node)
    }
    for _, edge := range G.Edges {
        result.Edges = append(result.Edges, edge)
    }
    return json.Marshal(result)
}
```

#### 3.5.2 HttpSenderTransform adaptation

File: `engine/executor/httpsender_transform.go`

When a chunk carries a Graph with attached Events/Metrics, use `GraphToJSON()` instead of `GraphToRows()`. Detection: check if any node has non-empty Events or Metrics.

Retain `GraphToRows()` for backward compatibility with pure topology queries.

### 3.6 ChunkPortsWithMeta Extension

File: `engine/executor/table_function_transform.go`

```go
type ChunkPortsWithMeta struct {
    ChunkPort  *ChunkPort
    Chunks     []Chunk
    IGraph     IGraph
    Meta       map[string]int
    SourceName string          // NEW: measurement name for explicit input identification
}
```

`SourceName` is populated from `LogicalTableFunction.TableFunctionSource` during `NewTableFunctionTransform`, providing explicit identification of each input's origin measurement.

## 4. Existing Code Issues and Improvements

### 4.1 [HIGH] FaultDemarcation recover swallows panic context

**Location**: `rca.go:161-166`

**Problem**: Unprotected type assertions (`ts.(float64)` at line 113, etc.) can panic. The recover handler logs a generic message, losing the original panic value and stack trace.

**Fix**:
- Replace all bare type assertions with comma-ok form.
- Log the recovered value `r` and stack trace in the recover handler.
- Goal: eliminate the need for recover entirely by handling all type errors explicitly.

### 4.2 [MEDIUM] isAnomaly O(N*M) full scan per node

**Location**: `rca.go:83-158`, called at `rca.go:213`

**Problem**: For each node in the BFS queue, `isAnomaly` scans all event chunks linearly. With N nodes and M events, complexity is O(N*M).

**Fix**: Pre-build an `entityID -> []parsedEvent` index before the BFS loop. Change `isAnomaly` to O(1) lookup:

```go
eventIndex := buildEventIndex(chunks, colMap) // map[string][]ParsedEvent
// In BFS loop:
anomaly := isAnomalyFromIndex(coreAnomalyTS, curEntityID, eventIndex)
```

### 4.3 [HIGH] GraphTransform external API callback has no timeout

**Location**: `graph_transform.go:162`, `graph_client.go:45-51`

**Problem**: `HttpsClient` has no `Timeout`. `SendGetRequest` does not use `context.Context`. A slow/unresponsive external topo API blocks the entire query indefinitely.

**Fix**:
- `SendGetRequest` accepts `context.Context`, uses `http.NewRequestWithContext`.
- `GraphTransform.Work` passes its `ctx` to `SendGetRequest`.
- Set `HttpsClient.Timeout = 30 * time.Second` as a default.

### 4.4 [HIGH] RCAOp hardcoded 2-input limit

**Location**: `table_function_factory.go:56`

**Problem**: `len(ChunkPortsWithMetas) != 2` blocks multi-input scenarios.

**Fix**: Change to `len(ChunkPortsWithMetas) < 2` minimum check. Use `classifyInputs()` for explicit type identification.

### 4.5 [LOW] `*new(IGraph)` non-idiomatic nil interface

**Location**: `table_function_factory.go:62`

**Problem**: `topoIGraph := *new(IGraph)` is a non-idiomatic way to declare a nil interface.

**Fix**: Replace with `var topoIGraph IGraph`.

### 4.6 [HIGH] GraphToRows cannot represent nested attached data

**Location**: `graph.go:407-418`, `httpsender_transform.go:536-539`

**Problem**: `GraphToRows()` outputs flat `Uid + MetaData` rows. Cannot express nested Events/Metrics per node.

**Fix**: Add `GraphToJSON()` method (see section 3.5.1). `HttpSenderTransform` uses JSON path for enriched graphs, retains `GraphToRows()` for plain topology queries.

### 4.7 [MEDIUM] Dual hop-count semantics confusion

**Location**: `graph.go:168` (`GraphStatement.HopNum`) vs `rca.go:185` (`AlgoParam.HopCount`)

**Problem**: Two separate BFS hop parameters with different semantics. When `AlgoParam.HopCount > GraphStatement.HopNum`, RCA search exceeds the topo subgraph boundary, causing silent node misses.

**Fix**:
- Add validation in `RCAOp.Run`: if `algo_params.hop_count` exceeds the topo subgraph's effective radius, return an explicit error.
- Document the distinction: topo hop = data fetch scope, RCA hop = fault propagation search scope.

## 5. Files Changed

| File | Change Type | Description |
|------|------------|-------------|
| `engine/executor/graph.go` | Modify | Add `Events`, `Metrics` fields to `GraphNode`; add `NodeEvent`, `NodeMetricSeries` types; add `GraphToJSON()` |
| `engine/executor/rca.go` | Modify | Add `MetricsConfig` types to `AlgoParam`; add `attachEventsToGraph()`, `attachMetricsToGraph()`, `buildEventIndex()`; fix bare type assertions |
| `engine/executor/table_function_factory.go` | Modify | Remove 2-input limit; add `classifyInputs()`; fix `*new(IGraph)` |
| `engine/executor/table_function_transform.go` | Modify | Add `SourceName` to `ChunkPortsWithMeta`; populate from logical plan |
| `engine/executor/graph_transform.go` | Modify | Pass `context.Context` to `SendGetRequest` |
| `lib/util/graph_client.go` | Modify | `SendGetRequest` accepts `context.Context`; set `HttpsClient.Timeout` |
| `engine/executor/httpsender_transform.go` | Modify | Detect enriched graph, use `GraphToJSON()` path |

## 6. Testing Strategy

- **Unit tests**: `rca_test.go` — test `classifyInputs`, `attachMetricsToGraph`, `attachEventsToGraph` with mock data.
- **Unit tests**: `graph_test.go` — test `GraphToJSON` output structure.
- **Integration test**: End-to-end query with topo CTE + events + metrics measurements, verify structured JSON output.
- **Regression test**: Existing RCA queries (2-input, no metrics) must continue to work unchanged.
- **Edge cases**: Empty metrics, node ID not found in metrics, aggregation modes, hop count validation.
