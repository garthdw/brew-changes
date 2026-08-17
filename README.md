# brew-changes

A Homebrew tap that adds `brew changes`: before you upgrade, see what actually
changed.

`brew outdated` tells you *that* packages are outdated. `brew changes` tells
you *what's in* the update — an interactive list of every outdated formula
and cask, where you can expand any of them in place to see its
`CHANGELOG.md` (or `CHANGES.md`, `HISTORY.md`, `NEWS.md`), falling back to
GitHub Releases, then upgrade everything or just the one you're looking at.

<img width="1399" height="886" alt="image" src="https://github.com/user-attachments/assets/43705826-953a-4e60-bf34-02ccdda71ae1" />

<img width="1401" height="883" alt="image" src="https://github.com/user-attachments/assets/b5642a7c-27f1-4db6-bd29-5d4d1008b058" />

<img width="1410" height="884" alt="image" src="https://github.com/user-attachments/assets/7db1623b-8583-4291-aa96-06beccc85b6f" />

## Install

```sh
brew install garthdw/tap/brew-changes
```

This installs a prebuilt binary — no Go toolchain required.

## Usage

```sh
brew changes
```

This will:

1. Find every outdated formula and cask (`brew outdated`).
2. For each one, try to resolve its GitHub repository from its homepage or
   source URL.
3. Open an interactive list — nothing is fetched yet, so this is instant even
   with many outdated packages.
4. Navigate and review: expanding a package fetches and renders its
   changelog in place, so you only pay the GitHub API cost for what you
   actually look at.
5. Upgrade everything, or just the package you're currently looking at.

### Keybindings

| Key         | Action                                      |
| ----------- | -------------------------------------------- |
| `↑`/`↓`, `j`/`k`, `J`/`K` | Move the cursor                |
| `j`/`k`     | Scroll the changelog when the highlighted package is expanded |
| `Enter`     | Expand/collapse the highlighted package's changelog |
| `o`         | Open the highlighted package's homepage (or GitHub repo) in your browser |
| `g`         | Open the highlighted package's GitHub repo in your browser |
| `c`         | Open the highlighted package's changelog in your browser |
| `a`         | Upgrade all outdated packages and quit      |
| `u`         | Upgrade just the highlighted package and quit |
| `?`         | Show the help/about popup                   |
| `q`, `Ctrl+C` | Quit without upgrading                    |

Packages whose source isn't hosted on GitHub are listed with a note that no
changelog could be found, rather than being skipped silently.

## GitHub API rate limits

`brew changes` talks to the GitHub API directly. Unauthenticated requests are
limited to 60/hour, which can be tight if you review a lot of changelogs. Set
a token to raise that limit:

```sh
export GITHUB_TOKEN=ghp_...   # or GH_TOKEN
```

If you have the [`gh`](https://cli.github.com) CLI installed and authenticated
(`gh auth login`), `brew changes` will use its token automatically as a
fallback when neither environment variable is set.

## Releasing

Publishing a GitHub Release (with a `vX.Y.Z` tag) triggers
[`.github/workflows/release.yml`](.github/workflows/release.yml), which runs
[GoReleaser](https://goreleaser.com) to build macOS binaries (arm64 + amd64)
and attach them to the release, then auto-commits the regenerated
`Formula/brew-changes.rb` to `main` with the new version/url/sha256.
