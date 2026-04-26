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

import "sync/atomic"

const (
	defaultTopoMaxGraphNodes = 100000
	defaultTopoMaxGraphEdges = 200000
	defaultTopoMaxUIDSetSize = 10000
)

var (
	topoMaxGraphNodes atomic.Int64
	topoMaxGraphEdges atomic.Int64
	topoMaxUIDSetSize atomic.Int64
)

func init() {
	SetTopoLimits(defaultTopoMaxGraphNodes, defaultTopoMaxGraphEdges, defaultTopoMaxUIDSetSize)
}

func SetTopoLimits(maxGraphNodes, maxGraphEdges, maxUIDSetSize int) {
	topoMaxGraphNodes.Store(int64(maxGraphNodes))
	topoMaxGraphEdges.Store(int64(maxGraphEdges))
	topoMaxUIDSetSize.Store(int64(maxUIDSetSize))
}

func getTopoMaxGraphNodes() int {
	return int(topoMaxGraphNodes.Load())
}

func getTopoMaxGraphEdges() int {
	return int(topoMaxGraphEdges.Load())
}

func getTopoMaxUIDSetSize() int {
	return int(topoMaxUIDSetSize.Load())
}
