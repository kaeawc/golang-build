package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Prompt names.
const (
	PromptSummarizeUser = "summarize_user"
	PromptOnboarding    = "onboarding_email"
)

var (
	summaryStyles = []string{"brief", "detailed", "bullet-points"}
	emailTones    = []string{"formal", "friendly", "playful"}
)

func (a *app) registerPrompts() {
	a.server.AddPrompt(&mcp.Prompt{
		Name:        PromptSummarizeUser,
		Title:       "Summarize a user",
		Description: "Summarize one user. Embeds the users://{id} resource so the model sees the record directly.",
		Arguments: []*mcp.PromptArgument{
			{Name: "user_id", Title: "User ID", Description: "ID of the user to summarize (completable)", Required: true},
			{Name: "style", Title: "Style", Description: "brief, detailed, or bullet-points (completable)"},
		},
	}, a.summarizeUser)

	a.server.AddPrompt(&mcp.Prompt{
		Name:        PromptOnboarding,
		Title:       "Draft onboarding email",
		Description: "Multi-turn prompt that drafts and then refines a welcome email.",
		Arguments: []*mcp.PromptArgument{
			{Name: "name", Title: "Name", Description: "recipient's name (completable from the directory)", Required: true},
			{Name: "tone", Title: "Tone", Description: "formal, friendly, or playful (completable)"},
		},
	}, a.onboardingEmail)
}

func (a *app) summarizeUser(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := req.Params.Arguments
	id, err := strconv.ParseInt(args["user_id"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("user_id must be an integer: %w", err)
	}
	style := args["style"]
	if style == "" {
		style = summaryStyles[0]
	}
	if !slices.Contains(summaryStyles, style) {
		return nil, fmt.Errorf("style must be one of %v", summaryStyles)
	}
	u, err := a.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(u)
	if err != nil {
		return nil, err
	}
	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Summarize user #%d in %s style", u.ID, style),
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI: UserURI(u.ID), MIMEType: "application/json", Text: string(body),
			}}},
			{Role: "user", Content: &mcp.TextContent{Text: fmt.Sprintf(
				"Write a %s summary of the user record above. Mention their ID.", style)}},
		},
	}, nil
}

func (a *app) onboardingEmail(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	name := req.Params.Arguments["name"]
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	tone := req.Params.Arguments["tone"]
	if tone == "" {
		tone = "friendly"
	}
	if !slices.Contains(emailTones, tone) {
		return nil, fmt.Errorf("tone must be one of %v", emailTones)
	}
	return &mcp.GetPromptResult{
		Description: "Onboarding email for " + name,
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: fmt.Sprintf("Draft a %s welcome email for %s, who just joined the golang-build project.", tone, name)}},
			{Role: "assistant", Content: &mcp.TextContent{Text: fmt.Sprintf("Subject: Welcome aboard, %s!\n\nHi %s, ...", name, name)}},
			{Role: "user", Content: &mcp.TextContent{Text: "Good start. Expand the body to three short paragraphs and add a link to the README."}},
		},
	}, nil
}
