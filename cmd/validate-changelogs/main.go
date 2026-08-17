// Command validate-changelogs is a standalone dev tool, not part of the
// shipped brew-changes binary (GoReleaser's build only points at
// ./cmd/brew-changes). It exercises this repo's changelog-resolution and
// version-matching logic against Homebrew's most-installed formulae, to
// catch regressions or gaps across a wide variety of real-world repos.
//
// Usage: go run ./cmd/validate-changelogs [N]
// N defaults to 50.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/garthdw/brew-changes/internal/changelogconfig"
	"github.com/garthdw/brew-changes/internal/ghsource"
	"github.com/garthdw/brew-changes/internal/homebrew"
)

type analyticsResponse struct {
	Items []struct {
		Formula string `json:"formula"`
	} `json:"items"`
}

type result struct {
	name        string
	owner, repo string
	source      string // "file:<name>", "releases", "no-repo", "skip-tap"
	found       bool
	skipped     bool
	manual      bool // recovered via changelog_config.json rather than auto-detection
	partial     bool // releases fetched, but the target version's tag wasn't among them (best effort)
	note        string
}

func main() {
	n := 50
	if len(os.Args) > 1 {
		if v, err := strconv.Atoi(os.Args[1]); err == nil {
			n = v
		}
	}

	names, err := topFormulae(n)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetching analytics:", err)
		os.Exit(1)
	}

	resolvable, skipped, err := filterUntapped(names)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listing taps:", err)
		os.Exit(1)
	}

	infos, err := homebrew.ResolveInfoBatch(resolvable, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "brew info:", err)
		os.Exit(1)
	}

	results := make([]result, 0, len(infos)+len(skipped))
	for _, r := range skipped {
		results = append(results, r)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for name, info := range infos {
		wg.Add(1)
		sem <- struct{}{}
		go func(name string, info homebrew.Info) {
			defer wg.Done()
			defer func() { <-sem }()
			r := validate(name, info)
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(name, info)
	}
	wg.Wait()

	for i := range results {
		r := &results[i]
		if r.skipped || r.found {
			continue
		}
		if url, ok := changelogconfig.Lookup(r.name); ok {
			r.manual = true
			r.note = "manual: " + url
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].name < results[j].name })

	var ok, fail, skip, manual, partial int
	for _, r := range results {
		status := "OK"
		switch {
		case r.skipped:
			status = "SKIP"
			skip++
		case r.manual:
			status = "MANUAL"
			manual++
		case r.partial:
			status = "PARTIAL"
			partial++
		case !r.found:
			status = "FAIL"
			fail++
		default:
			ok++
		}
		fmt.Printf("%-6s %-20s %-30s %-18s %s\n", status, r.name, r.owner+"/"+r.repo, r.source, r.note)
	}
	matched := ok + manual
	total := matched + fail + partial
	fmt.Printf("\n%d/%d matched (%d/%d auto-detected, %d via changelog_config.json), %d partial (best-effort), %d failed, %d skipped\n",
		matched, total, ok, matched, manual, partial, fail, skip)
}

func topFormulae(n int) ([]string, error) {
	resp, err := http.Get("https://formulae.brew.sh/api/analytics/install-on-request/365d.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var parsed analyticsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if n > len(parsed.Items) {
		n = len(parsed.Items)
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = parsed.Items[i].Formula
	}
	return names, nil
}

// filterUntapped splits names into those `brew info` can resolve locally
// and those that need a tap that isn't installed. A single unresolvable
// name (e.g. "hashicorp/tap/terraform" without the hashicorp/tap tap
// added) makes `brew info`'s whole batch call fail with no output at all,
// so third-party-tap formulae have to be screened out up front rather than
// relying on brew to report which name was the problem.
func filterUntapped(names []string) (resolvable []string, skipped []result, err error) {
	out, err := exec.Command("brew", "tap").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("brew tap: %w", err)
	}
	taps := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			taps[line] = true
		}
	}

	for _, name := range names {
		parts := strings.Split(name, "/")
		if len(parts) < 3 {
			resolvable = append(resolvable, name)
			continue
		}
		tap := parts[0] + "/" + parts[1]
		if taps[tap] {
			resolvable = append(resolvable, name)
		} else {
			skipped = append(skipped, result{name: name, source: "skip-tap", skipped: true, note: fmt.Sprintf("tap %q not installed", tap)})
		}
	}
	return resolvable, skipped, nil
}

func validate(name string, info homebrew.Info) result {
	r := result{name: name}

	owner, repo, ok := ghsource.ResolveRepo(info.Homepage, info.URL)
	if !ok {
		r.source = "no-repo"
		r.note = fmt.Sprintf("homepage=%s url=%s", info.Homepage, info.URL)
		return r
	}
	r.owner, r.repo = owner, repo

	filename, useReleases := ghsource.ResolveChangelogSource(owner, repo)
	if useReleases {
		r.source = "releases"
		releases, err := ghsource.FetchReleases(owner, repo, "", info.Version)
		if err != nil {
			r.note = "fetch error: " + err.Error()
			return r
		}
		if len(releases) == 0 {
			r.note = "no releases found"
			return r
		}
		if last := releases[len(releases)-1]; ghsource.TagMatchesVersion(last.TagName, info.Version) {
			r.found = true
			r.note = fmt.Sprintf("matched tag=%s", last.TagName)
			return r
		}
		r.partial = true
		r.note = fmt.Sprintf("tag for %s not found; showing %d most recent releases (latest=%s)", info.Version, len(releases), releases[0].TagName)
		return r
	}

	r.source = "file:" + filename
	section, found := ghsource.FetchChangelogSection(owner, repo, filename, info.Version, "", changelogconfig.LaxHeadings(name))
	r.found = found
	if !found {
		r.note = "version heading not located"
	} else {
		r.note = fmt.Sprintf("%d bytes", len(section))
	}
	return r
}
