<!-- The title becomes the squash-merge commit subject: imperative, under ~72 chars. -->

## What and why

<!-- What does this change, and why? Link the issue: "Closes #123". -->

## How it was verified

<!-- Commands you ran and what you saw; screenshots for anything user-visible. -->

## Checklist

- [ ] `make check` passes (tidy, vet, golangci-lint, race tests)
- [ ] Tests added or updated, and they fail without this change
- [ ] `make fmt` leaves no diff
- [ ] No new `gocyclo` (functions ≤ 10), `gosec` or `govulncheck` findings (`make ci`)
- [ ] Docs updated (README, `docs/`, `CLAUDE.md` / `AGENTS.md`) if behavior, layout or targets changed
- [ ] New dev tools pinned in `tools/go.mod`; new actions pinned by SHA
