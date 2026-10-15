package changelogconfig

import "testing"

func TestGet(t *testing.T) {
	if Get("rust").URL == "" {
		t.Error(`Get("rust").URL is empty, want a known URL`)
	}
	if !Get("tmux").LaxHeadings {
		t.Error(`Get("tmux").LaxHeadings = false, want true`)
	}
	if e := Get("zed@preview"); e.Repo != "zed-industries/zed" || !e.Prerelease {
		t.Errorf(`Get("zed@preview") = %+v, want repo zed-industries/zed with prerelease`, e)
	}
	if Get("_comment") != (Entry{}) {
		t.Error(`Get("_comment") should not expose the JSON file's own comment field`)
	}
	if Get("definitely-not-a-real-package") != (Entry{}) {
		t.Error("Get of an unknown package should return the zero Entry")
	}
}

func TestExpandURL(t *testing.T) {
	got := ExpandURL("https://example.com/v{major}/{major.minor}/notes-{version}.html", "9.0.2")
	if want := "https://example.com/v9/9.0/notes-9.0.2.html"; got != want {
		t.Errorf("ExpandURL = %q, want %q", got, want)
	}
}
