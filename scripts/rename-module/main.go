// Command rename-module turns a fresh copy of this template into your own
// project. It rewrites
//
//   - the Go module path: go.mod, tools/go.mod, every import, the goimports
//     local prefix in .golangci.yml, and URLs that embed it;
//   - the project name (read from project_name in .goreleaser.yaml): the
//     goreleaser project, fly.toml app, Docker image tag, Fly app names in
//     the workflows and scripts, package.json, the web UI and docs;
//   - the GitHub owner in .github/CODEOWNERS, when both paths are on
//     github.com;
//
// then runs `go mod tidy` (app and tools modules) and `make check`.
//
// Usage:
//
//	make rename MODULE=github.com/you/app [NAME=app]
//	go run ./scripts/rename-module -module github.com/you/app [-name app] [-dry-run] [-skip-check]
//
// NAME defaults to the last element of MODULE (minus a /vN suffix). Only
// tracked files are touched and the tree must be clean, so `git diff` shows
// exactly what changed and `git checkout -- .` undoes it. Re-running with the
// same arguments is a no-op.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
)

// selfDir is never rewritten: its tests hold the template's own paths as
// fixtures.
const selfDir = "scripts/rename-module/"

type options struct {
	dir       string
	module    string
	name      string
	dryRun    bool
	skipCheck bool
}

func main() {
	var opts options
	flag.StringVar(&opts.module, "module", "", "new Go module path, e.g. github.com/you/app (required)")
	flag.StringVar(&opts.name, "name", "", "new project name for binaries, images and apps (default: last element of -module)")
	flag.StringVar(&opts.dir, "dir", ".", "repository root")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "print what would change without writing")
	flag.BoolVar(&opts.skipCheck, "skip-check", false, "skip `make check` after renaming")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, opts, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rename-module:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts options, out io.Writer) error {
	plan, err := newPlan(opts)
	if err != nil {
		return err
	}
	changes, err := plan.collect(ctx, opts.dir)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Fprintf(out, "Nothing to change: module is %s, name is %s.\n", plan.newModule, plan.newName)
		return nil
	}
	if !opts.dryRun {
		if err := requireCleanTree(ctx, opts.dir); err != nil {
			return err
		}
	}

	plan.report(out, changes, opts.dryRun)
	if opts.dryRun {
		return nil
	}
	if err := writeChanges(opts.dir, changes); err != nil {
		return fmt.Errorf("%w (the tree was clean: undo the partial rename with `git checkout -- .`)", err)
	}
	return finish(ctx, opts, out)
}

func (p *plan) report(out io.Writer, changes []change, dryRun bool) {
	fmt.Fprintf(out, "Module: %s -> %s\nName:   %s -> %s\n", p.oldModule, p.newModule, p.oldName, p.newName)
	verb := "Rewrote"
	if dryRun {
		verb = "Would rewrite"
	}
	fmt.Fprintf(out, "%s %d files (replacements per file):\n", verb, len(changes))
	for _, c := range changes {
		fmt.Fprintf(out, "  %-50s %d\n", c.path, c.count)
	}
	if p.oldOwner != "" && p.newOwner == "" {
		fmt.Fprintf(out, "Note: %s is not on github.com: .github/CODEOWNERS still names @%s, and GitHub-only links\n"+
			"(e.g. security advisories in .github/ISSUE_TEMPLATE/config.yml) now point at %s; edit them by hand.\n",
			p.newModule, p.oldOwner, p.newURL)
	}
}

// finish tidies both modules and runs the pre-push gate in the renamed tree.
func finish(ctx context.Context, opts options, out io.Writer) error {
	type step struct {
		dir  string
		args []string
	}
	tools := filepath.Join(opts.dir, "tools")
	steps := []step{{opts.dir, []string{"go", "mod", "tidy"}}}
	if _, err := os.Stat(filepath.Join(tools, "go.mod")); err == nil {
		steps = append(steps, step{tools, []string{"go", "mod", "tidy"}})
	}
	if !opts.skipCheck {
		steps = append(steps, step{opts.dir, []string{"make", "check"}})
	}
	for _, s := range steps {
		cmdline := strings.Join(s.args, " ")
		fmt.Fprintf(out, "\n$ (cd %s && %s)\n", s.dir, cmdline)
		if err := runIn(ctx, s.dir, out, s.args[0], s.args[1:]...); err != nil {
			return fmt.Errorf("%s: %w", cmdline, err)
		}
	}
	fmt.Fprintln(out, "\nDone. Review with `git diff`, then commit.")
	return nil
}

// plan holds the replacement rules derived from the current tree and flags.
type plan struct {
	oldModule, newModule string
	oldURL, newURL       string // repository web URLs: module path minus /vN
	oldName, newName     string
	oldOwner, newOwner   string
}

func newPlan(opts options) (*plan, error) {
	if opts.module == "" {
		return nil, errors.New("-module is required, e.g. -module github.com/you/app")
	}
	if err := validateModule(opts.module); err != nil {
		return nil, err
	}
	p := &plan{newModule: opts.module, newName: opts.name}
	if p.newName == "" {
		p.newName = deriveName(opts.module)
	}
	if !nameRE.MatchString(p.newName) {
		return nil, fmt.Errorf("name %q must match %s (it becomes a Fly app and image name); pass -name", p.newName, nameRE)
	}

	var err error
	if p.oldModule, err = readField(filepath.Join(opts.dir, "go.mod"), moduleRE); err != nil {
		return nil, err
	}
	if p.oldName, err = readField(filepath.Join(opts.dir, ".goreleaser.yaml"), projectRE); err != nil {
		return nil, err
	}
	p.oldURL, p.newURL = "https://"+repoPath(p.oldModule), "https://"+repoPath(p.newModule)
	p.oldOwner, p.newOwner = githubOwner(p.oldModule), githubOwner(p.newModule)
	return p, nil
}

var (
	moduleRE  = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
	projectRE = regexp.MustCompile(`(?m)^project_name:\s*["']?([^"'\s]+)["']?\s*$`)
	nameRE    = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)
	// Deliberately stricter than the go command: what's safe to splice into
	// YAML, TOML, shell and Go source unquoted.
	modulePathRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*(/[A-Za-z0-9._~-]+)*$`)
	majorRE      = regexp.MustCompile(`^v[0-9]+$`)
)

func validateModule(m string) error {
	if !modulePathRE.MatchString(m) || strings.Contains(m, "..") {
		return fmt.Errorf("invalid module path %q", m)
	}
	return nil
}

// repoPath strips a major-version suffix: github.com/you/app/v2 lives in
// the repository github.com/you/app.
func repoPath(module string) string {
	if i := strings.LastIndexByte(module, '/'); i > 0 && majorRE.MatchString(module[i+1:]) {
		return module[:i]
	}
	return module
}

// deriveName turns github.com/you/My_App/v2 into my-app.
func deriveName(module string) string {
	repo := repoPath(module)
	last := repo[strings.LastIndexByte(repo, '/')+1:]
	return strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(last))
}

func githubOwner(module string) string {
	elems := strings.Split(module, "/")
	if len(elems) >= 3 && elems[0] == "github.com" {
		return elems[1]
	}
	return ""
}

func readField(path string, re *regexp.Regexp) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := re.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("%s: no match for %s", path, re)
	}
	return string(m[1]), nil
}

// rule replaces old with new wherever old stands as a whole token: the byte
// before it is not in notBefore and the byte after it is not in notAfter.
// With notInPath, it also must not follow a single slash (a path segment
// such as "owner/name"); a double slash ("https://name") is fine.
type rule struct {
	old, new            string
	notBefore, notAfter string
	notInPath           bool
}

const ident = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"

func (p *plan) rules(path string) []rule {
	var rs []rule
	// Order matters: at a given offset the first matching rule wins, so the
	// module path is consumed before its trailing name could match.
	if p.oldURL != p.newURL {
		// Web links name the repository, which drops a /vN module suffix:
		// "https://github.com/o/x/issues", "https://github.com/o/x.git".
		rs = append(rs, rule{p.oldURL, p.newURL, ident + ".-", ident + "-", false})
	}
	if p.oldModule != p.newModule {
		// github.com/o/x matches in "github.com/o/x/internal", not in
		// "github.com/o/x-fork".
		rs = append(rs, rule{p.oldModule, p.newModule, ident + ".-", ident + "-", false})
	}
	if p.oldName != p.newName {
		// golang-build matches in "golang-build-pr-7" and
		// "https://golang-build.fly.dev", not in "someone/golang-build",
		// which is another repo or module.
		rs = append(rs, rule{p.oldName, p.newName, ident + "-", ident, true})
	}
	if p.oldOwner != "" && p.newOwner != "" && p.oldOwner != p.newOwner && filepath.Base(path) == "CODEOWNERS" {
		rs = append(rs, rule{"@" + p.oldOwner, "@" + p.newOwner, ident, ident + "-", false})
	}
	return rs
}

func replaceAll(src []byte, rules []rule) ([]byte, int) {
	s := string(src)
	if !containsAny(s, rules) {
		return src, 0
	}
	var out strings.Builder
	out.Grow(len(s))
	n := 0
	for i := 0; i < len(s); {
		r, ok := matchAt(s, i, rules)
		if !ok {
			out.WriteByte(s[i])
			i++
			continue
		}
		out.WriteString(r.new)
		i += len(r.old)
		n++
	}
	return []byte(out.String()), n
}

func containsAny(s string, rules []rule) bool {
	for _, r := range rules {
		if strings.Contains(s, r.old) {
			return true
		}
	}
	return false
}

func matchAt(s string, i int, rules []rule) (rule, bool) {
	for _, r := range rules {
		if strings.HasPrefix(s[i:], r.old) && r.startsToken(s, i) && r.endsToken(s, i+len(r.old)) {
			return r, true
		}
	}
	return rule{}, false
}

func (r rule) startsToken(src string, i int) bool {
	if i == 0 {
		return true
	}
	if strings.IndexByte(r.notBefore, src[i-1]) >= 0 {
		return false
	}
	singleSlash := src[i-1] == '/' && (i < 2 || src[i-2] != '/')
	return !r.notInPath || !singleSlash
}

func (r rule) endsToken(src string, j int) bool {
	return j == len(src) || strings.IndexByte(r.notAfter, src[j]) < 0
}

type change struct {
	path    string
	content []byte
	mode    os.FileMode
	count   int
}

// collect computes the new content of every tracked text file that changes.
func (p *plan) collect(ctx context.Context, dir string) ([]change, error) {
	files, err := trackedFiles(ctx, dir)
	if err != nil {
		return nil, err
	}
	var changes []change
	for _, f := range files {
		c, err := p.rewrite(dir, f)
		if err != nil {
			return nil, err
		}
		if c != nil {
			changes = append(changes, *c)
		}
	}
	return changes, nil
}

func (p *plan) rewrite(dir, path string) (*change, error) {
	if skip(path) {
		return nil, nil
	}
	rules := p.rules(path)
	if len(rules) == 0 {
		return nil, nil
	}
	src, mode, err := readText(filepath.Join(dir, path))
	if src == nil || err != nil {
		return nil, err
	}
	dst, n := replaceAll(src, rules)
	if n == 0 {
		return nil, nil
	}
	if strings.HasSuffix(path, ".go") {
		// A longer or shorter string literal can shift gofmt's alignment of
		// neighbouring comments.
		if dst, err = format.Source(dst); err != nil {
			return nil, fmt.Errorf("%s: gofmt after rewrite: %w", path, err)
		}
	}
	return &change{path: path, content: dst, mode: mode, count: n}, nil
}

// readText returns nil content for anything that isn't a regular text file:
// symlinks, binaries, and tracked files deleted from the worktree.
func readText(path string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, err
	}
	src, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(src, 0) >= 0 {
		return nil, 0, err
	}
	return src, info.Mode().Perm(), nil
}

func skip(path string) bool {
	return strings.HasPrefix(path, selfDir) ||
		strings.HasSuffix(path, ".sum") ||
		path == "LICENSE"
}

func writeChanges(dir string, changes []change) error {
	for _, c := range changes {
		if err := os.WriteFile(filepath.Join(dir, c.path), c.content, c.mode); err != nil {
			return err
		}
	}
	return nil
}

func trackedFiles(ctx context.Context, dir string) ([]string, error) {
	var out bytes.Buffer
	if err := runIn(ctx, dir, &out, "git", "ls-files", "-z"); err != nil {
		return nil, fmt.Errorf("listing tracked files (is %s a git checkout?): %w", dir, err)
	}
	return strings.FieldsFunc(out.String(), func(r rune) bool { return r == 0 }), nil
}

func requireCleanTree(ctx context.Context, dir string) error {
	var out bytes.Buffer
	if err := runIn(ctx, dir, &out, "git", "status", "--porcelain"); err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if s := strings.TrimSpace(out.String()); s != "" {
		return fmt.Errorf("working tree has uncommitted changes; commit or stash them first:\n%s", s)
	}
	return nil
}

func runIn(ctx context.Context, dir string, out io.Writer, name string, args ...string) error {
	// #nosec G204 -- callers pass fixed commands (git, go, make); only the args vary
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
