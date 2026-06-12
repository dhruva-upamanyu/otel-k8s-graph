// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GraphClient is a thin HTTP client over the graph-read REST API. Each
// method returns the raw JSON decoded into a generic map so the MCP tool
// handlers can forward it to the LLM without committing to specific response
// schemas. The MCP never talks to Redis directly — only to this REST API.
type GraphClient struct {
	baseURL string
	http    *http.Client
}

func NewGraphClient(baseURL string) *GraphClient {
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	return &GraphClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *GraphClient) get(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB cap
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> %d: %s", u, resp.StatusCode, string(body))
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode %s: %w (body: %s)", u, err, snippet(body))
	}
	return out, nil
}

func (c *GraphClient) Search(ctx context.Context, query, kind string, limit int) (map[string]any, error) {
	q := url.Values{"q": {query}}
	if kind != "" {
		q.Set("kind", kind)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.get(ctx, "/search", q)
}

func (c *GraphClient) GetEntity(ctx context.Context, id string) (map[string]any, error) {
	// id may contain "/" for namespace-qualified IDs; pass unescaped — the
	// server uses a wildcard path param.
	return c.get(ctx, "/entity/"+id, nil)
}

func (c *GraphClient) ListEntities(ctx context.Context, kind string) (map[string]any, error) {
	return c.get(ctx, "/entities", url.Values{"kind": {kind}})
}

func (c *GraphClient) GetSubgraph(ctx context.Context, id string, maxDepth int) (map[string]any, error) {
	q := url.Values{}
	if maxDepth > 0 {
		q.Set("max_depth", strconv.Itoa(maxDepth))
	}
	return c.get(ctx, "/subgraph/"+id, q)
}

func snippet(b []byte) string {
	const max = 256
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
