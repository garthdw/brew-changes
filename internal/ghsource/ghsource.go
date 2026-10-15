// Package ghsource resolves a package's GitHub repository and fetches its
// changelog file or release notes over the GitHub REST API.
package ghsource

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"

	"github.com/garthdw/brew-changes/internal/changelog"
	"github.com/garthdw/brew-changes/internal/changelogconfig"
	"github.com/garthdw/brew-changes/internal/version"
)

var (
	changelogBaseNames  = []string{"CHANGELOG", "CHANGES", "HISTORY", "NEWS"}
	changelogExtensions = []string{".md", ".rst", ""}
	changelogFilenames  = buildChangelogFilenames()
)

// buildChangelogFilenames expands changelogBaseNames x changelogExtensions.
func buildChangelogFilenames() []string {
	filenames := make([]string, 0, len(changelogBaseNames)*len(changelogExtensions))
	for _, name := range changelogBaseNames {
		for _, ext := range changelogExtensions {
			filenames = append(filenames, name+ext)
		}
	}
	return filenames
}

// apiBaseURL is the GitHub REST API base, overridable in tests.
var apiBaseURL = "https://api.github.com"

// githubURLPattern matches a github.com (https or ssh) or
// raw.githubusercontent.com URL, capturing the host, owner, repo, and any
// path after them. The repo group is lazy so it stops at the shortest
// prefix that leaves a valid trailing ".git" and/or "/..." path — this lets
// repo names contain dots themselves (e.g. "llama.cpp", "socket.io") rather
// than treating the first dot as the start of a ".git" suffix. The host
// must be exactly github.com (optionally www.), not a subdomain like
// docs.github.com.
var githubURLPattern = regexp.MustCompile(`(?:^|//|@|//www\.)(github\.com[:/]|raw\.githubusercontent\.com/)([^/]+)/([^/]+?)(\.git)?(/.*)?$`)

// parseGitHubURL splits a GitHub URL into owner, repo, and whatever path
// follows them. raw reports a raw.githubusercontent.com URL, whose path is
// "/<ref>/<file>" rather than github.com's "/blob/<ref>/<file>".
func parseGitHubURL(u string) (owner, repo, rest string, raw, ok bool) {
	m := githubURLPattern.FindStringSubmatch(u)
	if m == nil {
		return "", "", "", false, false
	}
	return m[2], m[3], m[5], strings.HasPrefix(m[1], "raw."), true
}

// resolveRepo extracts owner and repo from whichever of homepage or url
// points at GitHub, preferring homepage. ok is false if neither does.
func resolveRepo(homepage, url string) (owner, repo string, ok bool) {
	for _, u := range []string{homepage, url} {
		if owner, repo, _, _, ok = parseGitHubURL(u); ok {
			return owner, repo, true
		}
	}
	return "", "", false
}

// Source is where a package's changelog comes from, and how to read it.
type Source struct {
	Owner string // GitHub owner, empty if no repo is known
	Repo  string // GitHub repo name

	// ManualURL is a manually curated changelog/release-notes link (see
	// internal/changelogconfig), used instead of any GitHub repo. Empty if
	// none is configured.
	ManualURL string

	// LaxHeadings relaxes version-heading matching (see
	// changelog.TrimToRange) for changelogs that mark version sections with
	// plain, unmarked lines rather than Markdown headings.
	LaxHeadings bool

	// PrereleasesOnly keeps only GitHub-flagged pre-releases, for preview
	// channels (e.g. zed@preview) whose release history is interleaved with
	// the stable channel's.
	PrereleasesOnly bool
}

// ResolveSource determines where the named package's changelog comes from:
// changelogconfig's curated entry if one names a URL or repo, otherwise a
// GitHub repo auto-detected from Homebrew's homepage/url metadata.
func ResolveSource(name, version, homepage, url string) Source {
	e := changelogconfig.Get(name)
	s := Source{ManualURL: changelogconfig.ExpandURL(e.URL, version), LaxHeadings: e.LaxHeadings, PrereleasesOnly: e.Prerelease}
	switch {
	case e.Repo != "":
		s.Owner, s.Repo, _ = strings.Cut(e.Repo, "/")
	case e.URL == "":
		s.Owner, s.Repo, _ = resolveRepo(homepage, url)
	}
	return s
}

// Origin says which source a Changelog came from.
type Origin int

const (
	FromNone     Origin = iota // no manual URL or GitHub repo is known
	FromManual                 // Source.ManualURL
	FromFile                   // a changelog file in the GitHub repo
	FromReleases               // the repo's GitHub releases (or tags)
)

// Changelog is the outcome of Source.Fetch.
type Changelog struct {
	Origin Origin
	// Filename is the changelog file found in the repo. It's set even when
	// that file didn't document the version and releases were used instead.
	Filename string
	Section  string    // trimmed changelog section, for FromManual and FromFile
	Releases []Release // newest first, for FromReleases
	// Found reports whether Section or Releases document the target version.
	Found bool
	Err   error // the manual URL or releases couldn't be fetched (see FetchManualSection)
}

// Fetch resolves and fetches s's changelog for an upgrade from installed to
// current: the manual URL if configured, otherwise the repo's changelog
// file, falling back to its releases when there's no file or it doesn't
// document current (e.g. a stale file left behind after switching to
// GitHub Releases). onStage, if non-nil, is called before each
// network stage after the first with the changelog filename or "releases",
// so callers can show progress.
func (s Source) Fetch(installed, current string, onStage func(stage string)) Changelog {
	if onStage == nil {
		onStage = func(string) {}
	}
	if s.ManualURL != "" {
		section, found, err := FetchManualSection(s.ManualURL, current, installed, s.LaxHeadings)
		return Changelog{Origin: FromManual, Section: section, Found: found, Err: err}
	}
	if s.Owner == "" {
		return Changelog{Origin: FromNone}
	}

	c := Changelog{Origin: FromReleases}
	if filename, found, _ := resolveChangelogFilename(s.Owner, s.Repo); found {
		c.Filename = filename
		onStage(filename)
		if section, ok := FetchChangelogSection(s.Owner, s.Repo, filename, current, installed, s.LaxHeadings); ok {
			c.Origin, c.Section, c.Found = FromFile, section, true
			return c
		}
	}

	onStage("releases")
	c.Releases, c.Err = FetchReleases(s.Owner, s.Repo, installed, current, s.PrereleasesOnly)
	c.Found = slices.ContainsFunc(c.Releases, func(r Release) bool { return version.MatchesTag(r.TagName, current) })
	return c
}

var (
	tokenOnce  sync.Once
	tokenValue string
)

// token returns a GitHub API token, preferring GITHUB_TOKEN/GH_TOKEN, then
// falling back to `gh auth token` if the gh CLI happens to be available.
// Returns "" if no token could be found (requests proceed unauthenticated).
// The result is resolved once per process and cached, since it can require
// spawning the gh CLI and doesn't change during a run.
func token() string {
	tokenOnce.Do(func() {
		for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
			if v := os.Getenv(env); v != "" {
				tokenValue = v
				return
			}
		}
		if _, err := exec.LookPath("gh"); err == nil {
			if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
				tokenValue = strings.TrimSpace(string(out))
			}
		}
	})
	return tokenValue
}

// httpClient is used for every GitHub API request. It sets an overall
// timeout because http.DefaultClient has none: a connection that opens but
// then stalls (flaky wifi, VPN, captive portal, ...) would otherwise hang
// the request forever with no way for the caller to retry or abort.
var httpClient = &http.Client{Timeout: 15 * time.Second}

func get(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if t := token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return httpClient.Do(req)
}

// getJSON fetches url and unmarshals the JSON response body into out.
func getJSON(url string, out any) error {
	_, err := getJSONPage(url, out)
	return err
}

// getJSONPage is getJSON for paginated endpoints, additionally returning
// the Link header's next-page URL (empty on the last page).
func getJSONPage(url string, out any) (next string, err error) {
	resp, err := get(url)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return "", fmt.Errorf("parsing %s: %w", url, err)
	}
	return nextPageURL(resp.Header.Get("Link")), nil
}

type contentsResponse struct {
	Content string `json:"content"`
}

// fetchGitHubFile fetches a file via the Contents API, falling back to a
// raw.githubusercontent.com request if that fails — the Contents API
// returns no content for files over 1MB (e.g. aws-cli's CHANGELOG.rst). An
// empty ref uses the default branch ("HEAD" for the raw fallback).
func fetchGitHubFile(owner, repo, path, ref string) (string, error) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s", apiBaseURL, owner, repo, path)
	if ref != "" {
		apiURL += "?ref=" + ref
	}
	var parsed contentsResponse
	if err := getJSON(apiURL, &parsed); err == nil {
		if decoded, derr := base64.StdEncoding.DecodeString(strings.ReplaceAll(parsed.Content, "\n", "")); derr == nil && len(decoded) > 0 {
			return string(decoded), nil
		}
	}

	rawRef := ref
	if rawRef == "" {
		rawRef = "HEAD"
	}
	content, _, err := fetchRaw(fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, rawRef, path))
	return content, err
}

// parseGitHubFileURL extracts owner/repo/ref/path from a github.com
// blob/raw URL or a raw.githubusercontent.com URL, so the file can be
// fetched through the authenticated Contents API instead of an anonymous
// GET.
func parseGitHubFileURL(rawURL string) (owner, repo, ref, path string, ok bool) {
	owner, repo, rest, raw, ok := parseGitHubURL(rawURL)
	if !ok {
		return "", "", "", "", false
	}
	rest = strings.TrimPrefix(rest, "/")
	if !raw {
		kind, after, _ := strings.Cut(rest, "/")
		if kind != "blob" && kind != "raw" {
			return "", "", "", "", false
		}
		rest = after
	}
	ref, path, ok = strings.Cut(rest, "/")
	if !ok || ref == "" || path == "" {
		return "", "", "", "", false
	}
	return owner, repo, ref, path, true
}

// rawFetchAttempts bounds retries for fetchRaw's transient-failure retry:
// raw.githubusercontent.com in particular has been observed to 503
// intermittently even when the file is perfectly reachable.
const rawFetchAttempts = 3

// ErrHTML is returned by FetchManualSection for an HTML page that doesn't
// document the requested version.
var ErrHTML = errors.New("changelog is an HTML page that doesn't document this version")

// fetchRaw fetches an arbitrary URL as text. HTML pages are converted to
// Markdown, so their <h2>-style headings become ATX headings TrimToRange
// can match, and isHTML reports that. Only transient failures (network
// errors, 5xx) are retried — a 404 won't change on retry.
func fetchRaw(rawURL string) (content string, isHTML bool, err error) {
	for attempt := range rawFetchAttempts {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		var retryable bool
		if content, isHTML, retryable, err = fetchRawOnce(rawURL); err == nil || !retryable {
			break
		}
	}
	return content, isHTML, err
}

func fetchRawOnce(rawURL string) (content string, isHTML, retryable bool, err error) {
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return "", false, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, resp.StatusCode >= 500, fmt.Errorf("GET %s: status %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, true, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "html") {
		return string(body), false, false, nil
	}
	content, err = htmltomarkdown.ConvertString(string(body), converter.WithDomain(rawURL))
	return content, true, false, err
}

// FetchManualSection fetches a manually-configured changelog URL (see
// changelogconfig) and trims it to the section documenting newVersion. err
// is set if the URL couldn't be retrieved, or is ErrHTML for an HTML page
// (e.g. Python's or PHP's changelogs) that doesn't document newVersion,
// since its leading lines would just be site navigation. Otherwise found is
// false if the content doesn't document newVersion under a recognized
// heading, in which case section holds a fallback excerpt.
func FetchManualSection(rawURL, newVersion, installedVersion string, lax bool) (section string, found bool, err error) {
	var content string
	var isHTML bool
	if owner, repo, ref, path, isGitHub := parseGitHubFileURL(rawURL); isGitHub {
		content, err = fetchGitHubFile(owner, repo, path, ref)
	} else {
		content, isHTML, err = fetchRaw(rawURL)
	}
	if err != nil {
		return "", false, err
	}
	section, found = changelog.TrimToRange(content, newVersion, installedVersion, lax)
	if isHTML && !found {
		return "", false, ErrHTML
	}
	return section, found, nil
}

// FetchChangelogSection fetches filename's content and trims it to the
// section documenting newVersion. ok is false if either the fetch fails, or
// the file doesn't actually cover newVersion (e.g. an index-style changelog
// like Node.js's root CHANGELOG.md, which just links out to per-major-
// version files rather than documenting versions itself) — either way, the
// caller should treat this source as unusable and fall back to another one.
// lax relaxes version-heading matching for formulae recorded as needing it
// in changelogconfig (see changelog.TrimToRange).
func FetchChangelogSection(owner, repo, filename, newVersion, installedVersion string, lax bool) (section string, ok bool) {
	content, err := fetchGitHubFile(owner, repo, filename, "")
	if err != nil {
		return "", false
	}
	return changelog.TrimToRange(content, newVersion, installedVersion, lax)
}

type repoContentEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// resolveChangelogFilename lists the repo's root directory in a single
// request and returns the well-known changelog filename present there. This
// avoids probing each candidate filename with its own round trip (which can
// add seconds of latency on repos where the first few candidates all miss).
// If more than one candidate exists, the most recently committed one wins
// (see lastCommitDate), on the theory that it's the one still maintained.
// found is false if none of the candidates exist; err is set only if the
// listing request itself failed.
func resolveChangelogFilename(owner, repo string) (filename string, found bool, err error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/", apiBaseURL, owner, repo)
	var entries []repoContentEntry
	if err := getJSON(url, &entries); err != nil {
		return "", false, fmt.Errorf("listing contents for %s/%s: %w", owner, repo, err)
	}
	present := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Type == "file" {
			present[e.Name] = true
		}
	}

	var matches []string
	for _, name := range changelogFilenames {
		if present[name] {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return "", false, nil
	case 1:
		return matches[0], true, nil
	default:
		return mostRecentlyCommitted(owner, repo, matches), true, nil
	}
}

// mostRecentlyCommitted returns whichever of the given filenames (assumed
// to all exist in the repo) has the most recent commit touching it, fetched
// concurrently since each lookup is an independent request.
func mostRecentlyCommitted(owner, repo string, filenames []string) string {
	dates := make([]time.Time, len(filenames))
	var wg sync.WaitGroup
	for i, name := range filenames {
		wg.Go(func() { dates[i] = lastCommitDate(owner, repo, name) })
	}
	wg.Wait()

	best := 0
	for i := 1; i < len(filenames); i++ {
		if dates[i].After(dates[best]) {
			best = i
		}
	}
	return filenames[best]
}

type commitEntry struct {
	Commit struct {
		Committer struct {
			Date string `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

// lastCommitDate returns the timestamp of the most recent commit touching
// filename, or the zero time if it can't be determined.
func lastCommitDate(owner, repo, filename string) time.Time {
	url := fmt.Sprintf("%s/repos/%s/%s/commits?path=%s&per_page=1", apiBaseURL, owner, repo, filename)
	var commits []commitEntry
	if err := getJSON(url, &commits); err != nil || len(commits) == 0 {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, commits[0].Commit.Committer.Date)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Release is a single GitHub release.
type Release struct {
	TagName     string `json:"tag_name"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
	Prerelease  bool   `json:"prerelease"`
}

const maxReleasePages = 3

// FetchReleases returns releases newer than installedVersion, newest first,
// stopping at the release matching installedVersion and otherwise capping
// out at 20. If targetVersion is non-empty, the single page of up to 100
// releases is searched for it instead of capping at 20 while accumulating;
// if the search reaches the end of that page without finding
// installedVersion, the result is truncated back down to 20 as a
// best-effort fallback rather than treated as an error.
//
// Some repos (docker/cli, vim/vim, ...) never publish GitHub Releases at
// all — they just tag commits directly. When the Releases API comes back
// completely empty (as opposed to "no releases newer than installed"),
// FetchReleases falls back to the plain git tags API, which GitHub returns
// newest-first the same way and which version.MatchesTag can filter
// identically. The fallback only has tag names to work with, so the
// resulting Releases have empty PublishedAt/Body.
func FetchReleases(owner, repo, installedVersion, targetVersion string, prereleasesOnly bool) ([]Release, error) {
	releasesURL := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", apiBaseURL, owner, repo)
	filtered, sawAny, err := fetchPages(releasesURL, func(r Release) Release { return r }, installedVersion, targetVersion, prereleasesOnly)
	if err != nil {
		return nil, err
	}
	if !sawAny {
		tagsURL := fmt.Sprintf("%s/repos/%s/%s/tags?per_page=100", apiBaseURL, owner, repo)
		filtered, _, err = fetchPages(tagsURL, func(t tagEntry) Release { return Release{TagName: t.Name} }, installedVersion, targetVersion, prereleasesOnly)
		return filtered, err
	}
	return findTargetIfMissing(owner, repo, filtered, targetVersion, prereleasesOnly), nil
}

// findTargetIfMissing handles targetVersion missing from filtered. A just-
// published release can be absent from the paginated /releases list (its
// search index lags) though /releases/latest reflects it, so that's checked
// and appended on a match. Otherwise the target is usually just older than
// the whole first page (llama.cpp's per-commit builds, gemini-cli's
// nightlies), making filtered irrelevant, so the target is fetched by its
// likely tag names and returned alone. For pre-release channels, whose
// releases /releases/latest never returns, only "-pre" tags are tried.
func findTargetIfMissing(owner, repo string, filtered []Release, targetVersion string, prereleasesOnly bool) []Release {
	if targetVersion == "" {
		return filtered
	}
	for _, r := range filtered {
		if version.MatchesTag(r.TagName, targetVersion) {
			return filtered
		}
	}
	tags := []string{"v" + targetVersion + "-pre", targetVersion + "-pre"}
	if !prereleasesOnly {
		url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBaseURL, owner, repo)
		var latest Release
		if err := getJSON(url, &latest); err == nil && version.MatchesTag(latest.TagName, targetVersion) {
			return append(filtered, latest)
		}
		tags = []string{"v" + targetVersion, targetVersion}
	}
	for _, tag := range tags {
		var r Release
		if err := getJSON(fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", apiBaseURL, owner, repo, tag), &r); err == nil && (r.Prerelease || !prereleasesOnly) {
			return []Release{r}
		}
	}
	return filtered
}

// accumulateRelease appends r to filtered and reports whether the caller
// should stop paging: once installedVersion is matched (r itself is
// excluded, since everything from here on is already installed), or, when
// targetVersion is empty, once the 20-item cap is reached.
//
// When installedVersion is empty there's no other bound to walk to, so a
// targetVersion match stops the search there instead (used when hunting for
// a tag that might sit past the old 20-item cap). With installedVersion
// set — the normal case — a target match must NOT stop the walk: target is
// usually the newest release, and stopping there would silently drop any
// older-but-still-unreleased versions between it and installedVersion (e.g.
// an upgrade that skipped a release).
func accumulateRelease(filtered []Release, r Release, installedVersion, targetVersion string) ([]Release, bool) {
	if version.MatchesTag(r.TagName, installedVersion) {
		return filtered, true
	}
	filtered = append(filtered, r)
	if installedVersion == "" && version.MatchesTag(r.TagName, targetVersion) {
		return filtered, true
	}
	if targetVersion == "" && len(filtered) >= 20 {
		return filtered, true
	}
	return filtered, false
}

type tagEntry struct {
	Name string `json:"name"`
}

// fetchPages walks a newest-first list endpoint (releases, or tags for
// repos that don't use GitHub Releases), feeding each entry through
// accumulateRelease, skipping non-pre-releases if prereleasesOnly. Only the
// first page is fetched when targetVersion is set. sawAny reports whether
// the endpoint returned any entries at all.
func fetchPages[T any](url string, toRelease func(T) Release, installedVersion, targetVersion string, prereleasesOnly bool) (filtered []Release, sawAny bool, err error) {
	pages := maxReleasePages
	if targetVersion != "" {
		pages = 1
	}

	for page := 0; page < pages && url != ""; page++ {
		var entries []T
		if url, err = getJSONPage(url, &entries); err != nil {
			return nil, false, err
		}
		sawAny = sawAny || len(entries) > 0
		for _, e := range entries {
			r := toRelease(e)
			if prereleasesOnly && !r.Prerelease {
				continue
			}
			var stop bool
			filtered, stop = accumulateRelease(filtered, r, installedVersion, targetVersion)
			if stop {
				return filtered, sawAny, nil
			}
		}
	}

	if targetVersion != "" && len(filtered) > 20 {
		filtered = filtered[:20]
	}
	return filtered, sawAny, nil
}

var linkNextPattern = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextPageURL(linkHeader string) string {
	m := linkNextPattern.FindStringSubmatch(linkHeader)
	if m == nil {
		return ""
	}
	return m[1]
}
