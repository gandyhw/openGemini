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

package config

import (
	"testing"
	"time"

	"github.com/influxdata/influxdb/toml"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestTopoConfig(t *testing.T) {
	convey.Convey("common url", t, func() {
		TopoTest := &Topo{
			TopoManagerUrl: "https://127.0.0.1:60892/v1/topology",
		}
		err := TopoTest.Validate()
		assert.Equal(t, nil, err)
	})

	convey.Convey("protocol header is not valid", t, func() {
		TopoTest := &Topo{
			TopoManagerUrl: "http://127.0.0.1:60892/v1/topology",
		}
		err := TopoTest.Validate()
		assert.Equal(t, "TopoManagerUrl must use https scheme", err.Error())
	})

	convey.Convey("TopoManagerUrl format is not vaild", t, func() {
		TopoTest := &Topo{
			TopoManagerUrl: "127.0.0.1:60892/v1/topology",
		}
		err := TopoTest.Validate()
		assert.Equal(t, "invalid TopoManagerUrl: parse \"127.0.0.1:60892/v1/topology\": first path segment in URL cannot contain colon", err.Error())
	})

	convey.Convey("hostname is not valid", t, func() {
		TopoTest := &Topo{
			TopoManagerUrl: "https://",
		}
		err := TopoTest.Validate()
		assert.Equal(t, "TopoManagerUrl must contain a valid hostname", err.Error())
	})

	convey.Convey("default limits are valid", t, func() {
		topo := NewTopo()
		assert.Equal(t, toml.Duration(5*time.Second), topo.RequestTimeout)
		assert.Equal(t, int64(64<<20), topo.MaxResponseBytes)
		assert.Equal(t, nil, topo.Validate())
	})

	convey.Convey("negative limit is invalid", t, func() {
		topo := NewTopo()
		topo.MaxUIDSetSize = -1
		err := topo.Validate()
		assert.Equal(t, "Topo max-uid-set-size must be non-negative", err.Error())
	})

}
