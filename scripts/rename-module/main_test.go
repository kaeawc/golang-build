package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testPlan() *plan {
	return &plan{
		oldModule: "github.com/kaeawc/golang-build", newModule: "github.com/you/app",
		oldName: "golang-build", newName: "app",
		oldOwner: "kaeawc", newOwner: "you",
	}
}

func TestReplaceAll(t *testing.T) {
	tests := []struct {
		name, path, in, want string
	}{
		{"go.mod", "go.mod", "module github.com/kaeawc/golang-build\n", "module github.com/you/app\n"},
		{"tools module", "tools/go.mod", "module github.com/kaeawc/golang-build/tools\n", "module github.com/you/app/tools\n"},
		{"import", "x.go", `import "github.com/kaeawc/golang-build/internal/tui"`, `import "github.com/you/app/internal/tui"`},
		{"url", "README.md", "https://github.com/kaeawc/golang-build.git", "https://github.com/you/app.git"},
		{"other module untouched", "x.go", `"github.com/kaeawc/golang-build-fork/x"`, `"github.com/kaeawc/golang-build-fork/x"`},
		{"goreleaser", ".goreleaser.yaml", "project_name: golang-build\n", "project_name: app\n"},
		{"fly app", "fly.toml", `app = "golang-build"`, `app = "app"`},
		{"image tag", "Makefile", "-t golang-build:dev .", "-t app:dev ."},
		{"preview app", "preview.yml", "APP: golang-build-pr-${{ n }}", "APP: app-pr-${{ n }}"},
		{"fly host", "deploy.yml", "https://golang-build.fly.dev/healthz", "https://app.fly.dev/healthz"},
		{"prefixed name untouched", "x.md", "my-golang-build and golang-build_x", "my-golang-build and golang-build_x"},
		{"codeowners", ".github/CODEOWNERS", "* @kaeawc\nci/ @kaeawc @kaeawc-bot\n", "* @you\nci/ @you @kaeawc-bot\n"},
		{"owner only in CODEOWNERS", "README.md", "ping @kaeawc", "ping @kaeawc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := replaceAll([]byte(tt.in), testPlan().rules(tt.path))
			if string(got) != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestReplaceAllNewModuleContainsOldName(t *testing.T) {
	// Single pass: the new module path must not be re-matched by the name rule.
	p := testPlan()
	p.newModule = "github.com/you/golang-build-fork"
	got, n := replaceAll([]byte("module github.com/kaeawc/golang-build\n"), p.rules("go.mod"))
	if want := "module github.com/you/golang-build-fork\n"; string(got) != want || n != 1 {
		t.Errorf("got %q (%d replacements), want %q (1)", got, n, want)
	}
}

func TestDeriveName(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/you/app":        "app",
		"github.com/you/My_App/v2":  "my-app",
		"example.com/svc.io":        "svc-io",
		"gitlab.com/group/sub/tool": "tool",
	} {
		if got := deriveName(in); got != want {
			t.Errorf("deriveName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateModule(t *testing.T) {
	for _, ok := range []string{"github.com/you/app", "example.com/a/b/v2", "app"} {
		if err := validateModule(ok); err != nil {
			t.Errorf("validateModule(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "/abs", "a//b", "a/b/", "a b", `a"b`, "a/../b", "a/$(x)"} {
		if err := validateModule(bad); err == nil {
			t.Errorf("validateModule(%q) = nil, want error", bad)
		}
	}
}

// TestRunEndToEnd renames a miniature template in a temporary git repo.
func TestRunEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":           "module github.com/kaeawc/golang-build\n\ngo 1.27.0\n",
		"tools/go.mod":     "module github.com/kaeawc/golang-build/tools\n\ngo 1.27.0\n",
		".goreleaser.yaml": "version: 2\nproject_name: golang-build\n",
		"fly.toml":         "app = \"golang-build\"\n",
		"lib/lib.go":       "// Package lib is a fixture.\npackage lib\n\n// Name is the project.\nconst Name = \"golang-build\"\n",
		"main.go": "package main\n\nimport \"github.com/kaeawc/golang-build/lib\"\n\n" +
			"var (\n\ta    = lib.Name     // x\n\tlong = \"golang-build\" // y\n)\n\nfunc main() { _, _ = a, long }\n",
		selfDir + "fixture.txt": "github.com/kaeawc/golang-build stays\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "init", "-q")
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init")

	opts := options{dir: dir, module: "example.com/acme/widget", skipCheck: true}

	// Dirty trees are refused before anything is written.
	writeFile(t, dir, "scratch.txt", "x")
	if err := run(t.Context(), opts, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("run on dirty tree: err = %v, want uncommitted-changes error", err)
	}
	if err := os.Remove(filepath.Join(dir, "scratch.txt")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run(t.Context(), opts, &out); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	for name, want := range map[string]string{
		"go.mod":                "module example.com/acme/widget\n",
		"tools/go.mod":          "module example.com/acme/widget/tools\n",
		".goreleaser.yaml":      "project_name: widget\n",
		"fly.toml":              `app = "widget"`,
		"main.go":               "import \"example.com/acme/widget/lib\"",
		selfDir + "fixture.txt": "github.com/kaeawc/golang-build stays",
	} {
		if got := readFile(t, dir, name); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", name, got, want)
		}
	}
	// gofmt re-aligned the comments after the literal got shorter.
	if got, want := readFile(t, dir, "main.go"), "\ta    = lib.Name // x\n\tlong = \"widget\" // y\n"; !strings.Contains(got, want) {
		t.Errorf("main.go not gofmt'd:\n%s", got)
	}
	if !strings.Contains(out.String(), "main.go") || !strings.Contains(out.String(), "-> example.com/acme/widget") {
		t.Errorf("output does not list changes:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "CODEOWNERS still names @kaeawc") {
		t.Errorf("output does not flag CODEOWNERS for a non-GitHub module:\n%s", out.String())
	}

	// Idempotent: the same arguments again change nothing, even uncommitted.
	out.Reset()
	if err := run(t.Context(), opts, &out); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing to change") {
		t.Errorf("second run output = %q, want Nothing to change", out.String())
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
