package changelog

import (
	"strconv"
	"strings"
	"testing"
)

func TestTrimToRange_BetweenHeadings(t *testing.T) {
	content := strings.Join([]string{
		"# Changelog",
		"",
		"## 2.0.0",
		"line a",
		"line b",
		"## 1.0.0",
		"line c",
	}, "\n")

	got, found := TrimToRange(content, "2.0.0", "1.0.0", false)
	want := strings.Join([]string{
		"## 2.0.0",
		"line a",
		"line b",
	}, "\n")

	if !found {
		t.Error("expected found = true")
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTrimToRange_InstalledHeadingMissing(t *testing.T) {
	lines := make([]string, 0, 5)
	lines = append(lines, "## 2.0.0")
	for i := 0; i < 150; i++ {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	content := strings.Join(lines, "\n")

	got, found := TrimToRange(content, "2.0.0", "1.0.0", false)

	if !found {
		t.Error("expected found = true")
	}
	if !strings.HasPrefix(got, "## 2.0.0\nline 0") {
		t.Errorf("expected result to start from the new-version heading, got prefix %q", got[:30])
	}
	if !strings.Contains(got, "truncated, showing 100 lines from the 2.0.0 heading") {
		t.Errorf("expected truncation notice, got %q", got)
	}
	gotLines := strings.Split(got, "\n")
	// MaxContentLines (100) content lines + 2 blank/notice lines appended.
	if len(gotLines) != MaxContentLines+2 {
		t.Errorf("got %d lines, want %d", len(gotLines), MaxContentLines+2)
	}
}

func TestTrimToRange_NewVersionHeadingMissing(t *testing.T) {
	lines := make([]string, 0, 150)
	for i := 0; i < 150; i++ {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	content := strings.Join(lines, "\n")

	got, found := TrimToRange(content, "9.9.9", "1.0.0", false)

	if found {
		t.Error("expected found = false when the new-version heading can't be located")
	}
	if !strings.Contains(got, "couldn't locate version headings, showing first 100 lines") {
		t.Errorf("expected fallback notice, got %q", got)
	}
	if !strings.HasPrefix(got, "line 0\n") {
		t.Errorf("expected result to start from the beginning, got prefix %q", got[:20])
	}
}

func TestTrimToRange_InstalledHeadingBeforeNewVersion(t *testing.T) {
	// installedVersion's heading appears before newVersion's — should be
	// ignored, falling back to the truncation-from-start behavior.
	content := strings.Join([]string{
		"## 1.0.0",
		"old stuff",
		"## 2.0.0",
		"new stuff",
	}, "\n")

	got, found := TrimToRange(content, "2.0.0", "1.0.0", false)
	want := "## 2.0.0\nnew stuff"

	if !found {
		t.Error("expected found = true")
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTrimToRange_EmptyInstalledVersion(t *testing.T) {
	content := strings.Join([]string{
		"## 2.0.0",
		"line a",
	}, "\n")

	got, found := TrimToRange(content, "2.0.0", "", false)
	want := "## 2.0.0\nline a"

	if !found {
		t.Error("expected found = true")
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindHeading_IgnoresNonHeadingLines(t *testing.T) {
	lines := []string{
		"See 2.0.0 in the body text, not a heading",
		"## 2.0.0 actual heading",
	}

	got := findHeading(lines, "2.0.0", false)
	if got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestFindHeading_NotFound(t *testing.T) {
	lines := []string{"## 1.0.0", "## 3.0.0"}
	if got := findHeading(lines, "2.0.0", false); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}

func TestFindHeading_LaxMatchesUnmarkedLine(t *testing.T) {
	lines := []string{"CHANGES FROM 1.0.0 TO 2.0.0", "* did a thing"}

	if got := findHeading(lines, "2.0.0", false); got != -1 {
		t.Errorf("strict: got %d, want -1 (no ATX/Setext heading present)", got)
	}
	if got := findHeading(lines, "2.0.0", true); got != 0 {
		t.Errorf("lax: got %d, want 0", got)
	}
}

func TestFindHeading_LaxIgnoresBlankLines(t *testing.T) {
	lines := []string{"", "  ", "2.0.0 (2026-01-01)"}
	if got := findHeading(lines, "2.0.0", true); got != 2 {
		t.Errorf("got %d, want 2 (blank lines shouldn't match)", got)
	}
}

func TestTrimToRange_LaxBetweenUnmarkedHeadings(t *testing.T) {
	content := strings.Join([]string{
		"2.0.0 (2026-01-01)",
		"line a",
		"1.0.0 (2025-01-01)",
		"line b",
	}, "\n")

	got, found := TrimToRange(content, "2.0.0", "1.0.0", true)
	want := strings.Join([]string{
		"2.0.0 (2026-01-01)",
		"line a",
	}, "\n")

	if !found {
		t.Error("expected found = true")
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTrimToRange_TrailingZeroOmittedInHeadings(t *testing.T) {
	content := "## GCC 16.2\nfixes\n## GCC 16.1\nolder\n"

	got, found := TrimToRange(content, "16.2.0", "16.1.0", false)
	if !found {
		t.Fatal("expected found = true")
	}
	if want := "## GCC 16.2\nfixes"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindHeading_TwoPartVersionNotShortenedToMajor(t *testing.T) {
	if got := findHeading([]string{"## 1 big release", "## 1.0"}, "1.0", false); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
	if got := findHeading([]string{"## 1 big release"}, "1.0", false); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}

func TestTrimToRange_TrailingZeroRetryDoesNotMatchLongerVersion(t *testing.T) {
	content := "## 2.6.0\nnewest\n## 2.5.1\npatch\n## 2.5\nolder\n"

	got, found := TrimToRange(content, "2.6.0", "2.5.0", false)
	if !found {
		t.Fatal("expected found = true")
	}
	if want := "## 2.6.0\nnewest\n## 2.5.1\npatch"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// auto-changelog (used by ast-grep) links each heading to a compare URL
// naming the previous version, so the new-version heading also mentions
// the installed version.
func TestTrimToRange_NewHeadingMentionsInstalledVersion(t *testing.T) {
	content := strings.Join([]string{
		"#### [0.51.0](https://github.com/ast-grep/ast-grep/compare/0.50.0...0.51.0)",
		"- new",
		"#### [0.50.0](https://github.com/ast-grep/ast-grep/compare/0.45.3...0.50.0)",
		"- old",
	}, "\n")

	got, found := TrimToRange(content, "0.51.0", "0.50.0", false)
	want := strings.Join([]string{
		"#### [0.51.0](https://github.com/ast-grep/ast-grep/compare/0.50.0...0.51.0)",
		"- new",
	}, "\n")

	if !found {
		t.Error("expected found = true")
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
