// Package changelogconfig holds a small manual per-formula configuration
// table for Homebrew formulae/casks whose changelog resolution needs help
// beyond ghsource's GitHub-based auto-detection. See changelog_config.json
// for how entries were chosen and what each field means.
package changelogconfig

import (
	_ "embed"
	"encoding/json"
	"sync"
)

//go:embed changelog_config.json
var data []byte

// entry is one formula's configuration. URL is a manual changelog/release
// notes link used when auto-detection can't find (or shouldn't be trusted
// for) the real changelog. LaxHeadings opts into a looser version-heading
// match for formulae whose auto-detected changelog file uses unmarked
// plain-text section headers rather than Markdown ATX/Setext headings.
type entry struct {
	URL         string `json:"url"`
	LaxHeadings bool   `json:"lax_headings"`
}

var (
	once    sync.Once
	entries map[string]entry
)

func load() map[string]entry {
	once.Do(func() {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			entries = map[string]entry{}
			return
		}
		delete(raw, "_comment")
		entries = make(map[string]entry, len(raw))
		for name, msg := range raw {
			var e entry
			if err := json.Unmarshal(msg, &e); err == nil {
				entries[name] = e
			}
		}
	})
	return entries
}

// Lookup returns the known changelog URL for a Homebrew formula or cask
// name, if one is recorded.
func Lookup(name string) (url string, ok bool) {
	e, found := load()[name]
	if !found || e.URL == "" {
		return "", false
	}
	return e.URL, true
}

// LaxHeadings reports whether name's changelog file should be matched with
// a looser version-heading rule (see entry.LaxHeadings).
func LaxHeadings(name string) bool {
	return load()[name].LaxHeadings
}
