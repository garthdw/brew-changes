package ghsource

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := apiBaseURL
	apiBaseURL = srv.URL
	t.Cleanup(func() { apiBaseURL = orig })
	return srv
}

func TestResolveRepo(t *testing.T) {
	tests := []struct {
		name      string
		homepage  string
		url       string
		wantOwner string
		wantRepo  string
		wantOK    bool
	}{
		{
			name:      "homepage is github",
			homepage:  "https://github.com/owner/repo",
			url:       "https://example.com/tarball.tar.gz",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantOK:    true,
		},
		{
			name:      "falls back to url when homepage isn't github",
			homepage:  "https://example.com",
			url:       "https://github.com/owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantOK:    true,
		},
		{
			name:      "homepage with trailing path",
			homepage:  "https://github.com/owner/repo/issues",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantOK:    true,
		},
		{
			name:      "ssh-style url",
			url:       "git@github.com:owner/repo.git",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantOK:    true,
		},
		{
			name:      "repo name contains a dot",
			url:       "https://github.com/ggml-org/llama.cpp.git",
			wantOwner: "ggml-org",
			wantRepo:  "llama.cpp",
			wantOK:    true,
		},
		{
			name:     "neither points at github",
			homepage: "https://example.com",
			url:      "https://gitlab.com/owner/repo",
			wantOK:   false,
		},
		{
			name:   "both empty",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, ok := ResolveRepo(tt.homepage, tt.url)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if owner != tt.wantOwner || repo != tt.wantRepo {
				t.Errorf("got (%q, %q), want (%q, %q)", owner, repo, tt.wantOwner, tt.wantRepo)
			}
		})
	}
}

func TestParseGitHubFileURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantOwner string
		wantRepo  string
		wantRef   string
		wantPath  string
		wantOK    bool
	}{
		{
			name:      "github.com blob URL",
			url:       "https://github.com/dart-lang/sdk/blob/main/CHANGELOG.md",
			wantOwner: "dart-lang",
			wantRepo:  "sdk",
			wantRef:   "main",
			wantPath:  "CHANGELOG.md",
			wantOK:    true,
		},
		{
			name:      "github.com raw URL",
			url:       "https://github.com/owner/repo/raw/main/nested/dir/NEWS",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantRef:   "main",
			wantPath:  "nested/dir/NEWS",
			wantOK:    true,
		},
		{
			name:      "raw.githubusercontent.com URL",
			url:       "https://raw.githubusercontent.com/owner/repo/main/CHANGELOG.md",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantRef:   "main",
			wantPath:  "CHANGELOG.md",
			wantOK:    true,
		},
		{
			name:   "not a github URL",
			url:    "https://curl.se/changes.html",
			wantOK: false,
		},
		{
			name:   "github.com but not a file URL",
			url:    "https://github.com/owner/repo/releases",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, ref, path, ok := parseGitHubFileURL(tt.url)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if owner != tt.wantOwner || repo != tt.wantRepo || ref != tt.wantRef || path != tt.wantPath {
				t.Errorf("got (%q, %q, %q, %q), want (%q, %q, %q, %q)", owner, repo, ref, path, tt.wantOwner, tt.wantRepo, tt.wantRef, tt.wantPath)
			}
		})
	}
}

func TestFetchRaw(t *testing.T) {
	t.Run("plain text is returned", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, "## 1.2.3\n\nsome notes\n")
		}))
		defer srv.Close()

		content, ok, contentType := FetchRaw(srv.URL)
		if !ok {
			t.Fatalf("ok = false, want true")
		}
		if strings.Contains(contentType, "html") {
			t.Errorf("contentType = %q, want non-html", contentType)
		}
		if content != "## 1.2.3\n\nsome notes\n" {
			t.Errorf("got %q", content)
		}
	})

	t.Run("html response is rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<html><body>not a changelog</body></html>")
		}))
		defer srv.Close()

		_, ok, contentType := FetchRaw(srv.URL)
		if ok {
			t.Errorf("ok = true, want false for html content-type")
		}
		if !strings.Contains(contentType, "html") {
			t.Errorf("contentType = %q, want html", contentType)
		}
	})

	t.Run("non-200 status is rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		if _, ok, _ := FetchRaw(srv.URL); ok {
			t.Errorf("ok = true, want false for 404")
		}
	})
}

func TestFetchManualSection_NonGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "## 2.0.0\nnew stuff\n## 1.0.0\nold stuff\n")
	}))
	defer srv.Close()

	section, fetched, found, _ := FetchManualSection(srv.URL, "2.0.0", "1.0.0", false)
	if !fetched || !found {
		t.Fatalf("fetched = %v, found = %v, want true, true", fetched, found)
	}
	if section != "## 2.0.0\nnew stuff" {
		t.Errorf("got %q", section)
	}
}

func TestFetchManualSection_GitHubBlobURL(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		content := base64.StdEncoding.EncodeToString([]byte("## 2.0.0\nnew stuff\n## 1.0.0\nold stuff\n"))
		json.NewEncoder(w).Encode(contentsResponse{Content: content})
	})

	// FetchManualSection parses owner/repo/path from the URL itself and
	// hits apiBaseURL (overridden to srv.URL) rather than the URL's host,
	// so any github.com blob URL resolves through the test server.
	section, fetched, found, _ := FetchManualSection("https://github.com/owner/repo/blob/main/CHANGELOG.md", "2.0.0", "1.0.0", false)
	if !fetched || !found {
		t.Fatalf("fetched = %v, found = %v, want true, true", fetched, found)
	}
	if section != "## 2.0.0\nnew stuff" {
		t.Errorf("got %q", section)
	}
}

func rootListingHandler(names ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries := make([]repoContentEntry, len(names))
		for i, n := range names {
			entries[i] = repoContentEntry{Name: n, Type: "file"}
		}
		body, _ := json.Marshal(entries)
		w.Write(body)
	}
}

func TestFetchChangelogFile_PicksFirstMatch(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/contents/":
			rootListingHandler("README.md", "CHANGELOG.md")(w, r)
		case "/repos/o/r/contents/CHANGELOG.md":
			body, _ := json.Marshal(map[string]string{
				"content": base64.StdEncoding.EncodeToString([]byte("## 1.0.0\nnotes")),
			})
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	filename, content, ok := FetchChangelogFile("o", "r")
	if !ok {
		t.Fatal("expected ok = true")
	}
	if filename != "CHANGELOG.md" {
		t.Errorf("got filename %q, want CHANGELOG.md", filename)
	}
	if content != "## 1.0.0\nnotes" {
		t.Errorf("got content %q", content)
	}
}

func TestFetchChangelogFile_FallsThroughToLaterName(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/contents/":
			rootListingHandler("README.md", "HISTORY.md")(w, r)
		case "/repos/o/r/contents/HISTORY.md":
			body, _ := json.Marshal(map[string]string{
				"content": base64.StdEncoding.EncodeToString([]byte("history notes")),
			})
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	filename, content, ok := FetchChangelogFile("o", "r")
	if !ok || filename != "HISTORY.md" || content != "history notes" {
		t.Errorf("got (%q, %q, %v), want (HISTORY.md, \"history notes\", true)", filename, content, ok)
	}
}

func TestFetchChangelogFile_NoneFound(t *testing.T) {
	withTestServer(t, rootListingHandler("README.md", "LICENSE"))

	_, _, ok := FetchChangelogFile("o", "r")
	if ok {
		t.Error("expected ok = false when no changelog file exists")
	}
}

func TestResolveChangelogFilename_MultipleMatchesPicksMostRecentlyCommitted(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r/contents/":
			rootListingHandler("CHANGELOG.md", "NEWS.md")(w, r)
		case r.URL.Path == "/repos/o/r/commits" && r.URL.Query().Get("path") == "CHANGELOG.md":
			body, _ := json.Marshal([]map[string]any{
				{"commit": map[string]any{"committer": map[string]string{"date": "2020-01-01T00:00:00Z"}}},
			})
			w.Write(body)
		case r.URL.Path == "/repos/o/r/commits" && r.URL.Query().Get("path") == "NEWS.md":
			body, _ := json.Marshal([]map[string]any{
				{"commit": map[string]any{"committer": map[string]string{"date": "2026-01-01T00:00:00Z"}}},
			})
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	filename, found, err := ResolveChangelogFilename("o", "r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || filename != "NEWS.md" {
		t.Errorf("got (%q, %v), want (\"NEWS.md\", true)", filename, found)
	}
}

func TestResolveChangelogFilename_ListingError(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, found, err := ResolveChangelogFilename("o", "r")
	if err == nil {
		t.Error("expected error on non-200 listing response")
	}
	if found {
		t.Error("expected found = false on listing error")
	}
}

func changelogSourceServer(t *testing.T, changelogCommitDate, latestReleaseDate string) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r/contents/":
			rootListingHandler("CHANGELOG.md")(w, r)
		case r.URL.Path == "/repos/o/r/commits" && r.URL.Query().Get("path") == "CHANGELOG.md":
			body, _ := json.Marshal([]map[string]any{
				{"commit": map[string]any{"committer": map[string]string{"date": changelogCommitDate}}},
			})
			w.Write(body)
		case r.URL.Path == "/repos/o/r/releases":
			body, _ := json.Marshal([]map[string]string{{"published_at": latestReleaseDate, "tag_name": "v1"}})
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestResolveChangelogSource_PrefersReleasesWhenNewerThanChangelogFile(t *testing.T) {
	changelogSourceServer(t, "2020-01-01T00:00:00Z", "2026-01-01T00:00:00Z")

	filename, useReleases := ResolveChangelogSource("o", "r")
	if !useReleases || filename != "" {
		t.Errorf("got (%q, %v), want (\"\", true)", filename, useReleases)
	}
}

func TestResolveChangelogSource_PrefersChangelogFileWhenNewerThanReleases(t *testing.T) {
	changelogSourceServer(t, "2026-01-01T00:00:00Z", "2020-01-01T00:00:00Z")

	filename, useReleases := ResolveChangelogSource("o", "r")
	if useReleases || filename != "CHANGELOG.md" {
		t.Errorf("got (%q, %v), want (\"CHANGELOG.md\", false)", filename, useReleases)
	}
}

func TestFetchReleases_StopsAtInstalledAcrossPages(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/releases?per_page=100&page=2>; rel="next"`, srv.URL))
			body, _ := json.Marshal([]Release{
				{TagName: "v3.0.0", PublishedAt: "2024-03-01", Body: "third"},
				{TagName: "v2.0.0", PublishedAt: "2024-02-01", Body: "second"},
			})
			w.Write(body)
		case "2":
			// v0.5.0 is older than the installed v1.0.0 and must not appear:
			// fetching should stop as soon as it reaches the installed tag.
			body, _ := json.Marshal([]Release{
				{TagName: "v1.0.0", PublishedAt: "2024-01-01", Body: "first (installed)"},
				{TagName: "v0.5.0", Body: "older"},
			})
			w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	orig := apiBaseURL
	apiBaseURL = srv.URL
	t.Cleanup(func() { apiBaseURL = orig })

	releases, err := FetchReleases("o", "r", "1.0.0", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"v3.0.0", "v2.0.0"}
	if len(releases) != len(want) {
		t.Fatalf("got %+v, want tags %v", releases, want)
	}
	for i, tag := range want {
		if releases[i].TagName != tag {
			t.Errorf("release %d: got %q, want %q", i, releases[i].TagName, tag)
		}
	}
}

func TestFetchReleases_TargetVersionBeyondTwentyButWithinFirstPage(t *testing.T) {
	var page []Release
	for i := 50; i > 0; i-- {
		page = append(page, Release{TagName: fmt.Sprintf("v%d.0.0", i)})
	}
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(page)
		w.Write(body)
	})

	// v25.0.0 is the 26th release, past the old hard cap of 20, but still
	// well within the single page a target search fetches.
	releases, err := FetchReleases("o", "r", "", "25.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 26 {
		t.Fatalf("got %d releases, want 26 (down through the v25.0.0 match)", len(releases))
	}
	if last := releases[len(releases)-1]; last.TagName != "v25.0.0" {
		t.Errorf("last release = %q, want v25.0.0", last.TagName)
	}
}

func TestFetchReleases_SkippedVersionIncludesIntermediateRelease(t *testing.T) {
	page := []Release{
		{TagName: "v0.34.2", Body: "latest notes"},
		{TagName: "v0.34.1", Body: "skipped notes"},
		{TagName: "v0.34.0", Body: "installed notes"},
	}
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(page)
		w.Write(body)
	})

	// Reproduces an upgrade that skips a release (installed 0.34.0, current
	// 0.34.2): the target version (0.34.2) is the newest entry, so it must
	// not be treated as a stopping point — v0.34.1 in between also needs to
	// be included.
	releases, err := FetchReleases("o", "r", "0.34.0", "0.34.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 2 || releases[0].TagName != "v0.34.2" || releases[1].TagName != "v0.34.1" {
		t.Fatalf("got %+v, want [v0.34.2, v0.34.1]", releases)
	}
}

func TestFetchReleases_TargetVersionNotFound_FallsBackToTwentyWithoutASecondPage(t *testing.T) {
	var page []Release
	for i := 100; i > 0; i-- {
		page = append(page, Release{TagName: fmt.Sprintf("v%d.0.0", i)})
	}
	requests := 0
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/repos/o/r/releases/latest" {
			body, _ := json.Marshal(Release{TagName: "v100.0.0"})
			w.Write(body)
			return
		}
		w.Header().Set("Link", `<https://example.com/should-not-be-fetched>; rel="next"`)
		body, _ := json.Marshal(page)
		w.Write(body)
	})

	// "999.0.0" never appears anywhere in the page, so this exercises the
	// best-effort fallback: truncate to the usual 20 rather than treat a
	// missing tag as an error, and don't page further looking for it. It
	// also checks /releases/latest as a last resort (v100.0.0, which still
	// doesn't match), so that's the only extra request expected.
	releases, err := FetchReleases("o", "r", "", "999.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requests != 2 {
		t.Fatalf("got %d requests, want 2 (target search must not fetch a second list page, only the /releases/latest fallback)", requests)
	}
	if len(releases) != 20 {
		t.Fatalf("got %d releases, want 20 (best-effort fallback)", len(releases))
	}
	if releases[0].TagName != "v100.0.0" {
		t.Errorf("first release = %q, want v100.0.0", releases[0].TagName)
	}
}

func TestFetchReleases_TargetVersionMissingFromListButLatest_IsAppended(t *testing.T) {
	page := []Release{
		{TagName: "v4.6.6"},
		{TagName: "v4.6.5"},
	}
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/releases/latest" {
			body, _ := json.Marshal(Release{TagName: "v4.6.7", Body: "notes"})
			w.Write(body)
			return
		}
		body, _ := json.Marshal(page)
		w.Write(body)
	})

	// Reproduces a GitHub API quirk where a just-published release is
	// absent from the paginated /releases list (its search index lags)
	// even though /releases/latest already reflects it.
	releases, err := FetchReleases("o", "r", "4.6.6", "4.6.7")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 1 || releases[0].TagName != "v4.6.7" {
		t.Fatalf("got %+v, want single release v4.6.7 from the /releases/latest fallback", releases)
	}
}

func TestFetchReleases_TagPrefixOtherThanV(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal([]Release{
			{TagName: "jq-1.8.2", PublishedAt: "2026-06-20"},
			{TagName: "jq-1.8.1", PublishedAt: "2025-07-01"},
			{TagName: "jq-1.8.0", PublishedAt: "2025-06-01"},
		})
		w.Write(body)
	})

	releases, err := FetchReleases("o", "r", "1.8.1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 1 || releases[0].TagName != "jq-1.8.2" {
		t.Errorf("got %+v, want only jq-1.8.2", releases)
	}
}

func TestFetchReleases_FallsBackToTagsWhenNoReleases(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			w.Write([]byte("[]"))
		case "/repos/o/r/tags":
			body, _ := json.Marshal([]map[string]string{
				{"name": "v2.0.0"},
				{"name": "v1.0.0"},
			})
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	releases, err := FetchReleases("o", "r", "1.0.0", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 1 || releases[0].TagName != "v2.0.0" {
		t.Errorf("got %+v, want only v2.0.0 (from tags fallback)", releases)
	}
}

func TestFetchReleases_NoFallbackWhenReleasesExistButNoneAreNewer(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases":
			body, _ := json.Marshal([]Release{{TagName: "v1.0.0"}})
			w.Write(body)
		case "/repos/o/r/tags":
			t.Error("should not fall back to tags when releases exist")
			w.Write([]byte("[]"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	releases, err := FetchReleases("o", "r", "1.0.0", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 0 {
		t.Errorf("got %+v, want none (already up to date)", releases)
	}
}

func TestTagMatchesVersion(t *testing.T) {
	tests := []struct {
		tag, version string
		want         bool
	}{
		{"1.8.1", "1.8.1", true},
		{"v1.8.1", "1.8.1", true},
		{"jq-1.8.1", "1.8.1", true},
		{"2024-edition-v1.8.1", "1.8.1", true},
		{"1.2.3", "2.3", false}, // "2.3" is the tail of a longer version, not a real match
		{"12.3", "2.3", false},  // digit immediately before the suffix
		{"v1.8.2", "1.8.1", false},
		{"", "1.8.1", false},
	}
	for _, tt := range tests {
		if got := TagMatchesVersion(tt.tag, tt.version); got != tt.want {
			t.Errorf("TagMatchesVersion(%q, %q) = %v, want %v", tt.tag, tt.version, got, tt.want)
		}
	}
}

func TestFetchReleases_ErrorStatus(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := FetchReleases("o", "r", "1.0.0", "")
	if err == nil {
		t.Error("expected error on non-200 status")
	}
}

func TestNextPageURL(t *testing.T) {
	tests := []struct {
		name       string
		linkHeader string
		want       string
	}{
		{
			name:       "no link header",
			linkHeader: "",
			want:       "",
		},
		{
			name:       "next only",
			linkHeader: `<https://api.github.com/repos/o/r/releases?page=2>; rel="next"`,
			want:       "https://api.github.com/repos/o/r/releases?page=2",
		},
		{
			name:       "next and last",
			linkHeader: `<https://api.github.com/repos/o/r/releases?page=2>; rel="next", <https://api.github.com/repos/o/r/releases?page=5>; rel="last"`,
			want:       "https://api.github.com/repos/o/r/releases?page=2",
		},
		{
			name:       "only last, no next (final page)",
			linkHeader: `<https://api.github.com/repos/o/r/releases?page=1>; rel="prev", <https://api.github.com/repos/o/r/releases?page=5>; rel="last"`,
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextPageURL(tt.linkHeader); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
