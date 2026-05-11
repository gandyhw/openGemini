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

package util

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Config struct {
	Path             string
	RequestTimeout   time.Duration
	MaxResponseBytes int64
}

type Client struct {
	Conf Config
}

var path string
var topoOptions = Config{}

type Param struct {
	ParamName  string
	ParamValue string
}

func SetTopoManagerUrl(topoManagerUrl string) {
	SetTopoManagerOptions(topoManagerUrl, 0, 0)
}

func SetTopoManagerOptions(topoManagerUrl string, requestTimeout time.Duration, maxResponseBytes int64) {
	path = topoManagerUrl
	topoOptions = Config{
		Path:             topoManagerUrl,
		RequestTimeout:   requestTimeout,
		MaxResponseBytes: maxResponseBytes,
	}
}

var HttpsClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	},
}

func GetClientConf() *Client {
	return &Client{
		Conf: topoOptions,
	}
}

func (c *Client) PushURL(param Param, startTime, endTime string) (string, error) {
	query := url.Values{}
	switch {
	case startTime == endTime && endTime != "":
		query.Add("time", startTime)
	case startTime != "" && endTime != "":
		query.Add("startTime", startTime)
		query.Add("endTime", endTime)
	default:
	}

	if param.ParamName != "" && param.ParamValue != "" {
		if param.ParamName != "applicationId" {
			return "", errors.New("invalid param name")
		}
		query.Add(param.ParamName, param.ParamValue)
	}
	u, err := url.Parse(c.Conf.Path)
	if err != nil {
		return "", fmt.Errorf("invalid base path: %s", c.Conf.Path)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (c *Client) SendGetRequest(httpClient *http.Client, param Param, startTime string, endTime string) (string, error) {
	return c.SendGetRequestContext(context.Background(), httpClient, param, startTime, endTime)
}

func (c *Client) SendGetRequestContext(ctx context.Context, httpClient *http.Client, param Param, startTime string, endTime string) (string, error) {
	pushURL, err := c.PushURL(param, startTime, endTime)
	if err != nil {
		return "", err
	}
	// todo: Implement a feature to periodically obtain the X-Auth-Token
	reqCtx := ctx
	cancel := func() {}
	if c.Conf.RequestTimeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, c.Conf.RequestTimeout)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "GET", pushURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Auth-Token", "")
	req.Header.Add("Content-Type", "application/json")

	writeResp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer writeResp.Body.Close()

	reader := io.Reader(writeResp.Body)
	if c.Conf.MaxResponseBytes > 0 {
		reader = io.LimitReader(writeResp.Body, c.Conf.MaxResponseBytes+1)
	}
	body, err := io.ReadAll(reader)
	if writeResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http get request failed, status code: %d, reason: %s, response body: %s", writeResp.StatusCode, writeResp.Status, string(body))
	}

	if err != nil {
		return "", err
	}
	if c.Conf.MaxResponseBytes > 0 && int64(len(body)) > c.Conf.MaxResponseBytes {
		return "", fmt.Errorf("http get request failed, response body exceeds max-response-bytes: %d", c.Conf.MaxResponseBytes)
	}

	return string(body), nil
}

type TopoProvider interface {
	Fetch(ctx context.Context, param Param, startTime string, endTime string) (string, error)
	CacheKey(param Param, startTime string, endTime string) (string, error)
	Configured() bool
}

type HTTPTopoProvider struct {
	client     *Client
	httpClient *http.Client
}

func NewHTTPTopoProvider(client *Client, httpClient *http.Client) *HTTPTopoProvider {
	return &HTTPTopoProvider{client: client, httpClient: httpClient}
}

func (p *HTTPTopoProvider) Configured() bool {
	return p != nil && p.client != nil && p.client.Conf.Path != ""
}

func (p *HTTPTopoProvider) Fetch(ctx context.Context, param Param, startTime string, endTime string) (string, error) {
	if !p.Configured() {
		return "", errors.New("topo manager url is empty")
	}
	return p.client.SendGetRequestContext(ctx, p.httpClient, param, startTime, endTime)
}

func (p *HTTPTopoProvider) CacheKey(param Param, startTime string, endTime string) (string, error) {
	if !p.Configured() {
		return "", errors.New("topo manager url is empty")
	}
	return p.client.PushURL(param, startTime, endTime)
}

type topoQueryCacheKey struct{}

type TopoQueryCache struct {
	mu      sync.Mutex
	entries map[string]*topoQueryCacheEntry
}

type topoQueryCacheEntry struct {
	ready chan struct{}
	data  string
	err   error
}

func NewTopoQueryCache() *TopoQueryCache {
	return &TopoQueryCache{entries: make(map[string]*topoQueryCacheEntry)}
}

func WithTopoQueryCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(topoQueryCacheKey{}).(*TopoQueryCache); ok {
		return ctx
	}
	return context.WithValue(ctx, topoQueryCacheKey{}, NewTopoQueryCache())
}

func TopoCacheFromContext(ctx context.Context) *TopoQueryCache {
	cache, _ := ctx.Value(topoQueryCacheKey{}).(*TopoQueryCache)
	return cache
}

func FetchTopo(ctx context.Context, provider TopoProvider, param Param, startTime string, endTime string) (string, error) {
	cache := TopoCacheFromContext(ctx)
	if cache == nil {
		return provider.Fetch(ctx, param, startTime, endTime)
	}
	return cache.Fetch(ctx, provider, param, startTime, endTime)
}

func (c *TopoQueryCache) Fetch(ctx context.Context, provider TopoProvider, param Param, startTime string, endTime string) (string, error) {
	key, err := provider.CacheKey(param, startTime, endTime)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	entry, ok := c.entries[key]
	if ok {
		c.mu.Unlock()
		select {
		case <-entry.ready:
			return entry.data, entry.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	entry = &topoQueryCacheEntry{ready: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()

	entry.data, entry.err = provider.Fetch(ctx, param, startTime, endTime)
	close(entry.ready)
	return entry.data, entry.err
}
