// Copyright 2025 Huawei Cloud Computing Technologies Co., Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package executor

import (
	"sync"
	"testing"
)

// TestGraphConcurrentCreateAndFilter verifies that CreateGraph and
// MultiHopFilter/EdgesFromSource can run concurrently without data races.
// CreateGraph replaces G.Nodes, G.Edges, G.edgesBySource, G.edgesByTarget
// while ensureEdgeIndexes (called by accessors) reads them. Both paths
// must be serialized by edgeIndexMu.
func TestGraphConcurrentCreateAndFilter(t *testing.T) {
	g := NewGraph()
	mockData := `{
    "data": {
        "resultUid": "r1",
        "metadata": {"region": "global", "timestamp": "1", "topokeys": ["s0"]},
        "graph": {
            "vertex": [
                {"uid": "a", "metadata": {"kind": "Node", "region": "r1", "tags": {}}}
            ],
            "edges": []
        }
    }}`

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			g.CreateGraph(mockData)
		}()
		go func() {
			defer wg.Done()
			g.MultiHopFilter("a", 2, nil, nil)
		}()
	}
	wg.Wait()
}

// TestGraphConcurrentEdgeMutationAndAccessors verifies that mutations
// via BatchInsertEdges (the proper public API) do not race with
// EdgesFromSource/EdgesToTarget accessors.
func TestGraphConcurrentEdgeMutationAndAccessors(t *testing.T) {
	g := NewGraph()
	g.Nodes["a"] = GraphNode{Uid: "a"}
	g.Nodes["b"] = GraphNode{Uid: "b"}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			edgeData := GraphData{
				Graph: TopoInfo{
					Edges: []GraphEdge{{
						Uid: "ab",
						MetaData: EdgeMetaData{
							SourceUid: "a",
							TargetUid: "b",
							Kind:      "test",
							Tags:      map[string]string{"i": string(rune(idx))},
						},
					}},
				},
			}
			g.BatchInsertEdges(edgeData)
		}(i)
		go func() {
			defer wg.Done()
			g.EdgesFromSource("a")
		}()
	}
	wg.Wait()
}

// TestGraphConcurrentBatchInsertAndFilter verifies that BatchInsertEdges
// (which appends to G.edgesBySource, G.edgesByTarget, and modifies G.Nodes)
// does not race with ensureEdgeIndexes.
func TestGraphConcurrentBatchInsertAndFilter(t *testing.T) {
	g := NewGraph()
	g.Nodes["a"] = GraphNode{Uid: "a"}
	g.Nodes["b"] = GraphNode{Uid: "b"}

	edgeAB := GraphEdge{
		Uid: "ab",
		MetaData: EdgeMetaData{
			SourceUid: "a",
			TargetUid: "b",
		},
	}
	edgeBA := GraphEdge{
		Uid: "ba",
		MetaData: EdgeMetaData{
			SourceUid: "b",
			TargetUid: "a",
		},
	}
	graphData := GraphData{
		Graph: TopoInfo{
			Edges: []GraphEdge{edgeAB, edgeBA},
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			g.BatchInsertEdges(graphData)
		}()
		go func() {
			defer wg.Done()
			g.EdgesFromSource("a")
		}()
	}
	wg.Wait()
}
