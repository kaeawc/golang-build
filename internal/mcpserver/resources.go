package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (a *app) registerResources() {
	a.server.AddResource(&mcp.Resource{
		URI:         URIDirectory,
		Name:        "directory",
		Title:       "Users directory",
		Description: "Markdown list of every user. Subscribe to be notified when users are created or deleted.",
		MIMEType:    "text/markdown",
		Annotations: &mcp.Annotations{Audience: []mcp.Role{"user", "assistant"}, Priority: 0.8},
	}, a.readDirectory)

	a.server.AddResource(&mcp.Resource{
		URI:         URIServerInfo,
		Name:        "server-info",
		Title:       "Server information",
		Description: "Build version and supported MCP protocol versions.",
		MIMEType:    "application/json",
	}, a.readServerInfo)

	logo := identiconPNG(ServerName)
	a.server.AddResource(&mcp.Resource{
		URI:         URILogo,
		Name:        "logo",
		Title:       "Server logo",
		Description: "Binary (blob) resource example.",
		MIMEType:    "image/png",
		Size:        int64(len(logo)),
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: URILogo, MIMEType: "image/png", Blob: logo}}}, nil
	})

	a.server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: URITemplateUser,
		Name:        "user",
		Title:       "User",
		Description: "A single user as JSON. Supports subscriptions and completion of {id}.",
		MIMEType:    "application/json",
	}, a.readUser)

	a.server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: URITemplateAvatar,
		Name:        "avatar",
		Title:       "User avatar",
		Description: "Deterministic PNG identicon for a user (binary template resource).",
		MIMEType:    "image/png",
	}, a.readUser)
}

func (a *app) readDirectory(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	users, err := a.allUsers(ctx)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Users (%d)\n\n", len(users))
	for _, u := range users {
		fmt.Fprintf(&b, "- [%s](%s)\n", u.Name, UserURI(u.ID))
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: URIDirectory, MIMEType: "text/markdown", Text: b.String()}}}, nil
}

func (a *app) readServerInfo(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	body, err := json.MarshalIndent(map[string]any{
		"name":             ServerName,
		"version":          a.Version,
		"protocolVersions": ProtocolVersions,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: URIServerInfo, MIMEType: "application/json", Text: string(body)}}}, nil
}

// readUser serves both user templates. The SDK has already matched the URI
// against one of them; ParseUserURI does the strict validation.
func (a *app) readUser(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	id, avatar, err := ParseUserURI(uri)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	u, err := a.Store.Get(ctx, id)
	if isNotFound(err) {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if err != nil {
		return nil, err
	}
	if avatar {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "image/png", Blob: identiconPNG(u.Name)}}}, nil
	}
	body, err := json.Marshal(u)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(body)}}}, nil
}
