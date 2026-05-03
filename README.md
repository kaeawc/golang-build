# golang-build
Experimental Go build tooling, references, and CI pipeline.

## Binaries

- `cmd/server` — HTTP API on `:8080` (gorilla/mux + middleware chain).
- `cmd/onboard` — interactive TUI wizard that generates a `.env` for the server.

## TUI scaffolding

`internal/tui/` is a small framework on top of [bubbletea](https://github.com/charmbracelet/bubbletea)/[lipgloss](https://github.com/charmbracelet/lipgloss) for building multi-step terminal wizards from this template. It exposes a `Phase` interface and four reusable phases:

- `Picker` — single-select list, optionally rendered as a comparison table.
- `Confirm` — yes/no prompt with default selection.
- `AsyncTask` — runs a worker function with a spinner, emits `TaskDoneMsg`.
- `Done` — final summary, quits on enter.

Each phase emits a tagged completion message (`PickerDoneMsg`, `ConfirmDoneMsg`, `TaskDoneMsg`); the caller's root `tea.Model` switches on the tag and transitions to the next phase. See `cmd/onboard/model.go` for the canonical assembly pattern, and `cmd/onboard/headless.go` for the non-TTY codepath used in CI.

```bash
make build-onboard
./onboard                              # interactive
./onboard --preset standard --yes      # headless, writes .env with defaults
```
