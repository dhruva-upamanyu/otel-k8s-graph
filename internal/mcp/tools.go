// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Input/output types are deliberately permissive ("any" for outputs) so the
// LLM gets the graph REST API's JSON shape directly. Input structs use
// jsonschema struct tags to give the model crisp parameter docs.

type SearchInput struct {
	Query string `json:"query" jsonschema:"required keyword to match (case-insensitive substring) against entity IDs, names, metadata values, and edge actions"`
	Kind  string `json:"kind,omitempty" jsonschema:"optional entity kind filter: namespace | node | zone | region | deployment | pod | container | endpoint | topic | database. Omit to search all kinds."`
	Limit int    `json:"limit,omitempty" jsonschema:"max matches to return; default 20, hard cap 500"`
}

type GetEntityInput struct {
	ID string `json:"id" jsonschema:"required exact entity ID, e.g. 'container:default/auth-7f8/app' or 'endpoint:auth-service/POST/api/auth/validate'"`
}

type ListEntitiesInput struct {
	Kind string `json:"kind" jsonschema:"required entity kind to enumerate: namespace | node | zone | region | deployment | pod | container | endpoint | topic | database"`
}

type GetSubgraphInput struct {
	ID       string `json:"id" jsonschema:"required exact entity ID to start BFS from"`
	MaxDepth int    `json:"max_depth,omitempty" jsonschema:"BFS depth limit; default 2. Larger values fan out quickly — keep small unless you need the whole reachable set."`
}

func registerTools(srv *mcp.Server, c *GraphClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search",
		Description: "Find entities by keyword across IDs, names, metadata values, and edge actions. Best first move when the user names something approximately (e.g. 'what calls /validate?', 'all java services').",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, any, error) {
		out, err := c.Search(ctx, in.Query, in.Kind, in.Limit)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_entity",
		Description: "Full detail for one entity by exact ID: kind, name, metadata (image, language, etc.), and all outbound edges with actions. Use after 'search' narrows things down.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetEntityInput) (*mcp.CallToolResult, any, error) {
		out, err := c.GetEntity(ctx, in.ID)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_entities",
		Description: "Enumerate every entity of a given kind. Use to answer 'walk every endpoint' or 'show all databases' style questions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListEntitiesInput) (*mcp.CallToolResult, any, error) {
		out, err := c.ListEntities(ctx, in.Kind)
		return nil, out, err
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_subgraph",
		Description: "Return every entity reachable from the root via BFS within max_depth hops. Use to find blast radius (downstream impact) or upstream call neighborhood. Keep max_depth small (default 2) unless you need the entire reachable set.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetSubgraphInput) (*mcp.CallToolResult, any, error) {
		if in.MaxDepth == 0 {
			in.MaxDepth = 2
		}
		out, err := c.GetSubgraph(ctx, in.ID, in.MaxDepth)
		return nil, out, err
	})
}
