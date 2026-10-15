// Command validate-changelogs is a standalone dev tool, not part of the
// shipped brew-changes binary (GoReleaser's build only points at
// ./cmd/brew-changes). It exercises this repo's changelog-resolution and
// version-matching logic against Homebrew's most-installed formulae (or,
// with -cask, casks), to catch regressions or gaps across a wide variety of
// real-world repos.
//
// Usage: go run ./cmd/validate-changelogs [-cask] [N]
// N defaults to 50.
package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/garthdw/brew-changes/internal/ghsource"
	"github.com/garthdw/brew-changes/internal/homebrew"
	"github.com/garthdw/brew-changes/internal/version"
)

type analyticsResponse struct {
	Items []struct {
		Formula string `json:"formula"`
		Cask    string `json:"cask"`
	} `json:"items"`
}

type result struct {
	name        string
	owner, repo string
	source      string // "file:<name>", "releases", "manual", "no-repo", "skip-tap"
	found       bool
	skipped     bool
	manual      bool // resolved via a changelog_config.json URL rather than auto-detection
	partial     bool // releases fetched, but the target version's tag wasn't among them (best effort)
	note        string
}

func main() {
	isCask := flag.Bool("cask", false, "validate the most-installed casks instead of formulae")
	flag.Parse()
	n := 50
	if v, err := strconv.Atoi(flag.Arg(0)); err == nil {
		n = v
		// flag stops at the first positional argument, so also accept
		// flags after N ("100 -cask").
		flag.CommandLine.Parse(flag.Args()[1:])
	}

	names, err := topPackages(n, *isCask)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetching analytics:", err)
		os.Exit(1)
	}

	resolvable, skipped, err := filterUntapped(names)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listing taps:", err)
		os.Exit(1)
	}

	infos, err := homebrew.ResolveInfoBatch(resolvable, *isCask)
	if err != nil {
		fmt.Fprintln(os.Stderr, "brew info:", err)
		os.Exit(1)
	}

	results := slices.Grow(skipped, len(infos))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for name, info := range infos {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			r := validate(name, info)
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		})
	}
	wg.Wait()

	slices.SortFunc(results, func(a, b result) int { return cmp.Compare(a.name, b.name) })

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

func topPackages(n int, isCask bool) ([]string, error) {
	url := "https://formulae.brew.sh/api/analytics/install-on-request/365d.json"
	if isCask {
		url = "https://formulae.brew.sh/api/analytics/cask-install/365d.json"
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var parsed analyticsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	names := make([]string, min(n, len(parsed.Items)))
	for i := range names {
		names[i] = cmp.Or(parsed.Items[i].Formula, parsed.Items[i].Cask)
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

// validate runs the same ghsource.Source.Fetch as the TUI and classifies
// the outcome. ErrHTML (an HTML page that doesn't document the version)
// counts as a failure, since the TUI can only show it as a link.
func validate(name string, info homebrew.Info) result {
	src := ghsource.ResolveSource(name, info.Version, info.Homepage, info.URL)
	r := result{name: name, owner: src.Owner, repo: src.Repo}
	c := src.Fetch("", info.Version, nil)
	r.found = c.Found
	switch c.Origin {
	case ghsource.FromNone:
		r.source = "no-repo"
		r.note = fmt.Sprintf("homepage=%s url=%s", info.Homepage, info.URL)
	case ghsource.FromManual:
		r.source, r.manual = "manual", c.Found
		switch {
		case errors.Is(c.Err, ghsource.ErrHTML):
			r.note = "link only (HTML lacks version): " + src.ManualURL
		case c.Err != nil:
			r.note = "fetch error: " + c.Err.Error()
		case !c.Found:
			r.note = "version heading not located: " + src.ManualURL
		default:
			r.note = fmt.Sprintf("%d bytes from %s", len(c.Section), src.ManualURL)
		}
	case ghsource.FromFile:
		r.source = "file:" + c.Filename
		r.note = fmt.Sprintf("%d bytes", len(c.Section))
	case ghsource.FromReleases:
		r.source = "releases"
		switch i := slices.IndexFunc(c.Releases, func(rel ghsource.Release) bool { return version.MatchesTag(rel.TagName, info.Version) }); {
		case c.Err != nil:
			r.note = "fetch error: " + c.Err.Error()
		case len(c.Releases) == 0:
			r.note = "no releases found"
		case i != -1:
			r.note = "matched tag=" + c.Releases[i].TagName
		default:
			r.partial = true
			r.note = fmt.Sprintf("tag for %s not found; showing %d most recent releases (latest=%s)", info.Version, len(c.Releases), c.Releases[0].TagName)
		}
		if c.Filename != "" {
			r.note = "fell back from " + c.Filename + "; " + r.note
		}
	}
	return r
}
