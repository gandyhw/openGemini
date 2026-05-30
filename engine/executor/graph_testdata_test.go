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

// MockGetTimeGraph returns a fixed topology payload used by the executor
// package's graph tests. It is exported so that the external executor_test
// package (e.g. graph_transform_test.go) can inject it via TopoDataFetcher.
func MockGetTimeGraph() string {
	jsonNodeData := `{
    "data": {
		  "resultUid": "eadee230-db0a-40df-887c-9958e1d872df",
		  "metadata": {
			"region": "global",
			"timestamp": "1753080098056",
			"topokeys": ["source0", "source1"]
		  },
		  "graph": {
			"vertex": [
			  {
				"uid": "vm1",
				"metadata": {
				  "kind": "Node",
				  "region": "cn-north-1",
				  "tags": { "namedb": "db1", "prop1": "value1" }
				}
			  },
			  {
				"uid": "vm2",
				"metadata": {
				  "kind": "Node",
				  "region": "cn-north-1",
				  "tags": { "namedb": "db2", "propvm2": "valuevm2" }
				}
			  },
			  {
				"uid": "vm3",
				"metadata": {
				  "kind": "Node",
				  "region": "cn-east-1",
				  "tags": { "namedb": "db3", "namedbtest": "db3test" }
				}
			  },
			  {
				"uid": "vm4",
				"metadata": {
				  "kind": "Node",
				  "region": "cn-east-2",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "vm5",
				"metadata": {
				  "kind": "Node",
				  "region": "cn-south-1",
				  "tags": { "namedb": "db5" }
				}
			  },
			  {
				"uid": "ELB",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-south-2",
				  "tags": { "1": "1", "namedb": "db3" }
				}
			  },
			  {
				"uid": "Nginx-ingress1",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-south-3",
				  "tags": { "namespace": "kube-system", "propingress1": "valueingress1" }
				}
			  },
			  {
				"uid": "Nginx-ingress2",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-east-3",
				  "tags": { "1": "1", "namedb": "db3" }
				}
			  },
			  {
				"uid": "Service1",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-east-1",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Service2",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-east-2",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Service3",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-east-2",
				  "tags": { "k1": "1", "namepod": "podService3" }
				}
			  },
			  {
				"uid": "Service4",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-west-2",
				  "tags": { "1": "1", "namepod": "podService6" }
				}
			  },
			  {
				"uid": "rds1",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-west-2",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "rds2",
				"metadata": {
				  "kind": "Pod",
				  "region": "cn-west-2",
				  "tags": { "proprds2": "valuerds2", "namepod": "pod-system" }
				}
			  }
			],
			"edges": [
			  {
				"uid": "ELB_source0::::vm1_source0",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source0",
				  "sourceUid": "ELB",
				  "targetTopokey": "source0",
				  "targetUid": "vm1",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Nginx-ingress1_source1::::vm2_source1",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source1",
				  "sourceUid": "Nginx-ingress1",
				  "targetTopokey": "source1",
				  "targetUid": "vm2",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Nginx-ingress2_source0::::vm3_source1",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source0",
				  "sourceUid": "Nginx-ingress2",
				  "targetTopokey": "source1",
				  "targetUid": "vm3",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Service1_source0::::vm2_source1",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source0",
				  "sourceUid": "Service1",
				  "targetTopokey": "source1",
				  "targetUid": "vm2",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Service2_source0::::vm2_source1",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source0",
				  "sourceUid": "Service2",
				  "targetTopokey": "source1",
				  "targetUid": "vm2",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Service3_source0::::vm3_source1",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source0",
				  "sourceUid": "Service3",
				  "targetTopokey": "source1",
				  "targetUid": "vm3",
				  "tags": { "1": "1", "edgeprop": "edge-com2" }
				}
			  },
			  {
				"uid": "Service4_source0::::vm3_source1",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source0",
				  "sourceUid": "Service4",
				  "targetTopokey": "source1",
				  "targetUid": "vm3",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "rds1_source0::::vm4_source1",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source0",
				  "sourceUid": "rds1",
				  "targetTopokey": "source1",
				  "targetUid": "vm4",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "rds2_source0::::vm5_source1",
				"metadata": {
				  "kind": "LOCATE",
				  "sourceTopokey": "source0",
				  "sourceUid": "rds2",
				  "targetTopokey": "source1",
				  "targetUid": "vm5",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "ELB_source0::::Nginx-ingress1_source0",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source0",
				  "sourceUid": "ELB",
				  "targetTopokey": "source0",
				  "targetUid": "Nginx-ingress1",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "ELB_source1::::Nginx-ingress2_source1",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source1",
				  "sourceUid": "ELB",
				  "targetTopokey": "source1",
				  "targetUid": "Nginx-ingress2",
				  "tags": { "1": "1", "edgeprop": "edge-com0" }
				}
			  },
			  {
				"uid": "Nginx-ingress1_source0::::Service1_source1",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source0",
				  "sourceUid": "Nginx-ingress1",
				  "targetTopokey": "source1",
				  "targetUid": "Service1",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Nginx-ingress1_source0::::Service2_source1",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source0",
				  "sourceUid": "Nginx-ingress1",
				  "targetTopokey": "source1",
				  "targetUid": "Service2",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Nginx-ingress2_source1::::Service3_source0",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source1",
				  "sourceUid": "Nginx-ingress2",
				  "targetTopokey": "source0",
				  "targetUid": "Service3",
				  "tags": { "edgeprop": "edge-com0", "1": "1" }
				}
			  },
			  {
				"uid": "Nginx-ingress2_source0::::Service4_source0",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source0",
				  "sourceUid": "Nginx-ingress2",
				  "targetTopokey": "source0",
				  "targetUid": "Service4",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Service1_source0::::rds1_source1",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source0",
				  "sourceUid": "Service1",
				  "targetTopokey": "source1",
				  "targetUid": "rds1",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Service2_source0::::rds1_source1",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source0",
				  "sourceUid": "Service2",
				  "targetTopokey": "source1",
				  "targetUid": "rds1",
				  "tags": { "1": "1" }
				}
			  },
			  {
				"uid": "Service3_source0::::rds2_source1",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source0",
				  "sourceUid": "Service3",
				  "targetTopokey": "source1",
				  "targetUid": "rds2",
				  "tags": { "1": "1", "edgeprop": "edge-com1" }
				}
			  },
			  {
				"uid": "Service4_source1::::rds2_source1",
				"metadata": {
				  "kind": "communication",
				  "sourceTopokey": "source1",
				  "sourceUid": "Service4",
				  "targetTopokey": "source1",
				  "targetUid": "rds2",
				  "tags": { "1": "1", "edgeprop": "edge-com2" }
				}
			  }
			]
		  }
      }
   }`
	return jsonNodeData
}
