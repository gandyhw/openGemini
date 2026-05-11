// Copyright 2025 openGemini Authors.
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
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/influxdata/influxdb/toml"
)

const (
	DefaultTopoManagerUrl       = ""
	DefaultTopoRequestTimeout   = 5 * time.Second
	DefaultTopoMaxResponseBytes = 64 << 20
	DefaultTopoMaxGraphNodes    = 100000
	DefaultTopoMaxGraphEdges    = 200000
	DefaultTopoMaxUIDSetSize    = 10000
)

type Topo struct {
	TopoManagerUrl   string        `toml:"topo-manager-url"`
	RequestTimeout   toml.Duration `toml:"request-timeout"`
	MaxResponseBytes int64         `toml:"max-response-bytes"`
	MaxGraphNodes    int           `toml:"max-graph-nodes"`
	MaxGraphEdges    int           `toml:"max-graph-edges"`
	MaxUIDSetSize    int           `toml:"max-uid-set-size"`
}

func NewTopo() Topo {
	return Topo{
		TopoManagerUrl:   DefaultTopoManagerUrl,
		RequestTimeout:   toml.Duration(DefaultTopoRequestTimeout),
		MaxResponseBytes: DefaultTopoMaxResponseBytes,
		MaxGraphNodes:    DefaultTopoMaxGraphNodes,
		MaxGraphEdges:    DefaultTopoMaxGraphEdges,
		MaxUIDSetSize:    DefaultTopoMaxUIDSetSize,
	}
}

func (c Topo) Validate() error {
	if c.TopoManagerUrl != "" {
		parsedUrl, err := url.Parse(c.TopoManagerUrl)
		if err != nil {
			return fmt.Errorf("invalid TopoManagerUrl: %s", err)
		}

		if parsedUrl.Scheme != "https" {
			return errors.New("TopoManagerUrl must use https scheme")
		}

		if parsedUrl.Host == "" {
			return errors.New("TopoManagerUrl must contain a valid hostname")
		}
	}
	if c.RequestTimeout < 0 {
		return errors.New("Topo request-timeout must be non-negative")
	}
	if c.MaxResponseBytes < 0 {
		return errors.New("Topo max-response-bytes must be non-negative")
	}
	if c.MaxGraphNodes < 0 {
		return errors.New("Topo max-graph-nodes must be non-negative")
	}
	if c.MaxGraphEdges < 0 {
		return errors.New("Topo max-graph-edges must be non-negative")
	}
	if c.MaxUIDSetSize < 0 {
		return errors.New("Topo max-uid-set-size must be non-negative")
	}
	return nil
}
