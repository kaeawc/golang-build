package env

import (
	"strconv"
	"strings"
	"testing"
)

// FuzzGetInt checks GetInt against strconv as an oracle and that it
// round-trips every int it formats.
func FuzzGetInt(f *testing.F) {
	for _, s := range []string{"0", "42", " -7 ", "+3", "", "  ", "1e3", "0x10", "9223372036854775808", "٣"} {
		f.Add(s, 5)
	}
	f.Fuzz(func(t *testing.T, raw string, def int) {
		got, err := GetInt(Map{"N": raw}, "N", def)
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			if err != nil || got != def {
				t.Fatalf("GetInt(%q, def=%d) = %d, %v; want default", raw, def, got, err)
			}
			return
		}
		want, wantErr := strconv.Atoi(trimmed)
		if (err != nil) != (wantErr != nil) || (err == nil && got != want) {
			t.Fatalf("GetInt(%q) = %d, %v; strconv.Atoi = %d, %v", raw, got, err, want, wantErr)
		}

		// Whatever GetInt returns must survive formatting and re-reading.
		back, err := GetInt(Map{"N": strconv.Itoa(got)}, "N", 0)
		if err != nil || back != got {
			t.Fatalf("round trip %d: got %d, %v", got, back, err)
		}
	})
}

// FuzzGetBool checks that GetBool accepts exactly the documented
// spellings, ignoring surrounding whitespace and letter case.
func FuzzGetBool(f *testing.F) {
	for _, s := range []string{"true", " YES ", "0", "Off", "y", "", "maybe", "truee", "\ttrue\n", "ＴＲＵＥ"} {
		f.Add(s)
	}
	spellings := map[string]bool{
		"1": true, "true": true, "yes": true, "on": true, "y": true, "t": true,
		"0": false, "false": false, "no": false, "off": false, "n": false, "f": false,
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got, err := GetBool(Map{"B": raw}, "B", true)
		norm := strings.ToLower(strings.TrimSpace(raw))
		want, known := spellings[norm]
		switch {
		case norm == "":
			if err != nil || !got {
				t.Fatalf("GetBool(%q) = %t, %v; want default true", raw, got, err)
			}
		case !known:
			if err == nil {
				t.Fatalf("GetBool(%q) = %t; want error for unknown spelling", raw, got)
			}
		case err != nil || got != want:
			t.Fatalf("GetBool(%q) = %t, %v; want %t", raw, got, err, want)
		}

		padded, err2 := GetBool(Map{"B": " \t" + raw + "\n "}, "B", true)
		if padded != got || (err == nil) != (err2 == nil) {
			t.Fatalf("whitespace padding changed GetBool(%q): %t,%v vs %t,%v", raw, got, err, padded, err2)
		}
	})
}
