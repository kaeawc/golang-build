# Contributing

Thanks for helping out. This file covers the workflow; the
[README](README.md) covers the layout, and `AGENTS.md` / `CLAUDE.md` hold
the working rules for coding agents.

## Setup

All you need is Go, at the version in `go.mod` (and Docker if you want to
run the full stack with `scripts/dev.sh` or `docker compose up`). Dev tools
come from `tools/go.mod`, so there is nothing else to install.

## Before you push

```bash
make check     # tidy-check, vet, golangci-lint, race tests
```

`make check` is the pre-push gate and should pass on every commit you push.
`make ci` runs everything CI runs, adding gocyclo, gosec, govulncheck,
go-licenses, actionlint and `make rename-test` (the template rename on a
scratch copy). Other useful targets:

- `make fmt`: format with gofumpt + goimports (CI fails on a diff).
- `make lint-fix`: apply golangci-lint's automatic fixes.
- `go test ./internal/handlers/ -run TestX -count=1`: one test while you iterate.
- `make validate-workflows`: actionlint plus shellcheck for the scripts the
  workflows call (needs `shellcheck` installed).
- `make help`: everything else.

Keep functions at a cyclomatic complexity of 10 or less (`make complexity`),
and add tests that fail without your change. Code that talks to Postgres,
Valkey, subprocesses or the clock goes through an interface with a fake, so
tests run without any of them; follow the pattern of the package you touch.

## Dev tools

Tools are pinned with `tool` directives in `tools/go.mod`, a separate module
so their dependencies stay out of the application's `go.mod`. Run one with
`go tool -modfile=tools/go.mod <tool>`; the Makefile does this for you.

- Add a tool: `cd tools && go get -tool <module>/cmd/<tool>@<version> && go mod tidy`,
  then wire it into a Makefile target.
- Upgrade every tool: `make upgrade-tools`. Dependabot also opens a weekly
  grouped PR for `tools/`.
- Don't `go install` tools globally or call them from `$PATH` in the
  Makefile or workflows; CI must run the pinned versions.

Application dependencies: `make upgrade` for minor/patch bumps, and
`make tidy` after any change to imports.

## Commits

- Imperative subject line, under about 72 characters, saying what the change
  does: `Add cmd/inspect: eventbus + LiveView demo`,
  `Fix blank-import lint in internal/db/migrate.go`.
- A body (wrapped at 72) when the reason isn't obvious from the diff: what
  was wrong or missing, and why this approach.
- One logical change per commit; keep formatting-only changes separate.

## Pull requests

- Branch from `main` and open the PR against `main`; nobody pushes to
  `main` directly. Name branches `work/<topic>`.
- PRs are squash-merged, and the PR title becomes the commit subject (with
  ` (#123)` appended), so write the title like a commit subject.
- Fill in the [PR template](.github/pull_request_template.md): what changed
  and why, how you tested it, and `Closes #123` for the issue it resolves.
- All CI jobs in `commit.yml` must pass. CODEOWNERS requests review
  automatically.
- Pin new GitHub Actions to a full commit SHA with the version in a
  comment (`uses: owner/action@<sha> # v1.2.3`), as the `template-rename`
  job does; Dependabot updates the SHA and the comment together. (Older
  jobs still pin major-version tags.) Run `make actionlint` after editing
  a workflow.

## Reporting bugs and requesting features

Use the [issue forms](.github/ISSUE_TEMPLATE). For security vulnerabilities,
don't open a public issue: use GitHub's private vulnerability reporting
(Security → Report a vulnerability).
