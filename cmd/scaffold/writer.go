package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// nameRe matches a valid lowercase Go identifier suitable for both
// file and package names. We deliberately reject uppercase, leading
// digits, and dashes — ASCII underscores are allowed but discouraged.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// reservedNames are Go keywords / blank identifiers we forbid as names.
var reservedNames = map[string]struct{}{
	"_": {}, "go": {}, "if": {}, "for": {}, "var": {}, "func": {},
	"map": {}, "type": {}, "case": {}, "else": {}, "defer": {},
	"break": {}, "const": {}, "range": {}, "return": {}, "select": {},
	"struct": {}, "switch": {}, "import": {}, "package": {},
	"chan": {}, "goto": {}, "continue": {}, "fallthrough": {}, "interface": {},
	"main": {},
}

// validateName returns nil if name is a legal scaffold target.
func validateName(name string) error {
	if !nameRe.MatchString(name) {
		return errors.New("name must be lowercase, start with a letter, and contain only [a-z0-9_]")
	}
	if _, ok := reservedNames[name]; ok {
		return fmt.Errorf("%q is reserved", name)
	}
	return nil
}

// write materializes a Spec under repoRoot. Refuses to overwrite an
// existing file. Creates intermediate directories as needed.
func write(repoRoot string, spec Spec) (string, error) {
	rel := spec.Path()
	if rel == "" {
		return "", fmt.Errorf("unknown kind: %q", spec.Kind)
	}
	abs := filepath.Join(repoRoot, rel)
	if _, err := os.Stat(abs); err == nil {
		return "", fmt.Errorf("%s already exists; refusing to overwrite", rel)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(spec.Render()), 0o600); err != nil {
		return "", err
	}
	return abs, nil
}
