// Package changelogconfig holds a small manual per-formula configuration
// table for Homebrew formulae/casks whose changelog resolution needs help
// beyond ghsource's GitHub-based auto-detection. See changelog_config.json
// for how entries were chosen and what each field means.
package changelogconfig

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed changelog_config.json
var data []byte

// Entry is one formula's configuration. URL is a manual changelog/release
// notes link used when auto-detection can't find (or shouldn't be trusted
// for) the real changelog. Repo ("owner/repo") names a GitHub repo that
// auto-detection can't find from Homebrew's metadata. LaxHeadings opts into
// a looser version-heading match for formulae whose auto-detected changelog
// file uses unmarked plain-text section headers rather than Markdown
// ATX/Setext headings. Prerelease restricts release notes to GitHub-flagged
// pre-releases, for preview channels.
type Entry struct {
	URL         string `json:"url"`
	Repo        string `json:"repo"`
	LaxHeadings bool   `json:"lax_headings"`
	Prerelease  bool   `json:"prerelease"`
}

var entries = parse()

func parse() map[string]Entry {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	delete(raw, "_comment")
	parsed := make(map[string]Entry, len(raw))
	for name, msg := range raw {
		var e Entry
		if err := json.Unmarshal(msg, &e); err == nil {
			parsed[name] = e
		}
	}
	return parsed
}

// Get returns the configuration recorded for a Homebrew formula or cask
// name, or the zero Entry if there is none.
func Get(name string) Entry {
	return entries[name]
}

// ExpandURL substitutes {version}, {major.minor} and {major} in an Entry's
// URL, for projects that publish notes per release, release branch or
// major series (git's RelNotes, qemu's ChangeLog/X.Y pages, gcc-N), so
// entries don't go stale every release.
func ExpandURL(url, version string) string {
	parts := strings.SplitN(version, ".", 3)
	majorMinor := strings.Join(parts[:min(2, len(parts))], ".")
	return strings.NewReplacer("{version}", version, "{major.minor}", majorMinor, "{major}", parts[0]).Replace(url)
}
