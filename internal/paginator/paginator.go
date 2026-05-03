// Package paginator provides cursor-based pagination for list endpoints.
//
// Cursors are opaque base64-URL-encoded JSON envelopes with a version
// field, so the cursor format can evolve without silently breaking
// clients (a stale cursor with a wrong version is rejected explicitly
// instead of being decoded into garbage).
//
//	type userCursor struct{ ID string; CreatedAt time.Time }
//
//	func ListUsers(w http.ResponseWriter, r *http.Request) {
//	    var cur userCursor
//	    if c := r.URL.Query().Get("cursor"); c != "" {
//	        if err := paginator.Decode(c, &cur); err != nil {
//	            jsonresp.WriteError(w, r, 400, "bad_cursor", err.Error())
//	            return
//	        }
//	    }
//	    pageSize := paginator.ClampPageSize(r.URL.Query().Get("pageSize"), 25, 100)
//
//	    users, hasMore := db.ListUsersAfter(cur, pageSize+1)
//	    page := paginator.NewPage(users, pageSize, hasMore, func(last User) userCursor {
//	        return userCursor{ID: last.ID, CreatedAt: last.CreatedAt}
//	    })
//	    jsonresp.Write(w, 200, page)
//	}
package paginator

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// CursorVersion is embedded in every encoded cursor. Bump when the
// canonical wire format changes so old cursors are rejected explicitly.
const CursorVersion = 1

// Page is the standard list-endpoint response envelope.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
}

// NewPage builds a Page given items and a hasMore flag from the data
// source. If hasMore is true and items is non-empty, encodeCursor is
// called on the last item to produce nextCursor.
//
// Callers typically fetch pageSize+1 from the data source then pass
// the result to SplitPage to derive (items, hasMore).
//
// Panics if encodeCursor returns a value that fails JSON marshaling
// (channels, funcs, cyclic structures). That's a programmer error —
// silently dropping the cursor would strand the client.
func NewPage[T, C any](items []T, hasMore bool, encodeCursor func(T) C) Page[T] {
	out := Page[T]{Items: items, HasMore: hasMore}
	if !hasMore || len(items) == 0 || encodeCursor == nil {
		return out
	}
	cursor := encodeCursor(items[len(items)-1])
	s, err := Encode(cursor)
	if err != nil {
		panic(fmt.Sprintf("paginator.NewPage: encodeCursor produced unmarshalable value: %v", err))
	}
	out.NextCursor = s
	return out
}

// envelope is the on-wire cursor format. Version isolates breaking
// changes; Payload is the caller's typed cursor as JSON.
type envelope struct {
	V int             `json:"v"`
	P json.RawMessage `json:"p"`
}

// Encode serializes any JSON-marshalable value as a base64-URL cursor
// string. The returned string contains no padding (URLEncoding without
// padding) so it's safe to pass via URL query strings without further
// encoding.
func Encode(v any) (string, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("paginator: marshal payload: %w", err)
	}
	env, err := json.Marshal(envelope{V: CursorVersion, P: payload})
	if err != nil {
		return "", fmt.Errorf("paginator: marshal envelope: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(env), nil
}

// ErrCursorVersion is returned by Decode when the cursor's version
// doesn't match CursorVersion (e.g. an old cursor after a server bump).
var ErrCursorVersion = errors.New("paginator: cursor version mismatch")

// ErrCursorMalformed is returned when the cursor isn't a valid base64
// envelope.
var ErrCursorMalformed = errors.New("paginator: cursor malformed")

// Decode reads a cursor produced by Encode and unmarshals the payload
// into v.
func Decode(s string, v any) error {
	if s == "" {
		return ErrCursorMalformed
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCursorMalformed, err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("%w: %w", ErrCursorMalformed, err)
	}
	if env.V != CursorVersion {
		return fmt.Errorf("%w: got %d, want %d (cursor was issued by an older or newer build)", ErrCursorVersion, env.V, CursorVersion)
	}
	if err := json.Unmarshal(env.P, v); err != nil {
		return fmt.Errorf("%w: payload: %w", ErrCursorMalformed, err)
	}
	return nil
}

// ClampPageSize parses size (query-string value), defaults to def if
// missing or invalid, and caps the result at maxN. Useful for endpoints
// that accept ?pageSize= queries.
func ClampPageSize(size string, def, maxN int) int {
	if def < 1 {
		def = 1
	}
	if maxN < def {
		maxN = def
	}
	if size == "" {
		return def
	}
	n, err := strconv.Atoi(size)
	if err != nil || n < 1 {
		return def
	}
	if n > maxN {
		return maxN
	}
	return n
}

// SplitPage trims a fetch-N+1 result down to its first pageSize items
// and reports whether there are more. Callers fetching pageSize+1 from
// the data source can call this to derive the items+hasMore inputs to
// NewPage in one step.
//
//	rows, _ := db.Query(..., pageSize+1)
//	items, hasMore := paginator.SplitPage(rows, pageSize)
//	page := paginator.NewPage(items, pageSize, hasMore, ...)
func SplitPage[T any](items []T, pageSize int) (page []T, hasMore bool) {
	if pageSize < 1 || len(items) <= pageSize {
		return items, false
	}
	return items[:pageSize], true
}
