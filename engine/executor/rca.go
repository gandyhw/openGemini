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
	"errors"
	"sort"

	"github.com/openGemini/openGemini/engine/hybridqp"
)

const (
	ID          = "id"
	Name        = "name"
	Level       = "level"
	RuleID      = "rule_id"
	EntityID    = "entity_id"
	Type        = "type"
	Annotations = "annotations"
)

// RCA assumes that the following fields exist in AnomalyEvent.Annotations.
const (
	// anomaly
	ANOMALY    = "anomaly"    // event type
	Timestamps = "timestamps" // required field

	// alarm
	ALARM = "alarm" // event type

	// event
	EVENT     = "event"       // event type
	CreatedTS = "create_time" // required field

	// alarm & event
	StartTS = "start_time" // optional field for EVENT, required field for ALARM
	EndTS   = "end_time"   // optional
)

type AlgoParam struct {
	HopCount  int                    `json:"hop_count"`
	BFSNarrow bool                   `json:"bfs_narrow"`
	Task      map[string]interface{} `json:"task"`
}

// RCA assumes that the following fields exist in AlgoParam.Task.
const (
	META       = "metadata"
	CoreEntity = "core_entity_id"
)

func isWithinTSRange(targetTS int64, sortedTSList []int64, closeHourMS int64) bool {
	// Search for the nearest timestamp.
	pos := sort.Search(len(sortedTSList), func(i int) bool {
		return sortedTSList[i] >= targetTS
	})

	// Check the current & previous position.
	for _, i := range []int{pos, pos - 1} {
		if i >= 0 && i < len(sortedTSList) {
			if hybridqp.Abs(targetTS-sortedTSList[i]) <= closeHourMS {
				return true
			}
		}
	}
	return false
}

func isAnomaly(anomalyTS []int64, curEntityID string, records []RCAEventRecord) (bool, error) {
	const (
		halfHourMs = 30 * 60 * 1000
		twoHourMs  = 120 * 60 * 1000
	)

	for _, record := range records {
		if record.EntityID != curEntityID {
			continue
		}
		switch record.Type {
		case ANOMALY:
			for _, ts := range record.Timestamps {
				if isWithinTSRange(ts, anomalyTS, halfHourMs) {
					return true, nil
				}
			}
		case ALARM:
			if record.HasEndTS {
				if isWithinTSRange(record.StartTS, anomalyTS, halfHourMs) {
					return true, nil
				}
			} else if isWithinTSRange(record.StartTS, anomalyTS, twoHourMs) {
				return true, nil
			}
		case EVENT:
			if record.HasEndTS {
				if isWithinTSRange(record.EndTS, anomalyTS, halfHourMs) {
					return true, nil
				}
			} else if record.HasStartTS {
				if isWithinTSRange(record.StartTS, anomalyTS, twoHourMs) {
					return true, nil
				}
			} else if isWithinTSRange(record.CreatedTS, anomalyTS, twoHourMs) {
				return true, nil
			}
		}
	}

	return false, nil
}

func FaultDemarcation(chunks []Chunk, subTopo *Graph, algoParams AlgoParam, colMap map[string]int) (graph *Graph, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("RCA Error: FaultDemarcation failed")
			err = errors.New("RCA Error: FaultDemarcation failed")
		}
	}()
	tmp, ok := algoParams.Task[META]
	if !ok {
		return nil, errors.New("RCA Error: meta not found in algoParams")
	}
	taskMeta, ok := tmp.(map[string]interface{})
	if !ok {
		return nil, errors.New("RCA Error: meta format error in algoParams")
	}

	tmp, ok = taskMeta[CoreEntity]
	if !ok {
		return nil, errors.New("RCA Error: core entity not found in task meta")
	}
	coreEntityID, ok := tmp.(string)
	if !ok {
		return nil, errors.New("RCA Error: core entity type error in task meta")
	}

	BFSHopCount := algoParams.HopCount
	if BFSHopCount == 0 {
		BFSHopCount = 2
	}
	BFSNarrow := algoParams.BFSNarrow

	records, err := BuildRCAEventRecords(chunks, colMap)
	if err != nil {
		return nil, err
	}

	// Extract anomaly timestamps.
	coreAnomalyTS, err := extractCoreAnomalyTimestamps(records, coreEntityID, taskMeta)
	if err != nil {
		return nil, err
	}

	subTopo.ensureEdgeIndexes()
	edgeList := make([]GraphEdge, 0, len(subTopo.Edges))
	existedEdgeID := make(map[string]struct{}, len(subTopo.Edges))
	visitedNodes := make(map[string]struct{}, len(subTopo.Nodes))
	visitedNodes[coreEntityID] = struct{}{}
	nodeQueue := []string{coreEntityID}
	nodeList := make([]GraphNode, 0, len(subTopo.Nodes))
	if node, ok := subTopo.Nodes[coreEntityID]; ok {
		nodeList = append(nodeList, node)
	}
	idx := 0

	for idx < len(nodeQueue) {
		curEntityID := nodeQueue[idx]
		anomaly, err := isAnomaly(coreAnomalyTS, curEntityID, records)
		if err != nil {
			return nil, err
		}
		if !anomaly {
			idx += 1
			continue
		}
		tmpVisited := make(map[string]struct{})
		tmpVisited[curEntityID] = struct{}{}
		tmpNodeIDList := []string{curEntityID}
		tmpHopCount := []int{0}
		tmpIdx := 0
		for tmpIdx < len(tmpNodeIDList) {
			tmpEntityID := tmpNodeIDList[tmpIdx]
			for _, tmpCase := range subTopo.edgesFromSource(tmpEntityID) {
				// edge.uid == SourceUid_SourceTopoKey::::TargetUid_TargetTopoKey
				meta := tmpCase.MetaData
				edgeUid := meta.SourceUid + "_" + meta.SourceTopoKey +
					"::::" + meta.TargetUid + "_" + meta.TargetTopoKey
				_, inExistedEdge := existedEdgeID[edgeUid]
				_, inNode := visitedNodes[meta.TargetUid]
				_, inTmpNode := tmpVisited[meta.TargetUid]
				if !inExistedEdge && (inNode || inTmpNode) {
					existedEdgeID[edgeUid] = struct{}{}
					edgeList = append(edgeList, tmpCase)
				}
				if tmpHopCount[tmpIdx] < BFSHopCount && !inTmpNode {
					tmpVisited[meta.TargetUid] = struct{}{}
					tmpNodeIDList = append(tmpNodeIDList, meta.TargetUid)
					tmpHopCount = append(tmpHopCount, tmpHopCount[tmpIdx]+1)
				}
			}
			for _, tmpCase := range subTopo.edgesToTarget(tmpEntityID) {
				// edge.uid == SourceUid_SourceTopoKey::::TargetUid_TargetTopoKey
				meta := tmpCase.MetaData
				edgeUid := meta.SourceUid + "_" + meta.SourceTopoKey +
					"::::" + meta.TargetUid + "_" + meta.TargetTopoKey
				_, inExistedEdge := existedEdgeID[edgeUid]
				_, inNode := visitedNodes[meta.SourceUid]
				_, inTmpNode := tmpVisited[meta.SourceUid]
				if !inExistedEdge && (inNode || inTmpNode) {
					existedEdgeID[edgeUid] = struct{}{}
					edgeList = append(edgeList, tmpCase)
				}
				if tmpHopCount[tmpIdx] < BFSHopCount && !inTmpNode {
					tmpVisited[meta.SourceUid] = struct{}{}
					tmpNodeIDList = append(tmpNodeIDList, meta.SourceUid)
					tmpHopCount = append(tmpHopCount, tmpHopCount[tmpIdx]+1)
				}
			}
			tmpIdx += 1
		}
		for tmpNode := range tmpVisited {
			if _, ok := visitedNodes[tmpNode]; !ok {
				if n, ok := subTopo.Nodes[tmpNode]; ok {
					nodeList = append(nodeList, n)
				}
				visitedNodes[tmpNode] = struct{}{}
				nodeQueue = append(nodeQueue, tmpNode)
			}
		}
		if BFSNarrow {
			BFSHopCount = 1
		}
		idx += 1
	}

	return buildGraph(nodeList, edgeList), nil
}

func buildGraph(nodeList []GraphNode, edgeList []GraphEdge) *Graph {
	nodes := make(map[string]GraphNode, len(nodeList))
	edges := make(map[string]GraphEdge, len(edgeList))
	for _, node := range nodeList {
		nodes[node.Uid] = node
	}
	for _, edge := range edgeList {
		edges[edge.Uid] = edge
	}
	return &Graph{Nodes: nodes, Edges: edges}
}

func extractCoreAnomalyTimestamps(records []RCAEventRecord, coreEntityID string, taskMeta map[string]interface{}) ([]int64, error) {
	var coreAnomalyTS []int64
	found := false

	for _, record := range records {
		if record.EntityID != coreEntityID {
			continue
		}
		found = true

		switch record.Type {
		case ANOMALY:
			coreAnomalyTS = append(coreAnomalyTS, record.Timestamps...)
		case ALARM:
			coreAnomalyTS = append(coreAnomalyTS, record.StartTS)
		case EVENT:
			if record.HasStartTS {
				coreAnomalyTS = append(coreAnomalyTS, record.StartTS)
			} else {
				coreAnomalyTS = append(coreAnomalyTS, record.CreatedTS)
			}
		}
	}

	if !found || len(coreAnomalyTS) == 0 {
		if endTime, ok := taskMeta[EndTS].(float64); ok {
			coreAnomalyTS = append(coreAnomalyTS, int64(endTime))
		} else {
			return nil, errors.New("no valid timestamps found and end time not available")
		}
	}

	return coreAnomalyTS, nil
}
