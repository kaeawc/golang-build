// Package env provides a small Reader abstraction over process
// environment access. Production code injects a Reader instead of
// calling os.Getenv directly so tests can substitute Map and avoid
// mutating the global process environment (which races under
// t.Parallel()).
package env

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Reader reads environment-variable-style configuration.
type Reader interface {
	// Lookup returns the value of name and whether it was set. An
	// empty string with ok==true means "set to empty"; an empty
	// string with ok==false means "unset".
	Lookup(name string) (value string, ok bool)
}

// Get returns the value of name, or "" when unset. Convenience
// wrapper around Lookup matching the os.Getenv signature.
func Get(r Reader, name string) string {
	v, _ := r.Lookup(name)
	return v
}

// GetTrim returns the value of name with leading/trailing whitespace
// removed. Returns "" when unset.
func GetTrim(r Reader, name string) string {
	return strings.TrimSpace(Get(r, name))
}

// GetInt parses name as a signed decimal integer. Returns def when
// name is unset or empty. Returns an error when set but unparseable.
func GetInt(r Reader, name string, def int) (int, error) {
	v, ok := r.Lookup(name)
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("env %s: %w", name, err)
	}
	return n, nil
}

// GetBool parses common true/false spellings (1/0, true/false,
// yes/no, on/off — case-insensitive). Returns def when unset or
// empty. Returns an error when set but unparseable.
func GetBool(r Reader, name string, def bool) (bool, error) {
	v, ok := r.Lookup(name)
	v = strings.ToLower(strings.TrimSpace(v))
	if !ok || v == "" {
		return def, nil
	}
	switch v {
	case "1", "true", "yes", "on", "y", "t":
		return true, nil
	case "0", "false", "no", "off", "n", "f":
		return false, nil
	}
	return false, fmt.Errorf("env %s: not a boolean: %q", name, v)
}

// OS is a Reader backed by os.LookupEnv.
type OS struct{}

// Lookup implements Reader using os.LookupEnv.
func (OS) Lookup(name string) (string, bool) { return os.LookupEnv(name) }

// Default is a process-wide OS reader for callers that cannot reach
// a composition root. Prefer injecting a Reader explicitly.
var Default Reader = OS{}

// Map is a Reader backed by an in-memory map. The zero value is
// usable and reports every variable as unset.
//
// Map is safe to use concurrently for reads; tests typically populate
// it once before injecting it.
type Map map[string]string

// Lookup implements Reader.
func (m Map) Lookup(name string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[name]
	return v, ok
}

// With returns a new Map with name set to value, leaving the receiver
// untouched. Useful for chaining setup.
func (m Map) With(name, value string) Map {
	out := make(Map, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out[name] = value
	return out
}
