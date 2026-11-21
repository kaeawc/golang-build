package mcpserver

import (
	"context"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxCompletions is the spec's cap on values per completion response.
const maxCompletions = 100

// complete implements completion/complete for prompt arguments and
// resource-template parameters. Unknown references yield an empty result
// rather than an error, so clients can probe freely.
func (a *app) complete(ctx context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	ref, arg := req.Params.Ref, req.Params.Argument
	key := completionKey{ref: ref.Type + ":" + ref.Name + ref.URI, arg: arg.Name}
	fn, ok := a.completers()[key]
	if !ok {
		return completion(nil), nil
	}
	values, err := fn(ctx, arg.Value)
	if err != nil {
		return nil, err
	}
	return completion(values), nil
}

// completionKey identifies a completable argument: ref is
// "<ref type>:<prompt name or template URI>".
type completionKey struct{ ref, arg string }

type completer func(ctx context.Context, prefix string) ([]string, error)

func (a *app) completers() map[completionKey]completer {
	static := func(options []string) completer {
		return func(_ context.Context, prefix string) ([]string, error) { return filterPrefix(options, prefix), nil }
	}
	return map[completionKey]completer{
		{"ref/prompt:" + PromptSummarizeUser, "user_id"}: a.completeUserIDs,
		{"ref/prompt:" + PromptSummarizeUser, "style"}:   static(summaryStyles),
		{"ref/prompt:" + PromptOnboarding, "name"}:       a.completeNames,
		{"ref/prompt:" + PromptOnboarding, "tone"}:       static(emailTones),
		{"ref/resource:" + URITemplateUser, "id"}:        a.completeUserIDs,
		{"ref/resource:" + URITemplateAvatar, "id"}:      a.completeUserIDs,
	}
}

func completion(values []string) *mcp.CompleteResult {
	total := len(values)
	if values == nil {
		values = []string{}
	}
	if len(values) > maxCompletions {
		values = values[:maxCompletions]
	}
	return &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{
		Values: values, Total: total, HasMore: total > len(values),
	}}
}

func (a *app) completeUserIDs(ctx context.Context, prefix string) ([]string, error) {
	users, err := a.allUsers(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, u := range users {
		if id := strconv.FormatInt(u.ID, 10); strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (a *app) completeNames(ctx context.Context, prefix string) ([]string, error) {
	users, err := a.allUsers(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(users))
	for i, u := range users {
		names[i] = u.Name
	}
	return filterPrefix(names, prefix), nil
}

func filterPrefix(options []string, prefix string) []string {
	var out []string
	for _, o := range options {
		if strings.HasPrefix(strings.ToLower(o), strings.ToLower(prefix)) {
			out = append(out, o)
		}
	}
	return out
}
