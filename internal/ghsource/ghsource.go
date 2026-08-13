// Package ghsource resolves a package's GitHub repository and fetches its
// changelog file or release notes over the GitHub REST API.
package ghsource

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/garthdw/homebrew-changes/internal/changelog"
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

// githubURLPattern matches a GitHub owner/repo out of a URL. The repo
// group is lazy so it stops at the shortest prefix that leaves a valid
// trailing ".git" and/or "/..." path — this lets repo names contain dots
// themselves (e.g. "llama.cpp", "socket.io") rather than treating the
// first dot as the start of a ".git" suffix.
var githubURLPattern = regexp.MustCompile(`github\.com[:/]([^/]+)/([^/]+?)(\.git)?(/.*)?$`)

// ResolveRepo extracts "owner" and "repo" from a URL pointing at
// github.com, trying homepage first and falling back to url. ok is false if
// neither points at GitHub.
func ResolveRepo(homepage, url string) (owner, repo string, ok bool) {
	if owner, repo, ok = repoFromURL(homepage); ok {
		return owner, repo, true
	}
	return repoFromURL(url)
}

func repoFromURL(url string) (owner, repo string, ok bool) {
	m := githubURLPattern.FindStringSubmatch(url)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
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

func get(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if t := token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return http.DefaultClient.Do(req)
}

// getJSON fetches url and unmarshals the JSON response body into out.
func getJSON(url string, out any) error {
	resp, err := get(url)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return json.Unmarshal(body, out)
}

type contentsResponse struct {
	Content string `json:"content"`
}

// FetchChangelogFileAt fetches a single named file's content from the
// repo's default branch.
func FetchChangelogFileAt(owner, repo, filename string) (content string, ok bool) {
	return fetchGitHubFile(owner, repo, filename, "")
}

// fetchGitHubFile fetches a file via the Contents API, falling back to a
// raw.githubusercontent.com request if that fails — the Contents API
// returns no content for files over 1MB (e.g. aws-cli's CHANGELOG.rst). An
// empty ref uses the default branch ("HEAD" for the raw fallback).
func fetchGitHubFile(owner, repo, path, ref string) (content string, ok bool) {
	apiURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s", apiBaseURL, owner, repo, path)
	if ref != "" {
		apiURL += "?ref=" + ref
	}
	var parsed contentsResponse
	if err := getJSON(apiURL, &parsed); err == nil {
		if decoded, derr := base64.StdEncoding.DecodeString(strings.ReplaceAll(parsed.Content, "\n", "")); derr == nil && len(decoded) > 0 {
			return string(decoded), true
		}
	}

	rawRef := ref
	if rawRef == "" {
		rawRef = "HEAD"
	}
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, rawRef, path)
	return FetchRaw(rawURL)
}

var (
	githubFileURLPattern      = regexp.MustCompile(`^https?://(?:www\.)?github\.com/([^/]+)/([^/]+)/(?:blob|raw)/([^/]+)/(.+)$`)
	rawGithubUsercontentURLRe = regexp.MustCompile(`^https?://raw\.githubusercontent\.com/([^/]+)/([^/]+)/([^/]+)/(.+)$`)
)

// parseGitHubFileURL extracts owner/repo/ref/path from a github.com
// blob/raw URL or a raw.githubusercontent.com URL (two patterns since the
// hosts don't share a path shape), so the file can be fetched through the
// authenticated Contents API instead of an anonymous GET.
func parseGitHubFileURL(rawURL string) (owner, repo, ref, path string, ok bool) {
	if m := githubFileURLPattern.FindStringSubmatch(rawURL); m != nil {
		return m[1], m[2], m[3], m[4], true
	}
	if m := rawGithubUsercontentURLRe.FindStringSubmatch(rawURL); m != nil {
		return m[1], m[2], m[3], m[4], true
	}
	return "", "", "", "", false
}

// rawFetchAttempts bounds retries for FetchRaw's transient-failure retry:
// raw.githubusercontent.com in particular has been observed to 503
// intermittently even when the file is perfectly reachable.
const rawFetchAttempts = 3

// FetchRaw fetches an arbitrary URL as plain text. ok is false if the
// response looks like HTML (unusable by changelog.TrimToRange) or every
// attempt fails. Only transient failures (network errors, 5xx) are
// retried — a 404 or HTML response won't change on retry.
func FetchRaw(rawURL string) (content string, ok bool) {
	for attempt := 0; attempt < rawFetchAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		var retryable bool
		content, ok, retryable = fetchRawOnce(rawURL)
		if ok || !retryable {
			return content, ok
		}
	}
	return "", false
}

func fetchRawOnce(rawURL string) (content string, ok bool, retryable bool) {
	resp, err := http.Get(rawURL)
	if err != nil {
		return "", false, true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, resp.StatusCode >= 500
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "html") {
		return "", false, false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, true
	}
	return string(body), true, false
}

// FetchManualSection fetches a manually-configured changelog URL (see
// changelogconfig) and trims it to the section documenting newVersion.
func FetchManualSection(rawURL, newVersion, installedVersion string, lax bool) (section string, ok bool) {
	var content string
	if owner, repo, ref, path, isGitHub := parseGitHubFileURL(rawURL); isGitHub {
		content, ok = fetchGitHubFile(owner, repo, path, ref)
	} else {
		content, ok = FetchRaw(rawURL)
	}
	if !ok {
		return "", false
	}
	return changelog.TrimToRange(content, newVersion, installedVersion, lax)
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
	content, ok := FetchChangelogFileAt(owner, repo, filename)
	if !ok {
		return "", false
	}
	return changelog.TrimToRange(content, newVersion, installedVersion, lax)
}

type repoContentEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ResolveChangelogFilename lists the repo's root directory in a single
// request and returns the well-known changelog filename present there. This
// avoids probing each candidate filename with its own round trip (which can
// add seconds of latency on repos where the first few candidates all miss).
// If more than one candidate exists, the most recently committed one wins
// (see lastCommitDate), on the theory that it's the one still maintained.
// found is false if none of the candidates exist; err is set only if the
// listing request itself failed.
func ResolveChangelogFilename(owner, repo string) (filename string, found bool, err error) {
	name, _, found, err := resolveChangelogFilename(owner, repo)
	return name, found, err
}

// resolveChangelogFilename is ResolveChangelogFilename's implementation. It
// additionally returns the winning candidate's last-commit date when
// already known — i.e. when mostRecentlyCommitted had to fetch it to break
// a tie between multiple candidates — so ResolveChangelogSource can reuse
// that date instead of re-fetching it. date is the zero value when there
// was only one candidate (no tie-break needed, so no date was fetched).
func resolveChangelogFilename(owner, repo string) (filename string, date time.Time, found bool, err error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/", apiBaseURL, owner, repo)
	var entries []repoContentEntry
	if err := getJSON(url, &entries); err != nil {
		return "", time.Time{}, false, fmt.Errorf("listing contents for %s/%s: %w", owner, repo, err)
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
		return "", time.Time{}, false, nil
	case 1:
		return matches[0], time.Time{}, true, nil
	default:
		name, d := mostRecentlyCommitted(owner, repo, matches)
		return name, d, true, nil
	}
}

// mostRecentlyCommitted returns whichever of the given filenames (assumed
// to all exist in the repo) has the most recent commit touching it, and
// that commit's date, fetched concurrently since each lookup is an
// independent request.
func mostRecentlyCommitted(owner, repo string, filenames []string) (string, time.Time) {
	dates := make([]time.Time, len(filenames))
	var wg sync.WaitGroup
	for i, name := range filenames {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			dates[i] = lastCommitDate(owner, repo, name)
		}(i, name)
	}
	wg.Wait()

	best := 0
	for i := 1; i < len(filenames); i++ {
		if dates[i].After(dates[best]) {
			best = i
		}
	}
	return filenames[best], dates[best]
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

// FetchChangelogFile resolves and fetches the repo's changelog file,
// returning the filename found and its content.
func FetchChangelogFile(owner, repo string) (filename, content string, ok bool) {
	name, found, err := ResolveChangelogFilename(owner, repo)
	if err != nil || !found {
		return "", "", false
	}
	content, ok = FetchChangelogFileAt(owner, repo, name)
	if !ok {
		return "", "", false
	}
	return name, content, true
}

// ResolveChangelogSource decides between a repo's changelog file and its
// GitHub releases: some repos stop maintaining a CHANGELOG.md (or similar)
// after switching to GitHub Releases, leaving the stale file in place. If a
// changelog file exists, its last commit date is compared against the
// latest release's publish date; releases win if they're newer. useReleases
// is also true if no changelog file was found, or its listing couldn't be
// fetched.
func ResolveChangelogSource(owner, repo string) (filename string, useReleases bool) {
	name, fileDate, found, err := resolveChangelogFilename(owner, repo)
	if err != nil || !found {
		return "", true
	}

	var releaseDate time.Time
	if !fileDate.IsZero() {
		// Multiple candidates existed, so resolveChangelogFilename already
		// fetched the winning file's commit date while breaking the tie;
		// only the release date is still needed.
		releaseDate = latestReleaseDate(owner, repo)
	} else {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			fileDate = lastCommitDate(owner, repo, name)
		}()
		go func() {
			defer wg.Done()
			releaseDate = latestReleaseDate(owner, repo)
		}()
		wg.Wait()
	}

	if !releaseDate.IsZero() && releaseDate.After(fileDate) {
		return "", true
	}
	return name, false
}

// latestReleaseDate returns the publish date of the repo's most recent
// release, or the zero time if it can't be determined.
func latestReleaseDate(owner, repo string) time.Time {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=1", apiBaseURL, owner, repo)
	var releases []Release
	if err := getJSON(url, &releases); err != nil || len(releases) == 0 {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, releases[0].PublishedAt)
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
}

const maxReleasePages = 3

// TagMatchesVersion reports whether a release tag identifies the given
// plain version string (e.g. Homebrew's "1.8.1"), regardless of whatever
// prefix the upstream project puts on its tags: "v1.8.1", "jq-1.8.1", and
// even "2024-edition-v1.8.1" all match "1.8.1".
//
// A tag matches if it ends with the version and the character immediately
// before that suffix is neither a digit nor '.'. That boundary check is
// what makes this safe where a naive "strip everything before the first
// digit" isn't: without it, version "2.3" would wrongly match tag "1.2.3"
// (the tail of a different, longer version number) since '.' would be
// mistaken for a valid prefix separator.
func TagMatchesVersion(tag, version string) bool {
	if version == "" {
		return false
	}
	if exactSuffixMatch(tag, version) {
		return true
	}
	return trailingComponentsMatch(tag, version)
}

func exactSuffixMatch(tag, version string) bool {
	if !strings.HasSuffix(tag, version) {
		return false
	}
	if len(tag) == len(version) {
		return true
	}
	boundary := tag[len(tag)-len(version)-1]
	return boundary != '.' && (boundary < '0' || boundary > '9')
}

var trailingVersionPattern = regexp.MustCompile(`[0-9]+(\.[0-9]+)*$`)

// trailingComponentsMatch compares tag's trailing dot-separated numeric run
// against version component-by-component, ignoring leading zeros in each
// component. This catches cases like yt-dlp, whose calendar-style versions
// are zero-padded in its GitHub tags ("2026.07.04") but not in Homebrew's
// reported version ("2026.7.4"), which exactSuffixMatch can't see past.
func trailingComponentsMatch(tag, version string) bool {
	loc := trailingVersionPattern.FindStringIndex(tag)
	if loc == nil {
		return false
	}
	if loc[0] > 0 {
		boundary := tag[loc[0]-1]
		if boundary == '.' || (boundary >= '0' && boundary <= '9') {
			return false
		}
	}

	tagParts := strings.Split(tag[loc[0]:], ".")
	versionParts := strings.Split(version, ".")
	if len(tagParts) != len(versionParts) {
		return false
	}
	for i := range tagParts {
		if normalizeVersionComponent(tagParts[i]) != normalizeVersionComponent(versionParts[i]) {
			return false
		}
	}
	return true
}

func normalizeVersionComponent(s string) string {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}

// FetchReleases returns releases newer than installedVersion, newest first,
// capped at a reasonable count. It stops as soon as it reaches the release
// matching installedVersion (see TagMatchesVersion), since everything after
// that in the newest-first list is already installed.
//
// Some repos (docker/cli, vim/vim, ...) never publish GitHub Releases at
// all — they just tag commits directly. When the Releases API comes back
// completely empty (as opposed to "no releases newer than installed"),
// FetchReleases falls back to the plain git tags API, which GitHub returns
// newest-first the same way and which TagMatchesVersion can filter
// identically. The fallback only has tag names to work with, so the
// resulting Releases have empty PublishedAt/Body.
func FetchReleases(owner, repo, installedVersion string) ([]Release, error) {
	filtered, sawAny, err := fetchReleasePages(owner, repo, installedVersion)
	if err != nil {
		return nil, err
	}
	if !sawAny {
		return fetchTags(owner, repo, installedVersion)
	}
	return filtered, nil
}

func fetchReleasePages(owner, repo, installedVersion string) ([]Release, bool, error) {
	var filtered []Release
	sawAny := false
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", apiBaseURL, owner, repo)

	for page := 0; page < maxReleasePages && url != "" && len(filtered) < 20; page++ {
		resp, err := get(url)
		if err != nil {
			return nil, false, fmt.Errorf("fetching releases for %s/%s: %w", owner, repo, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, false, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, false, fmt.Errorf("fetching releases for %s/%s: status %d", owner, repo, resp.StatusCode)
		}

		var pageReleases []Release
		if err := json.Unmarshal(body, &pageReleases); err != nil {
			return nil, false, fmt.Errorf("parsing releases for %s/%s: %w", owner, repo, err)
		}
		if len(pageReleases) > 0 {
			sawAny = true
		}
		for _, r := range pageReleases {
			if TagMatchesVersion(r.TagName, installedVersion) {
				return filtered, sawAny, nil
			}
			filtered = append(filtered, r)
			if len(filtered) >= 20 {
				return filtered, sawAny, nil
			}
		}

		url = nextPageURL(resp.Header.Get("Link"))
	}

	return filtered, sawAny, nil
}

type tagEntry struct {
	Name string `json:"name"`
}

// fetchTags mirrors fetchReleasePages against the tags API instead of
// releases, for repos that don't use GitHub Releases at all.
func fetchTags(owner, repo, installedVersion string) ([]Release, error) {
	var filtered []Release
	url := fmt.Sprintf("%s/repos/%s/%s/tags?per_page=100", apiBaseURL, owner, repo)

	for page := 0; page < maxReleasePages && url != "" && len(filtered) < 20; page++ {
		resp, err := get(url)
		if err != nil {
			return nil, fmt.Errorf("fetching tags for %s/%s: %w", owner, repo, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetching tags for %s/%s: status %d", owner, repo, resp.StatusCode)
		}

		var tags []tagEntry
		if err := json.Unmarshal(body, &tags); err != nil {
			return nil, fmt.Errorf("parsing tags for %s/%s: %w", owner, repo, err)
		}
		for _, t := range tags {
			if TagMatchesVersion(t.Name, installedVersion) {
				return filtered, nil
			}
			filtered = append(filtered, Release{TagName: t.Name})
			if len(filtered) >= 20 {
				return filtered, nil
			}
		}

		url = nextPageURL(resp.Header.Get("Link"))
	}

	return filtered, nil
}

var linkNextPattern = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextPageURL(linkHeader string) string {
	m := linkNextPattern.FindStringSubmatch(linkHeader)
	if m == nil {
		return ""
	}
	return m[1]
}
