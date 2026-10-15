// Package changelog trims a full changelog file down to the section
// relevant to a specific version upgrade.
package changelog

import (
	"fmt"
	"strings"

	"github.com/garthdw/brew-changes/internal/version"
)

// MaxContentLines caps how much of a changelog is shown when the exact
// version range can't be located.
const MaxContentLines = 100

// TrimToRange returns the section of content between the newVersion and
// installedVersion headings, if both can be located. If only newVersion's
// heading is found, it returns up to MaxContentLines from there. found is
// false only when newVersion's heading can't be located at all — e.g. an
// index-style changelog (like Node.js's, which just links out to
// per-major-version files) that doesn't actually document the version in
// question. In that case, the first MaxContentLines of content is returned
// as a last resort, but callers may prefer to treat found=false as a
// signal to fall back to another source (release notes, say).
//
// lax relaxes heading detection to accept any non-blank line containing
// the version (not just Markdown ATX/Setext headings) — for changelogs
// like tmux's CHANGES or xz's NEWS, which mark version sections with plain
// unmarked lines. It should only be enabled for formulae whose file format
// has been checked, since it's more prone to matching a version mentioned
// in body text rather than an actual section boundary.
func TrimToRange(content, newVersion, installedVersion string, lax bool) (string, bool) {
	lines := strings.Split(content, "\n")

	startLine := findHeading(lines, newVersion, lax)
	if startLine == -1 {
		result := strings.Join(lines[:min(MaxContentLines, len(lines))], "\n")
		return result + fmt.Sprintf("\n\n... (couldn't locate version headings, showing first %d lines)", MaxContentLines), false
	}

	endLine := findHeading(lines, installedVersion, lax)
	if endLine != -1 && endLine > startLine {
		return strings.Join(lines[startLine:endLine], "\n"), true
	}

	end := startLine + MaxContentLines
	result := strings.Join(lines[startLine:min(end, len(lines))], "\n")
	if end < len(lines) {
		result += fmt.Sprintf("\n\n... (truncated, showing %d lines from the %s heading)", MaxContentLines, newVersion)
	}
	return result, true
}

// findHeading returns the index of the first heading line containing
// v, or -1 if none is found. Both ATX (`# text`) and Setext (`text`
// underlined with `===`/`---`) markdown headings are recognized, since
// some changelogs (e.g. fzf, ripgrep) use the latter exclusively. When lax
// is true, any non-blank line counts as a heading candidate, for
// changelogs that mark version sections with plain unmarked lines. Each of
// version.LineMatchers(v) is tried in turn.
func findHeading(lines []string, v string, lax bool) int {
	if v == "" {
		return -1
	}
	for _, matches := range version.LineMatchers(v) {
		for i, line := range lines {
			isATX := strings.TrimLeft(line, "#") != line
			isSetext := strings.TrimSpace(line) != "" && i+1 < len(lines) && isSetextUnderline(lines[i+1])
			isHeading := isATX || isSetext || (lax && strings.TrimSpace(line) != "")
			if isHeading && matches(line) {
				return i
			}
		}
	}
	return -1
}

// isSetextUnderline reports whether line is a valid Setext heading
// underline: one or more '=' characters, or one or more '-' characters,
// with no other content besides surrounding whitespace.
func isSetextUnderline(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	c := trimmed[0]
	return (c == '=' || c == '-') && strings.Trim(trimmed, string(c)) == ""
}
