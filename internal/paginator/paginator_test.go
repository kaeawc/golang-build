package paginator

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

type userCursor struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	want := userCursor{ID: "u_42", CreatedAt: time.Unix(1700000000, 0).UTC()}
	s, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if s == "" {
		t.Fatal("Encode returned empty string")
	}

	var got userCursor
	if err := Decode(s, &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.ID != want.ID || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestEncodeIsURLSafe(t *testing.T) {
	// Encode something with characters that base64-std would escape: +, /, =
	cur := struct {
		Bytes []byte `json:"b"`
	}{Bytes: []byte{0xff, 0xfe, 0xfd, 0xfc}}
	s, err := Encode(cur)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, c := range s {
		switch c {
		case '+', '/', '=':
			t.Errorf("encoded cursor contains URL-unsafe char %q: %s", c, s)
		}
	}
}

func TestDecodeRejectsEmptyString(t *testing.T) {
	var v userCursor
	err := Decode("", &v)
	if !errors.Is(err, ErrCursorMalformed) {
		t.Errorf("err = %v, want ErrCursorMalformed", err)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	var v userCursor
	err := Decode("!!!not-base64!!!", &v)
	if !errors.Is(err, ErrCursorMalformed) {
		t.Errorf("err = %v, want ErrCursorMalformed", err)
	}
}

func TestDecodeRejectsVersionMismatch(t *testing.T) {
	bogus := base64.RawURLEncoding.EncodeToString([]byte(`{"v":99,"p":{"id":"x"}}`))
	var v userCursor
	err := Decode(bogus, &v)
	if !errors.Is(err, ErrCursorVersion) {
		t.Errorf("err = %v, want ErrCursorVersion", err)
	}
}

func TestDecodePayloadTypeMismatch(t *testing.T) {
	// Encode a struct, decode into an incompatible target.
	type other struct {
		Nope int `json:"id"` // wrong type for "id"
	}
	s, _ := Encode(userCursor{ID: "u_1"})
	var got other
	err := Decode(s, &got)
	if !errors.Is(err, ErrCursorMalformed) {
		t.Errorf("err = %v, want ErrCursorMalformed wrapped", err)
	}
}

func TestNewPageNoCursorWhenNoMore(t *testing.T) {
	items := []userCursor{{ID: "a"}, {ID: "b"}}
	page := NewPage(items, false, func(c userCursor) string { return c.ID })
	if page.HasMore {
		t.Error("HasMore should be false")
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q, want empty (no more items)", page.NextCursor)
	}
	if len(page.Items) != 2 {
		t.Errorf("Items count = %d", len(page.Items))
	}
}

func TestNewPageEmitsCursorWhenHasMore(t *testing.T) {
	items := []userCursor{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	page := NewPage(items, true, func(c userCursor) userCursor { return c })
	if !page.HasMore {
		t.Error("HasMore should be true")
	}
	if page.NextCursor == "" {
		t.Fatal("NextCursor should be set")
	}

	var got userCursor
	if err := Decode(page.NextCursor, &got); err != nil {
		t.Fatalf("Decode NextCursor: %v", err)
	}
	if got.ID != "c" {
		t.Errorf("decoded cursor ID = %q, want last item's id (c)", got.ID)
	}
}

func TestNewPageNoCursorOnEmpty(t *testing.T) {
	page := NewPage([]userCursor{}, true, func(c userCursor) string { return c.ID })
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q, want empty (no items to encode)", page.NextCursor)
	}
}

func TestNewPagePanicsOnEncodeError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when encodeCursor returns unmarshalable value")
		}
	}()
	items := []userCursor{{ID: "a"}}
	_ = NewPage(items, true, func(userCursor) chan int { return make(chan int) })
}

func TestClampPageSize(t *testing.T) {
	for _, tc := range []struct {
		name string
		size string
		def  int
		maxN int
		want int
	}{
		{"default for empty", "", 25, 100, 25},
		{"default for invalid", "abc", 25, 100, 25},
		{"default for zero", "0", 25, 100, 25},
		{"default for negative", "-5", 25, 100, 25},
		{"clamped to max", "500", 25, 100, 100},
		{"valid value", "50", 25, 100, 50},
		{"def below 1 floored to 1", "", 0, 100, 1},
		{"max below def lifted to def", "", 25, 5, 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampPageSize(tc.size, tc.def, tc.maxN)
			if got != tc.want {
				t.Errorf("ClampPageSize(%q, %d, %d) = %d, want %d", tc.size, tc.def, tc.maxN, got, tc.want)
			}
		})
	}
}

func TestSplitPage(t *testing.T) {
	for _, tc := range []struct {
		name        string
		items       []int
		pageSize    int
		wantItems   []int
		wantHasMore bool
	}{
		{"exact fit", []int{1, 2, 3}, 3, []int{1, 2, 3}, false},
		{"under fit", []int{1, 2}, 3, []int{1, 2}, false},
		{"one over", []int{1, 2, 3, 4}, 3, []int{1, 2, 3}, true},
		{"empty", nil, 3, nil, false},
		{"zero size", []int{1}, 0, []int{1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotItems, gotHasMore := SplitPage(tc.items, tc.pageSize)
			if gotHasMore != tc.wantHasMore {
				t.Errorf("hasMore = %v, want %v", gotHasMore, tc.wantHasMore)
			}
			if len(gotItems) != len(tc.wantItems) {
				t.Fatalf("items len = %d, want %d", len(gotItems), len(tc.wantItems))
			}
			for i := range gotItems {
				if gotItems[i] != tc.wantItems[i] {
					t.Errorf("items[%d] = %d, want %d", i, gotItems[i], tc.wantItems[i])
				}
			}
		})
	}
}
