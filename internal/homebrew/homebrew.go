// Package homebrew wraps the `brew` CLI's JSON output for outdated-package
// discovery, per-package metadata lookup, and upgrading.
package homebrew

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

type outdatedFormula struct {
	Name              string   `json:"name"`
	InstalledVersions []string `json:"installed_versions"`
	CurrentVersion    string   `json:"current_version"`
}

type outdatedCask struct {
	Name              string   `json:"name"`
	InstalledVersions []string `json:"installed_versions"`
	CurrentVersion    string   `json:"current_version"`
}

type outdatedResult struct {
	Formulae []outdatedFormula `json:"formulae"`
	Casks    []outdatedCask    `json:"casks"`
}

// Outdated returns every outdated formula and cask.
func Outdated() ([]Package, error) {
	var result outdatedResult

	var formulaResult, caskResult outdatedResult
	var formulaErr, caskErr error

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		out, err := exec.Command("brew", "outdated", "--json=v2", "--formula").Output()
		if err != nil {
			formulaErr = fmt.Errorf("brew outdated --formula: %w", err)
			return
		}
		if err := json.Unmarshal(out, &formulaResult); err != nil {
			formulaErr = fmt.Errorf("parsing brew outdated --formula output: %w", err)
		}
	}()
	go func() {
		defer wg.Done()
		out, err := exec.Command("brew", "outdated", "--json=v2", "--cask").Output()
		if err != nil {
			caskErr = fmt.Errorf("brew outdated --cask: %w", err)
			return
		}
		if err := json.Unmarshal(out, &caskResult); err != nil {
			caskErr = fmt.Errorf("parsing brew outdated --cask output: %w", err)
		}
	}()
	wg.Wait()

	if formulaErr != nil {
		return nil, formulaErr
	}
	if caskErr != nil {
		return nil, caskErr
	}
	result.Formulae = formulaResult.Formulae
	result.Casks = caskResult.Casks

	packages := make([]Package, 0, len(result.Formulae)+len(result.Casks))
	for _, f := range result.Formulae {
		packages = append(packages, Package{
			Name:      f.Name,
			Installed: firstOrEmpty(f.InstalledVersions),
			Current:   f.CurrentVersion,
			IsCask:    false,
		})
	}
	for _, c := range result.Casks {
		packages = append(packages, Package{
			Name:      c.Name,
			Installed: firstOrEmpty(c.InstalledVersions),
			Current:   c.CurrentVersion,
			IsCask:    true,
		})
	}

	return packages, nil
}

func firstOrEmpty(versions []string) string {
	if len(versions) > 0 {
		return versions[0]
	}
	return ""
}

type formulaInfo struct {
	Name     string `json:"name"`
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
	Token    string `json:"token"`
	Homepage string `json:"homepage"`
	Version  string `json:"version"`
	URL      string `json:"url"`
}

type formulaInfoResult struct {
	Formulae []formulaInfo `json:"formulae"`
}

type caskInfoResult struct {
	Casks []caskInfo `json:"casks"`
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

	for i := 0; i < len(names); i += resolveBatchSize {
		end := i + resolveBatchSize
		if end > len(names) {
			end = len(names)
		}
		chunk := names[i:end]
		wg.Add(1)
		sem <- struct{}{}
		go func(chunk []string) {
			defer wg.Done()
			defer func() { <-sem }()
			resolved, err := resolveChunk(chunk, isCask)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			for k, v := range resolved {
				infos[k] = v
			}
		}(chunk)
	}
	wg.Wait()

	if len(infos) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return infos, nil
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
	for k, v := range left {
		merged[k] = v
	}
	for k, v := range right {
		merged[k] = v
	}
	return merged, nil
}

func fetchInfo(names []string, isCask bool) (map[string]Info, error) {
	infos := make(map[string]Info, len(names))

	if isCask {
		args := append([]string{"info", "--json=v2", "--cask"}, names...)
		out, err := exec.Command("brew", args...).Output()
		if err != nil {
			return nil, err
		}
		var result caskInfoResult
		if err := json.Unmarshal(out, &result); err != nil {
			return nil, fmt.Errorf("parsing brew info --cask %v output: %w", names, err)
		}
		for _, c := range result.Casks {
			infos[c.Token] = Info{Homepage: c.Homepage, URL: c.URL, Version: c.Version}
		}
		return infos, nil
	}

	args := append([]string{"info", "--json=v2", "--formula"}, names...)
	out, err := exec.Command("brew", args...).Output()
	if err != nil {
		return nil, err
	}
	var result formulaInfoResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing brew info --formula %v output: %w", names, err)
	}
	for _, f := range result.Formulae {
		url := f.URLs.Stable.URL
		if url == "" {
			url = f.URLs.Head.URL
		}
		infos[f.Name] = Info{Homepage: f.Homepage, URL: url, Version: f.Versions.Stable}
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
