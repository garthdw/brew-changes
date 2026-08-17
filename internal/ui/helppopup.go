package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/garthdw/bubblekit/layout"
)

// helpEntries lists every mainMap keybinding in display order, paired with a
// human-readable label. Kept separate from mainMap (which maps keys to
// action names) so this list can order and word entries for readability.
var helpEntries = []struct {
	key   string
	label string
}{
	{"↑/↓, j/k", "move the cursor"},
	{"j/k", "scroll changelog (when expanded)"},
	{"enter", "expand/collapse"},
	{"o", "open homepage"},
	{"g", "open github"},
	{"c", "open changelog"},
	{"a", "upgrade all"},
	{"u", "upgrade current"},
	{"?", "help"},
	{"q", "quit"},
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

	keyWidth := 0
	for _, e := range helpEntries {
		if w := lipgloss.Width(e.key); w > keyWidth {
			keyWidth = w
		}
	}
	for _, e := range helpEntries {
		b.WriteString(styleVersion.Render(lipgloss.NewStyle().Width(keyWidth).Render(e.key)))
		b.WriteString("  ")
		b.WriteString(e.label)
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
