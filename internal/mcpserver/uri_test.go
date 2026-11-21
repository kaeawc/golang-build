package mcpserver

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseUserURI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		uri    string
		id     int64
		avatar bool
		ok     bool
	}{
		{"users://1", 1, false, true},
		{"users://42/avatar.png", 42, true, true},
		{"users://9223372036854775807", 9223372036854775807, false, true},
		{"users://9223372036854775808", 0, false, false}, // overflow
		{"users://0", 0, false, false},
		{"users://007", 0, false, false}, // non-canonical
		{"users://-1", 0, false, false},
		{"users://+1", 0, false, false},
		{"users://1/", 0, false, false},
		{"users://", 0, false, false},
		{"users://directory", 0, false, false},
		{"users://1/avatar.png/avatar.png", 0, false, false},
		{"https://1", 0, false, false},
	}
	for _, tt := range tests {
		id, avatar, err := ParseUserURI(tt.uri)
		if (err == nil) != tt.ok || id != tt.id || avatar != tt.avatar {
			t.Errorf("ParseUserURI(%q) = %d, %t, %v", tt.uri, id, avatar, err)
		}
	}
}

// FuzzParseUserURI checks that the strict parser never panics and that
// everything it accepts is canonical: re-rendering the parsed ID yields the
// exact input. That property is what keeps resource subscriptions (keyed by
// URI) from being split across aliases such as users://007.
func FuzzParseUserURI(f *testing.F) {
	for _, s := range []string{"users://1", "users://12/avatar.png", "users://0", "users://007", "users://-5", "users://1e3", "users://", "x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, uri string) {
		id, avatar, err := ParseUserURI(uri)
		if err != nil {
			return
		}
		if id <= 0 {
			t.Fatalf("accepted non-positive id %d from %q", id, uri)
		}
		want := UserURI(id)
		if avatar {
			want = AvatarURI(id)
		}
		if want != uri {
			t.Fatalf("non-canonical accept: %q parsed as %q", uri, want)
		}
		if strings.ContainsAny(uri, " \t\n?#") {
			t.Fatalf("accepted URI with separators: %q", uri)
		}
	})
}

// BenchmarkCallToolGetUser measures a full typed tool round trip (schema
// validation, handler, structured output) over in-memory transports.
func BenchmarkCallToolGetUser(b *testing.B) {
	h := newHarness(b, latest, nil)
	params := &mcp.CallToolParams{Name: ToolGetUser, Arguments: map[string]any{"id": 7}}
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		res, err := h.cs.CallTool(ctx, params)
		if err != nil || res.IsError {
			b.Fatalf("%v %+v", err, res)
		}
	}
}
