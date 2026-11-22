package mcpserver

import (
	"math"
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

// FuzzUserURIRoundTrip is the other direction: every URI the server renders
// for a valid ID parses back to that ID, and no other ID is representable.
func FuzzUserURIRoundTrip(f *testing.F) {
	for _, id := range []int64{1, 42, 0, -1, math.MaxInt64, math.MinInt64} {
		f.Add(id, false)
		f.Add(id, true)
	}
	f.Fuzz(func(t *testing.T, id int64, avatar bool) {
		uri := UserURI(id)
		if avatar {
			uri = AvatarURI(id)
		}
		gotID, gotAvatar, err := ParseUserURI(uri)
		if id <= 0 {
			if err == nil {
				t.Fatalf("ParseUserURI(%q) accepted non-positive id %d", uri, gotID)
			}
			return
		}
		if err != nil || gotID != id || gotAvatar != avatar {
			t.Fatalf("ParseUserURI(%q) = %d, %t, %v; want %d, %t", uri, gotID, gotAvatar, err, id, avatar)
		}
	})
}

// BenchmarkParseUserURI is the per-request cost of resolving a templated
// resource read.
func BenchmarkParseUserURI(b *testing.B) {
	for _, bc := range []struct{ name, uri string }{
		{"user", "users://7"},
		{"avatar-maxint", AvatarURI(math.MaxInt64)},
		{"invalid", "users://not-a-number"},
	} {
		b.Run(bc.name, func(b *testing.B) {
			uri := bc.uri
			b.ReportAllocs()
			for b.Loop() {
				_, _, _ = ParseUserURI(uri)
			}
		})
	}
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
