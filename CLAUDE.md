# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`brew-changes` is the source for a Go CLI (`brew changes`), published as a formula in the shared `garthdw/homebrew-tap`. The CLI shows what actually changed in outdated Homebrew formulae/casks (changelog or GitHub release notes) before the user upgrades, via an interactive Bubble Tea TUI.

## Commands

```sh
go build ./...              # build everything
go vet ./...                 # static checks
gofmt -l .                   # list files needing formatting (fix with gofmt -w)
go run ./cmd/brew-changes    # run the TUI locally
go test ./...                # run unit tests
```

### Release validation (GoReleaser)

```sh
goreleaser check                                   # validate .goreleaser.yaml
goreleaser release --snapshot --skip=publish --clean   # local build without publishing/tagging
```

## Architecture

Package flow, in the order `cmd/brew-changes/main.go` calls them:

1. **`internal/homebrew`** — shells out to `brew outdated --json=v2` (formula + cask) and `brew info --json=v2` to list outdated packages and resolve each one's homepage/source URL. Also runs `brew upgrade`.
2. **`internal/ghsource`** — resolves a homepage/source URL to a `(owner, repo)` GitHub pair, then talks to the GitHub REST API directly (not via `gh`) to fetch a changelog file (tries `CHANGELOG.md`, `CHANGES.md`, `HISTORY.md`, `NEWS.md`, `CHANGELOG` in order) or fall back to release notes. Auth token resolution order: `GITHUB_TOKEN` / `GH_TOKEN` env vars, then `gh auth token` if the `gh` CLI is installed; cached for the process via `sync.Once` since it may spawn a subprocess.
3. **`internal/changelog`** — trims a full changelog file down to just the section between the installed and current version headings (`TrimToRange`), so the TUI doesn't dump the entire file.
4. **`internal/ui`** — the Bubble Tea model/view driving the interactive list. Key structural point: item metadata (name, versions, owner/repo) is resolved eagerly for all outdated packages up front, but changelog *bodies* are fetched lazily via `tea.Cmd` only when a package is expanded — this is what keeps the initial list instant even with many outdated packages.

### UI navigation model (`internal/ui/model.go`)

Arrow keys (`up`/`down`) always move the cursor between items. `j`/`k` are dual-purpose: they scroll the expanded item's changelog body when the highlighted item is expanded, otherwise they move the cursor like the arrow keys. Move the cursor via `m.moveCursor`, which calls `m.refreshViewport()` — the rendered content (including the `>` cursor marker and bold-selected styling) is only regenerated on that call, it does not follow `m.cursor` automatically.

### Release pipeline

Publishing a GitHub Release (tag `vX.Y.Z`) triggers `.github/workflows/release.yml`, which runs GoReleaser to cross-compile macOS and Linux binaries (arm64 + amd64, no CGO), attach them to the release, and auto-commit the regenerated `Formula/brew-changes.rb` to `main` via GoReleaser's `brews` publisher (configured in `.goreleaser.yaml`). **`Formula/brew-changes.rb` is generated — don't hand-edit it**, edit `.goreleaser.yaml`'s `brews:` block instead. Note: GoReleaser has soft-deprecated `brews` in favor of `homebrew_casks`, but the installed GoReleaser version doesn't yet implement a formula-generating replacement, so `brews` (with its deprecation warning) is intentional here, not an oversight.

There is no `vendor/` directory — it was intentionally removed once the formula switched from building from source to downloading prebuilt binaries, since Homebrew no longer needs to `go build` in a network-restricted sandbox.

## Code comments

Comments should only exist to help when the code is confusing, or to explain purpose/rationale that isn't obvious from reading it. Don't add a comment if it's easily understood from the code itself, and don't leave comments describing how the code used to work. The exception is a comment explaining *why* code is written in an unusual way to handle a specific edge case or issue (e.g. "loop here to avoid a lookup issue") — that kind of non-obvious rationale is worth keeping.
