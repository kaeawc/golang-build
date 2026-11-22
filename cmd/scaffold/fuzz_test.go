package main

import (
	"go/parser"
	"go/token"
	"path"
	"strings"
	"testing"
)

// FuzzValidateName checks that every name the validator accepts is safe to
// generate from: a non-keyword Go identifier, a path that stays inside its
// target directory, and rendered source that parses as Go. Names it rejects
// are not inspected further; rejecting too much is a UX bug, not a crash.
func FuzzValidateName(f *testing.F) {
	for _, s := range []string{"users", "rate_limit", "x1", "", "Users", "1abc", "a-b", "a/b", "..", "main", "func", "café"} {
		f.Add(s)
	}
	// Random mutation rarely spells a whole keyword, so seed every one.
	for tok := range token.TILDE {
		if tok.IsKeyword() {
			f.Add(tok.String())
		}
	}
	f.Fuzz(func(t *testing.T, name string) {
		if validateName(name) != nil {
			return
		}
		if !token.IsIdentifier(name) || token.IsKeyword(name) {
			t.Fatalf("accepted %q, which is not a usable Go identifier", name)
		}
		for _, kind := range Kinds {
			spec := Spec{Kind: kind, Name: name}
			p := spec.Path()
			if path.Clean(p) != p || !strings.HasPrefix(p, "internal/") || strings.Contains(p, "..") {
				t.Fatalf("%s %q: path %q escapes internal/", kind, name, p)
			}
			file, err := parser.ParseFile(token.NewFileSet(), p, spec.Render(), 0)
			if err != nil {
				t.Fatalf("%s %q: rendered source does not parse: %v", kind, name, err)
			}
			if kind == KindPackage && file.Name.Name != name {
				t.Fatalf("package %q rendered as package %q", name, file.Name.Name)
			}
		}
	})
}
