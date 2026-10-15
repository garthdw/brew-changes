package version

import "testing"

func TestMatchesTag(t *testing.T) {
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
		{"2026.07.04", "2026.7.4", true}, // zero-padded calendar version (yt-dlp)
		{"v1.20.0-pre", "1.20.0", true},  // preview channel suffix (zed@preview)
	}
	for _, tt := range tests {
		if got := MatchesTag(tt.tag, tt.version); got != tt.want {
			t.Errorf("MatchesTag(%q, %q) = %v, want %v", tt.tag, tt.version, got, tt.want)
		}
	}
}

func TestInLine(t *testing.T) {
	tests := []struct {
		line, version string
		want          bool
	}{
		{"## 3.1 (2026-01-01)", "3.1", true},
		{"## 3.1.0", "3.1", true}, // brew's version less precise than the heading
		{"## 3.10.2", "3.1", false},
		{"## 13.1", "3.1", false},
		{"## v3.1", "3.1", true},
	}
	for _, tt := range tests {
		if got := InLine(tt.line, tt.version); got != tt.want {
			t.Errorf("InLine(%q, %q) = %v, want %v", tt.line, tt.version, got, tt.want)
		}
	}
}

func TestLineMatchers(t *testing.T) {
	tests := []struct {
		v, line string
		want    []bool // one result per matcher
	}{
		{"16.2.0", "## GCC 16.2", []bool{false, true}},
		{"16.2.0", "## GCC 16.2.1", []bool{false, false}},
		{"16.2.0", "## 16.2.0", []bool{true, false}},
		{"1.0", "## 1 big release", []bool{false}},
		{"1.2.3", "## 1.2.3", []bool{true}},
	}
	for _, tt := range tests {
		matchers := LineMatchers(tt.v)
		if len(matchers) != len(tt.want) {
			t.Fatalf("LineMatchers(%q) has %d matchers, want %d", tt.v, len(matchers), len(tt.want))
		}
		for i, match := range matchers {
			if got := match(tt.line); got != tt.want[i] {
				t.Errorf("LineMatchers(%q)[%d](%q) = %v, want %v", tt.v, i, tt.line, got, tt.want[i])
			}
		}
	}
}
