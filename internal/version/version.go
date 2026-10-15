// Package version matches Homebrew's plain version strings (e.g. "1.8.1")
// against the forms upstream projects write them in: changelog headings and
// release tags.
package version

import (
	"regexp"
	"strings"
)

// InLine reports whether line contains v as a standalone token, not merely
// as a substring of a longer version number (e.g. "3.1" inside "3.10.2"). A
// trailing "." is allowed after the match (e.g. "3.1" inside "3.1.0"), since
// brew's reported version is sometimes less precise than the version
// documented in a changelog heading.
func InLine(line, v string) bool {
	return inLine(line, v, true)
}

// inLine is InLine, optionally rejecting a match that continues as a longer
// version ("2.5" inside "2.5.1").
func inLine(line, v string, allowLonger bool) bool {
	for start := 0; ; {
		idx := strings.Index(line[start:], v)
		if idx == -1 {
			return false
		}
		idx += start
		end := idx + len(v)
		beforeOK := idx == 0 || !isVersionChar(line[idx-1])
		afterOK := end == len(line) || !isDigit(line[end])
		if !allowLonger && end+1 < len(line) && line[end] == '.' && isDigit(line[end+1]) {
			afterOK = false
		}
		if beforeOK && afterOK {
			return true
		}
		start = idx + 1
	}
}

// LineMatchers returns predicates reporting whether a line mentions v, to
// be tried in order: InLine, then, for a three-plus-part version ending in
// ".0", v without that suffix, since some projects drop it (gcc's "GCC
// 16.2", git's "Git v2.56") while Homebrew reports "16.2.0". The shortened
// form must match exactly, so "2.5" (for "2.5.0") doesn't match "2.5.1".
func LineMatchers(v string) []func(line string) bool {
	matchers := []func(string) bool{func(line string) bool { return InLine(line, v) }}
	if short, ok := strings.CutSuffix(v, ".0"); ok && strings.Contains(short, ".") {
		matchers = append(matchers, func(line string) bool { return inLine(line, short, false) })
	}
	return matchers
}

// MatchesTag reports whether a release tag identifies v, regardless of
// whatever prefix the upstream project puts on its tags: "v1.8.1",
// "jq-1.8.1", and even "2024-edition-v1.8.1" all match "1.8.1". A trailing
// "-pre" (as used by preview/nightly channels, e.g. zed@preview's
// "v1.20.0-pre") is stripped before matching, so those tags can also serve
// as a release walk's installed/target boundary.
//
// A tag matches if it ends with v and the character immediately before
// that suffix can't continue a version number. That boundary check is what
// makes this safe where a naive "strip everything before the first digit"
// isn't: without it, "2.3" would wrongly match tag "1.2.3" (the tail of a
// different, longer version number) since '.' would be mistaken for a valid
// prefix separator.
func MatchesTag(tag, v string) bool {
	if v == "" {
		return false
	}
	tag = strings.TrimSuffix(tag, "-pre")
	return exactSuffixMatch(tag, v) || trailingComponentsMatch(tag, v)
}

func exactSuffixMatch(tag, v string) bool {
	if !strings.HasSuffix(tag, v) {
		return false
	}
	return len(tag) == len(v) || !isVersionChar(tag[len(tag)-len(v)-1])
}

var trailingVersionPattern = regexp.MustCompile(`[0-9]+(\.[0-9]+)*$`)

// trailingComponentsMatch compares tag's trailing dot-separated numeric run
// against v component-by-component, ignoring leading zeros in each
// component. This catches cases like yt-dlp, whose calendar-style versions
// are zero-padded in its GitHub tags ("2026.07.04") but not in Homebrew's
// reported version ("2026.7.4"), which exactSuffixMatch can't see past.
func trailingComponentsMatch(tag, v string) bool {
	loc := trailingVersionPattern.FindStringIndex(tag)
	if loc == nil || (loc[0] > 0 && isVersionChar(tag[loc[0]-1])) {
		return false
	}
	tagParts := strings.Split(tag[loc[0]:], ".")
	versionParts := strings.Split(v, ".")
	if len(tagParts) != len(versionParts) {
		return false
	}
	for i := range tagParts {
		if normalizeComponent(tagParts[i]) != normalizeComponent(versionParts[i]) {
			return false
		}
	}
	return true
}

func normalizeComponent(s string) string {
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0"
	}
	return s
}

func isVersionChar(b byte) bool {
	return isDigit(b) || b == '.'
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
