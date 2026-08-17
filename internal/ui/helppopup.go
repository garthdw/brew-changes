package ui

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/garthdw/bubblekit/keymap"
	"github.com/garthdw/bubblekit/layout"
)

// helpRows lists every mainMap action group in display order, paired with a
// human-readable label. The keys shown for each row are resolved live from
// mainMap (via keysForActions) rather than hardcoded, so overrides loaded
// from a user's keymap.json stay reflected in the popup.
var helpRows = []struct {
	actions []string
	label   string
}{
	{[]string{"move-up", "move-down"}, "move the cursor"},
	{[]string{"move-up-or-scroll-up", "move-down-or-scroll-down"}, "scroll changelog (when expanded)"},
	{[]string{"toggle-expand"}, "expand/collapse"},
	{[]string{"open-homepage"}, "open homepage"},
	{[]string{"open-github"}, "open github"},
	{[]string{"open-changelog"}, "open changelog"},
	{[]string{"upgrade-all"}, "upgrade all"},
	{[]string{"upgrade-current"}, "upgrade current"},
	{[]string{"help"}, "help"},
	{[]string{"quit"}, "quit"},
}

// keyGlyphs renders a couple of well-known key names as arrows, matching
// the popup's previous hand-written labels.
var keyGlyphs = map[string]string{"up": "↑", "down": "↓"}

// keysForActions returns every key in m currently bound to one of actions,
// sorted and joined for display (e.g. "K/up").
func keysForActions(m *keymap.Map, actions []string) string {
	want := make(map[string]bool, len(actions))
	for _, a := range actions {
		want[a] = true
	}
	var keys []string
	for key, action := range m.Actions {
		if !want[action] {
			continue
		}
		if glyph, ok := keyGlyphs[key]; ok {
			key = glyph
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, "/")
}

const helpHomepage = "https://github.com/garthdw/brew-changes"

// renderHelpPopup builds the about/help modal's content and frames it in
// styleModalPopup, clamped so it never exceeds the terminal size.
func renderHelpPopup(m model) string {
	var b strings.Builder
	b.WriteString(styleHeader.Render("brew-changes"))
	b.WriteString("\n")
	b.WriteString(styleDim.Render("Interactive changelog viewer for outdated Homebrew packages"))
	b.WriteString("\n\n")
	b.WriteString("by Garth de Wet\n")
	b.WriteString("made with Claude Code\n")
	b.WriteString(helpHomepage)
	b.WriteString("\n\n")

	keys := make([]string, len(helpRows))
	keyWidth := 0
	for i, row := range helpRows {
		keys[i] = keysForActions(mainMap, row.actions)
		if w := lipgloss.Width(keys[i]); w > keyWidth {
			keyWidth = w
		}
	}
	for i, row := range helpRows {
		b.WriteString(styleVersion.Render(lipgloss.NewStyle().Width(keyWidth).Render(keys[i])))
		b.WriteString("  ")
		b.WriteString(row.label)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styleDim.Render("press any key to close"))

	content := strings.TrimRight(b.String(), "\n")
	width := lipgloss.Width(content)
	if maxWidth := m.width - 6; maxWidth > 0 && width > maxWidth {
		width = maxWidth
	}
	height := strings.Count(content, "\n") + 1

	return layout.RenderTitledPane(styleModalPopup, width, height, "About", content)
}
