package paginator

import (
	"strconv"
	"testing"
	"unicode/utf8"
)

type fuzzCursor struct {
	ID string `json:"id"`
	N  int64  `json:"n"`
}

// FuzzCursorRoundTrip checks Decode(Encode(c)) == c and that the encoded
// form never needs further escaping in a query string.
func FuzzCursorRoundTrip(f *testing.F) {
	f.Add("u_1", int64(0))
	f.Add("", int64(-1))
	f.Add(`"quoted" & <tagged>`, int64(1<<62))
	f.Fuzz(func(t *testing.T, id string, n int64) {
		// encoding/json replaces invalid UTF-8 with U+FFFD, so such IDs
		// cannot round-trip; real cursors are built from valid strings.
		if !utf8.ValidString(id) {
			return
		}
		in := fuzzCursor{ID: id, N: n}
		s, err := Encode(in)
		if err != nil {
			t.Fatalf("Encode(%+v): %v", in, err)
		}
		for _, r := range s {
			if !isURLSafe(r) {
				t.Fatalf("Encode(%+v) = %q contains %q", in, s, r)
			}
		}
		var out fuzzCursor
		if err := Decode(s, &out); err != nil {
			t.Fatalf("Decode(Encode(%+v)): %v", in, err)
		}
		if out != in {
			t.Fatalf("round trip: got %+v, want %+v", out, in)
		}
	})
}

// FuzzDecode feeds arbitrary client-supplied cursors to Decode. It must
// never panic, and anything it accepts must re-encode to a cursor that
// decodes to the same value.
func FuzzDecode(f *testing.F) {
	valid, err := Encode(fuzzCursor{ID: "u_1", N: 7})
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{valid, "", "!!!", "e30", "eyJ2IjoyLCJwIjp7fX0", "eyJ2IjoxLCJwIjpudWxsfQ"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var first fuzzCursor
		if err := Decode(s, &first); err != nil {
			return
		}
		again, err := Encode(first)
		if err != nil {
			t.Fatalf("re-Encode(%+v): %v", first, err)
		}
		var second fuzzCursor
		if err := Decode(again, &second); err != nil || second != first {
			t.Fatalf("%q decoded to %+v, re-encoded %q decoded to %+v (%v)", s, first, again, second, err)
		}
	})
}

// FuzzClampPageSize checks the result is always a usable page size in
// [1, max]: in-range requests pass through, oversized ones are capped, and
// anything else falls back to the default (after ClampPageSize's own
// normalization of def and max).
func FuzzClampPageSize(f *testing.F) {
	f.Add("10", 25, 100)
	f.Add("", 0, 0)
	f.Add("-5", 25, 10)
	f.Add("99999999999999999999", 1, 50)
	f.Fuzz(func(t *testing.T, size string, def, maxN int) {
		got := ClampPageSize(size, def, maxN)
		normDef := max(def, 1)
		normMax := max(maxN, normDef)
		want := normDef
		if n, err := strconv.Atoi(size); err == nil && n >= 1 {
			want = min(n, normMax)
		}
		if got != want || got < 1 || got > normMax {
			t.Fatalf("ClampPageSize(%q, %d, %d) = %d, want %d", size, def, maxN, got, want)
		}
	})
}

func isURLSafe(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}
