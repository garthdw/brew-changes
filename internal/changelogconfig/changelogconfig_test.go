package changelogconfig

import "testing"

func TestLookup(t *testing.T) {
	url, ok := Lookup("rust")
	if !ok || url == "" {
		t.Errorf("Lookup(%q) = (%q, %v), want a known URL", "rust", url, ok)
	}

	if _, ok := Lookup("_comment"); ok {
		t.Error(`Lookup("_comment") should not expose the JSON file's own comment field`)
	}

	if _, ok := Lookup("definitely-not-a-real-package"); ok {
		t.Error("Lookup of an unknown package should return ok = false")
	}
}

func TestLaxHeadings(t *testing.T) {
	if !LaxHeadings("tmux") {
		t.Error(`LaxHeadings("tmux") = false, want true`)
	}
	if LaxHeadings("rust") {
		t.Error(`LaxHeadings("rust") = true, want false (rust uses a URL override, not lax headings)`)
	}
	if LaxHeadings("definitely-not-a-real-package") {
		t.Error("LaxHeadings of an unknown package should return false")
	}
}
