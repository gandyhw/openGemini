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
	"container/list"
	"encoding/json"
	"fmt"

	"github.com/influxdata/influxdb/models"
	"github.com/openGemini/openGemini/lib/errno"
	"github.com/openGemini/openGemini/lib/util/lifted/influx/influxql"
)

// GraphNode is a vertex of the topology graph. OutEdges and InEdges hold the
// uids of the edges adjacent to the node, indexed by Graph.Edges.
type GraphNode struct {
	Uid      string       `json:"uid"`
	MetaData NodeMetaData `json:"metadata"`
	OutEdges []string
	InEdges  []string
}

// NodeMetaData carries the descriptive attributes of a GraphNode.
type NodeMetaData struct {
	Kind   string            `json:"kind"`
	Region string            `json:"region"`
	Tags   map[string]string `json:"tags"`
}

// GraphEdge is a directed edge of the topology graph.
type GraphEdge struct {
	Uid      string       `json:"uid"`
	MetaData EdgeMetaData `json:"metadata"`
}

// EdgeMetaData carries the descriptive attributes of a GraphEdge, including the
// uids of its source and target nodes.
type EdgeMetaData struct {
	Kind          string            `json:"kind"`
	SourceTopoKey string            `json:"sourceTopoKey"`
	SourceUid     string            `json:"sourceUid"`
	TargetTopoKey string            `json:"targetTopoKey"`
	TargetUid     string            `json:"targetUid"`
	Tags          map[string]string `json:"tags"`
}

// GraphData is the topology payload returned by the topo service.
type GraphData struct {
	ResultUid string   `json:"resultUid"`
	MetaData  MetaData `json:"metadata"`
	Graph     TopoInfo `json:"graph"`
}

// TopoInfo holds the raw vertices and edges of a topology graph.
type TopoInfo struct {
	Vertex []GraphNode `json:"vertex"`
	Edges  []GraphEdge `json:"edges"`
}

// Response is the top-level envelope of the topo service response.
type Response struct {
	Data GraphData `json:"data"`
}

// MetaData carries graph-level metadata of a topology payload.
type MetaData struct {
	Region    string   `json:"region"`
	Timestamp string   `json:"timestamp"`
	Topokeys  []string `json:"topokeys"`
}

// Graph is an in-memory topology graph indexed by node and edge uid.
type Graph struct {
	Nodes map[string]GraphNode
	Edges map[string]GraphEdge
}

// Field names recognised in node/edge filter conditions.
const (
	Kind string = "kind"
	Uid  string = "uid"
)

// hopDirection indicates which endpoint of an edge a hop traverses towards.
type hopDirection int

const (
	hopIncoming hopDirection = iota // traverse from an edge's target to its source
	hopOutgoing                     // traverse from an edge's source to its target
)

// IGraph is the behaviour consumed from a graph once it has been attached to a
// chunk. Construction helpers (CreateGraph, BatchInsert*) operate on the
// concrete *Graph and are intentionally not part of this interface.
type IGraph interface {
	GraphToRows() models.Rows
}

// NewGraph returns an empty Graph ready for insertion.
func NewGraph() *Graph {
	return &Graph{
		Nodes: make(map[string]GraphNode),
		Edges: make(map[string]GraphEdge),
	}
}

// GetNodeInfo returns a copy of the node with the given uid, or nil if absent.
// The returned value is a snapshot; mutating it does not affect the graph.
func (g *Graph) GetNodeInfo(id string) *GraphNode {
	if node, ok := g.Nodes[id]; ok {
		return &node
	}
	return nil
}

// GetEdgeInfo returns a copy of the edge with the given uid, or nil if absent.
// The returned value is a snapshot; mutating it does not affect the graph.
func (g *Graph) GetEdgeInfo(id string) *GraphEdge {
	if edge, ok := g.Edges[id]; ok {
		return &edge
	}
	return nil
}

// BatchInsertNodes inserts all vertices of graphData into the graph.
func (g *Graph) BatchInsertNodes(graphData GraphData) {
	for _, node := range graphData.Graph.Vertex {
		g.Nodes[node.Uid] = node
	}
}

// BatchInsertEdges inserts the edges of graphData and wires up the adjacency
// lists of their endpoints. Only the edges in this batch are wired so that the
// method may be called incrementally without duplicating adjacency entries.
func (g *Graph) BatchInsertEdges(graphData GraphData) error {
	for _, edge := range graphData.Graph.Edges {
		g.Edges[edge.Uid] = edge

		sourceNode, ok := g.Nodes[edge.MetaData.SourceUid]
		if !ok {
			return fmt.Errorf("this edge's sourceNode does not exist")
		}
		sourceNode.OutEdges = append(sourceNode.OutEdges, edge.Uid)
		g.Nodes[edge.MetaData.SourceUid] = sourceNode

		targetNode, ok := g.Nodes[edge.MetaData.TargetUid]
		if !ok {
			return fmt.Errorf("this edge's targetNode does not exist")
		}
		targetNode.InEdges = append(targetNode.InEdges, edge.Uid)
		g.Nodes[edge.MetaData.TargetUid] = targetNode
	}
	return nil
}

// CreateGraph builds the graph from a JSON topology payload.
func (g *Graph) CreateGraph(jsonGraphData string) error {
	var resp Response
	if err := json.Unmarshal([]byte(jsonGraphData), &resp); err != nil {
		return fmt.Errorf("parse graph json: %w", err)
	}
	g.BatchInsertNodes(resp.Data)
	return g.BatchInsertEdges(resp.Data)
}

// MultiHopFilter performs a breadth-first traversal of up to hopNum hops from
// startNodeId, keeping only the nodes and edges that satisfy nodeCondition and
// edgeCondition, and returns the resulting subgraph.
func (g *Graph) MultiHopFilter(startNodeId string, hopNum int, nodeCondition influxql.Expr, edgeCondition influxql.Expr) (*Graph, error) {
	startNode, ok := g.Nodes[startNodeId]
	if !ok {
		return nil, fmt.Errorf("MultiHopFilter startNodeId not found %s", startNodeId)
	}

	edgesBySource := make(map[string][]GraphEdge)
	edgesByTarget := make(map[string][]GraphEdge)
	for _, edge := range g.Edges {
		edgesBySource[edge.MetaData.SourceUid] = append(edgesBySource[edge.MetaData.SourceUid], edge)
		edgesByTarget[edge.MetaData.TargetUid] = append(edgesByTarget[edge.MetaData.TargetUid], edge)
	}

	visited := make(map[string]struct{})
	queue := list.New()
	queue.PushBack(startNode)
	visited[startNodeId] = struct{}{}

	subgraph := NewGraph()
	subgraph.Nodes[startNodeId] = startNode

	for queue.Len() > 0 && hopNum > 0 {
		levelSize := queue.Len()
		for i := 0; i < levelSize; i++ {
			current, ok := queue.Remove(queue.Front()).(GraphNode)
			if !ok {
				return nil, fmt.Errorf("queue element is not of type GraphNode")
			}

			if err := g.processEdges(edgesBySource[current.Uid], subgraph, visited, queue, nodeCondition, edgeCondition, hopOutgoing); err != nil {
				return nil, err
			}
			if err := g.processEdges(edgesByTarget[current.Uid], subgraph, visited, queue, nodeCondition, edgeCondition, hopIncoming); err != nil {
				return nil, err
			}
		}

		hopNum--
		if len(visited) == len(g.Nodes) {
			break
		}
	}
	return subgraph, nil
}

// processEdges evaluates the filter conditions against each edge and, for the
// matching ones, adds the edge and its unvisited endpoint to subgraph and
// enqueues that endpoint for the next hop.
func (g *Graph) processEdges(edges []GraphEdge, subgraph *Graph, visited map[string]struct{}, queue *list.List, nodeCondition influxql.Expr, edgeCondition influxql.Expr, hopDir hopDirection) error {
	for _, edge := range edges {
		isMatch, err := g.isMatchQueryConditions(nodeCondition, edgeCondition, edge, hopDir)
		if err != nil {
			return err
		}
		if !isMatch {
			continue
		}

		curUid := edge.MetaData.TargetUid
		if hopDir == hopIncoming {
			curUid = edge.MetaData.SourceUid
		}
		curNode, exists := g.Nodes[curUid]
		if !exists {
			return fmt.Errorf("topo data received from api is incorrect")
		}

		subgraph.Edges[edge.Uid] = edge
		if _, ok := visited[curUid]; ok {
			continue
		}
		subgraph.Nodes[curNode.Uid] = curNode
		visited[curNode.Uid] = struct{}{}
		queue.PushBack(curNode)
	}
	return nil
}

// isMatchQueryConditions reports whether edge satisfies both the node and edge
// filter conditions.
func (g *Graph) isMatchQueryConditions(nodeCondition influxql.Expr, edgeCondition influxql.Expr, edge GraphEdge, hopDir hopDirection) (bool, error) {
	isNodeMatch, err := g.isFilterConditionMatch(nodeCondition, edge, hopDir, g.checkNodeFilterCondition)
	if err != nil {
		return false, err
	}
	isEdgeMatch, err := g.isFilterConditionMatch(edgeCondition, edge, hopDir, g.checkEdgeFilterCondition)
	if err != nil {
		return false, err
	}
	return isNodeMatch && isEdgeMatch, nil
}

func (g *Graph) isFilterConditionMatch(cond influxql.Expr, edge GraphEdge, hopDir hopDirection, handler func(varRef string, literal string, edge GraphEdge, op influxql.Token, hopDir hopDirection) (bool, error)) (bool, error) {
	checkField := func(expr influxql.Expr) (bool, error) {
		binaryExpr, ok := expr.(*influxql.BinaryExpr)
		if !ok {
			return false, errno.NewError(errno.ConvertToBinaryExprFailed, expr)
		}

		var varRef *influxql.VarRef
		var literal *influxql.StringLiteral
		switch binaryExpr.Op {
		case influxql.EQ, influxql.NEQ:
			if varRef, ok = binaryExpr.LHS.(*influxql.VarRef); !ok {
				if varRef, ok = binaryExpr.RHS.(*influxql.VarRef); !ok {
					return false, fmt.Errorf("unsupported edge or node filter condition syntax")
				}
			}
			if literal, ok = binaryExpr.RHS.(*influxql.StringLiteral); !ok {
				if literal, ok = binaryExpr.LHS.(*influxql.StringLiteral); !ok {
					return false, fmt.Errorf("unsupported edge or node filter condition syntax")
				}
			}
			return handler(varRef.Val, literal.Val, edge, binaryExpr.Op, hopDir)
		default:
			return false, fmt.Errorf("unsupported operator: %s", binaryExpr.Op)
		}
	}
	return g.checkCondition(cond, checkField)
}

func (g *Graph) checkEdgeFilterCondition(varRef string, literal string, edge GraphEdge, op influxql.Token, hopDir hopDirection) (bool, error) {
	switch varRef {
	case Kind:
		return matchString(edge.MetaData.Kind, literal, op), nil
	default:
		return matchTag(edge.MetaData.Tags, varRef, literal, op), nil
	}
}

func (g *Graph) checkNodeFilterCondition(varRef string, literal string, edge GraphEdge, op influxql.Token, hopDir hopDirection) (bool, error) {
	nodeUid := edge.MetaData.TargetUid
	if hopDir == hopIncoming {
		nodeUid = edge.MetaData.SourceUid
	}
	node, ok := g.Nodes[nodeUid]
	if !ok {
		return false, fmt.Errorf("the nodeUid not found")
	}
	switch varRef {
	case Kind:
		return matchString(node.MetaData.Kind, literal, op), nil
	case Uid:
		return matchString(nodeUid, literal, op), nil
	default:
		return matchTag(node.MetaData.Tags, varRef, literal, op), nil
	}
}

// matchString evaluates an EQ/NEQ comparison between two strings.
func matchString(actual string, expected string, op influxql.Token) bool {
	return (op == influxql.EQ && actual == expected) || (op == influxql.NEQ && actual != expected)
}

// matchTag evaluates an EQ/NEQ comparison against a tag value. A missing tag
// matches NEQ (the value differs) and never matches EQ.
func matchTag(tags map[string]string, key string, val string, op influxql.Token) bool {
	if v, ok := tags[key]; ok {
		return matchString(v, val, op)
	}
	return op == influxql.NEQ
}

func (g *Graph) checkCondition(expr influxql.Expr, checkField func(influxql.Expr) (bool, error)) (bool, error) {
	if expr == nil {
		return true, nil
	}

	if binaryExpr, ok := expr.(*influxql.BinaryExpr); ok {
		switch binaryExpr.Op {
		case influxql.EQ, influxql.NEQ:
			return checkField(expr)
		case influxql.OR:
			leftResult, err := g.checkCondition(binaryExpr.LHS, checkField)
			if err != nil {
				return false, err
			}
			if leftResult {
				return true, nil
			}
			return g.checkCondition(binaryExpr.RHS, checkField)
		case influxql.AND:
			leftResult, err := g.checkCondition(binaryExpr.LHS, checkField)
			if err != nil {
				return false, err
			}
			if !leftResult {
				return false, nil
			}
			return g.checkCondition(binaryExpr.RHS, checkField)
		default:
			return false, fmt.Errorf("unsupported operator: %s", binaryExpr.Op)
		}
	}
	if parenExpr, ok := expr.(*influxql.ParenExpr); ok {
		return g.checkCondition(parenExpr.Expr, checkField)
	}

	return checkField(expr)
}

// GraphToRows renders the graph's nodes and edges as two models.Row tables.
func (g *Graph) GraphToRows() models.Rows {
	nodeRow := &models.Row{Columns: []string{"Uid", "MetaData"}}
	for _, node := range g.Nodes {
		nodeRow.Values = append(nodeRow.Values, []interface{}{node.Uid, node.MetaData})
	}

	edgeRow := &models.Row{Columns: []string{"Uid", "MetaData"}}
	for _, edge := range g.Edges {
		edgeRow.Values = append(edgeRow.Values, []interface{}{edge.Uid, edge.MetaData})
	}
	return models.Rows{nodeRow, edgeRow}
}

func (g *Graph) addToBufMap(bufMap map[interface{}]struct{}) {
	for _, v := range g.Nodes {
		bufMap[v.Uid] = struct{}{}
	}
}
