package env

import (
	"testing"
)

func TestOS_LookupReadsProcessEnv(t *testing.T) {
	t.Setenv("KRIT_ENV_TEST_PRESENT", "yes")
	if v, ok := (OS{}).Lookup("KRIT_ENV_TEST_PRESENT"); !ok || v != "yes" {
		t.Fatalf("Lookup present = (%q, %v), want (yes, true)", v, ok)
	}
	if v, ok := (OS{}).Lookup("KRIT_ENV_TEST_DEFINITELY_UNSET_zzz"); ok || v != "" {
		t.Fatalf("Lookup unset = (%q, %v), want (\"\", false)", v, ok)
	}
}

func TestDefault_IsOS(t *testing.T) {
	t.Parallel()

	if _, ok := Default.(OS); !ok {
		t.Fatalf("Default = %T, want OS", Default)
	}
}

func TestMap_DistinguishesUnsetAndEmpty(t *testing.T) {
	t.Parallel()

	m := Map{"SET_EMPTY": ""}
	if v, ok := m.Lookup("SET_EMPTY"); !ok || v != "" {
		t.Fatalf("Lookup SET_EMPTY = (%q, %v), want (\"\", true)", v, ok)
	}
	if v, ok := m.Lookup("ABSENT"); ok || v != "" {
		t.Fatalf("Lookup ABSENT = (%q, %v), want (\"\", false)", v, ok)
	}
}

func TestMap_NilMapIsUsable(t *testing.T) {
	t.Parallel()

	var m Map
	if v, ok := m.Lookup("ANYTHING"); ok || v != "" {
		t.Fatalf("nil Map Lookup = (%q, %v), want (\"\", false)", v, ok)
	}
}

func TestMap_WithDoesNotMutateReceiver(t *testing.T) {
	t.Parallel()

	base := Map{"A": "1"}
	derived := base.With("B", "2")

	if _, ok := base.Lookup("B"); ok {
		t.Fatal("base should not have key B after With")
	}
	if v, _ := derived.Lookup("A"); v != "1" {
		t.Errorf("derived A = %q, want 1", v)
	}
	if v, _ := derived.Lookup("B"); v != "2" {
		t.Errorf("derived B = %q, want 2", v)
	}
}

func TestGetReturnsValueOrEmpty(t *testing.T) {
	t.Parallel()

	m := Map{"K": "v"}
	if got := Get(m, "K"); got != "v" {
		t.Errorf("Get K = %q, want v", got)
	}
	if got := Get(m, "MISSING"); got != "" {
		t.Errorf("Get MISSING = %q, want \"\"", got)
	}
}

func TestGetTrim(t *testing.T) {
	t.Parallel()

	m := Map{"PADDED": "  spaced  "}
	if got := GetTrim(m, "PADDED"); got != "spaced" {
		t.Errorf("GetTrim = %q, want spaced", got)
	}
}

func TestGetInt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		val     string
		set     bool
		def     int
		want    int
		wantErr bool
	}{
		{name: "set", val: "42", set: true, def: 9, want: 42},
		{name: "unset", set: false, def: 9, want: 9},
		{name: "empty", val: "", set: true, def: 9, want: 9},
		{name: "whitespace", val: "  17 ", set: true, def: 0, want: 17},
		{name: "negative", val: "-5", set: true, def: 0, want: -5},
		{name: "garbage", val: "abc", set: true, def: 0, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var m Map
			if tc.set {
				m = Map{"K": tc.val}
			}
			got, err := GetInt(m, "K", tc.def)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestGetBool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		val     string
		set     bool
		def     bool
		want    bool
		wantErr bool
	}{
		{val: "1", set: true, want: true},
		{val: "TRUE", set: true, want: true},
		{val: "Yes", set: true, want: true},
		{val: "on", set: true, want: true},
		{val: "0", set: true, want: false},
		{val: "false", set: true, want: false},
		{val: "no", set: true, want: false},
		{val: "off", set: true, want: false},
		{set: false, def: true, want: true},
		{set: false, def: false, want: false},
		{val: "", set: true, def: true, want: true},
		{val: "  true ", set: true, want: true},
		{val: "tralse", set: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.val+"_"+boolStr(tc.set), func(t *testing.T) {
			t.Parallel()
			var m Map
			if tc.set {
				m = Map{"K": tc.val}
			}
			got, err := GetBool(m, "K", tc.def)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "set"
	}
	return "unset"
}

// Compile-time assertion that OS and Map both satisfy Reader.
var (
	_ Reader = OS{}
	_ Reader = Map(nil)
)
