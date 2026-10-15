// Package homebrew wraps the `brew` CLI's JSON output for outdated-package
// discovery, per-package metadata lookup, and upgrading.
package homebrew

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Package is an outdated formula or cask.
type Package struct {
	Name      string
	Installed string
	Current   string
	IsCask    bool
}

type outdatedEntry struct {
	Name              string   `json:"name"`
	InstalledVersions []string `json:"installed_versions"`
	CurrentVersion    string   `json:"current_version"`
}

type outdatedResult struct {
	Formulae []outdatedEntry `json:"formulae"`
	Casks    []outdatedEntry `json:"casks"`
}

// Outdated returns every outdated formula and cask.
func Outdated() ([]Package, error) {
	out, err := exec.Command("brew", "outdated", "--json=v2").Output()
	if err != nil {
		return nil, fmt.Errorf("brew outdated: %w", err)
	}
	var result outdatedResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing brew outdated output: %w", err)
	}
	return append(toPackages(result.Formulae, false), toPackages(result.Casks, true)...), nil
}

func toPackages(entries []outdatedEntry, isCask bool) []Package {
	packages := make([]Package, 0, len(entries))
	for _, e := range entries {
		packages = append(packages, Package{
			Name:      e.Name,
			Installed: upstreamVersion(firstOrEmpty(e.InstalledVersions), isCask),
			Current:   upstreamVersion(e.CurrentVersion, isCask),
			IsCask:    isCask,
		})
	}
	return packages
}

var revisionSuffix = regexp.MustCompile(`_[0-9]+$`)

// upstreamVersion drops what brew appends to a version that upstream tags,
// changelog headings and URLs never include: a formula's "_N" revision
// ("2.51.0_1") or a cask's ",build" suffix (docker-desktop's
// "4.95.0,242831").
func upstreamVersion(v string, isCask bool) string {
	if isCask {
		v, _, _ = strings.Cut(v, ",")
		return v
	}
	return revisionSuffix.ReplaceAllString(v, "")
}

func kindFlag(isCask bool) string {
	if isCask {
		return "--cask"
	}
	return "--formula"
}

func firstOrEmpty(versions []string) string {
	if len(versions) > 0 {
		return versions[0]
	}
	return ""
}

type formulaInfo struct {
	FullName string `json:"full_name"`
	Homepage string `json:"homepage"`
	Versions struct {
		Stable string `json:"stable"`
	} `json:"versions"`
	URLs struct {
		Stable struct {
			URL string `json:"url"`
		} `json:"stable"`
		Head struct {
			URL string `json:"url"`
		} `json:"head"`
	} `json:"urls"`
}

type caskInfo struct {
	FullToken string `json:"full_token"`
	Homepage  string `json:"homepage"`
	Version   string `json:"version"`
	URL       string `json:"url"`
}

type infoResult struct {
	Formulae []formulaInfo `json:"formulae"`
	Casks    []caskInfo    `json:"casks"`
}

// Info is a package's homepage, source/download URL, and current stable
// version.
type Info struct {
	Homepage string
	URL      string
	Version  string
}

// resolveBatchSize caps how many names go into a single `brew info`
// invocation. `brew info` exits nonzero with no JSON output at all if even
// one name in the batch fails to resolve (a renamed/removed formula, a
// cask-only name passed as a formula, etc), so a single bad name would
// otherwise poison results for the whole list. Chunking bounds that blast
// radius, and resolveChunk bisects a failing chunk to isolate and drop just
// the offending name(s).
const resolveBatchSize = 50

// ResolveInfoBatch returns homepage/source info for multiple formulae or
// casks (but not both), keyed by name. Names not found in brew's output
// (including ones that fail to resolve at all) are simply absent from the
// result.
func ResolveInfoBatch(names []string, isCask bool) (map[string]Info, error) {
	infos := make(map[string]Info, len(names))
	if len(names) == 0 {
		return infos, nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	var firstErr error

	for chunk := range slices.Chunk(names, resolveBatchSize) {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			resolved, err := resolveChunk(chunk, isCask)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			maps.Copy(infos, resolved)
		})
	}
	wg.Wait()

	if len(infos) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return infos, nil
}

// ResolveInfo returns each package's Info, aligned with packages, resolving
// formulae and casks concurrently. Packages brew can't resolve get a zero
// Info.
func ResolveInfo(packages []Package) []Info {
	var formulae, casks []string
	for _, p := range packages {
		if p.IsCask {
			casks = append(casks, p.Name)
		} else {
			formulae = append(formulae, p.Name)
		}
	}
	// Errors leave the map nil, which reads as "no info" for every package.
	var formulaInfos, caskInfos map[string]Info
	var wg sync.WaitGroup
	wg.Go(func() { formulaInfos, _ = ResolveInfoBatch(formulae, false) })
	wg.Go(func() { caskInfos, _ = ResolveInfoBatch(casks, true) })
	wg.Wait()

	infos := make([]Info, len(packages))
	for i, p := range packages {
		if p.IsCask {
			infos[i] = caskInfos[p.Name]
		} else {
			infos[i] = formulaInfos[p.Name]
		}
	}
	return infos
}

// resolveChunk runs a single `brew info` call for chunk. If brew itself
// can't be run, or its output can't be parsed, that error is returned
// as-is. If brew runs but exits nonzero (some name in chunk didn't
// resolve), the chunk is bisected to isolate and drop the offending
// name(s) instead of losing the whole chunk.
func resolveChunk(chunk []string, isCask bool) (map[string]Info, error) {
	infos, err := fetchInfo(chunk, isCask)
	if err == nil {
		return infos, nil
	}
	if len(chunk) == 1 {
		return nil, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return nil, err
	}

	mid := len(chunk) / 2
	left, _ := resolveChunk(chunk[:mid], isCask)
	right, _ := resolveChunk(chunk[mid:], isCask)
	merged := make(map[string]Info, len(left)+len(right))
	maps.Copy(merged, left)
	maps.Copy(merged, right)
	return merged, nil
}

func fetchInfo(names []string, isCask bool) (map[string]Info, error) {
	flag := kindFlag(isCask)
	out, err := exec.Command("brew", append([]string{"info", "--json=v2", flag}, names...)...).Output()
	if err != nil {
		return nil, err
	}
	var result infoResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing brew info %s %v output: %w", flag, names, err)
	}

	infos := make(map[string]Info, len(names))
	for _, c := range result.Casks {
		infos[c.FullToken] = Info{Homepage: c.Homepage, URL: c.URL, Version: upstreamVersion(c.Version, true)}
	}
	for _, f := range result.Formulae {
		infos[f.FullName] = Info{Homepage: f.Homepage, URL: cmp.Or(f.URLs.Stable.URL, f.URLs.Head.URL), Version: f.Versions.Stable}
	}
	return infos, nil
}

// Upgrade runs `brew upgrade` for the named formulae and/or `brew upgrade
// --cask` for the named casks, streaming brew's output to the terminal.
// Only safe to call when nothing else owns the terminal (e.g. after a TUI
// has quit) — use UpgradeQuiet otherwise.
func Upgrade(formulaNames, caskNames []string) error {
	return upgrade(run, formulaNames, caskNames)
}

// UpgradeQuiet behaves like Upgrade but captures brew's output instead of
// streaming it to the terminal, for callers that don't own the terminal
// (e.g. a Bubble Tea UI still running in alt-screen mode).
func UpgradeQuiet(formulaNames, caskNames []string) error {
	return upgrade(runCaptured, formulaNames, caskNames)
}

func upgrade(runFn func(name string, args ...string) error, formulaNames, caskNames []string) error {
	if len(formulaNames) > 0 {
		if err := runFn("brew", append([]string{"upgrade"}, formulaNames...)...); err != nil {
			return err
		}
	}
	if len(caskNames) > 0 {
		if err := runFn("brew", append([]string{"upgrade", "--cask"}, caskNames...)...); err != nil {
			return err
		}
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w", name, args, err)
	}
	return nil
}

func runCaptured(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}
