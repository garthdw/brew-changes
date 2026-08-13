package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/garthdw/homebrew-changes/internal/theme"
)

var (
	styleVersion   lipgloss.Style
	styleArrow     lipgloss.Style
	styleDim       lipgloss.Style
	styleHeader    lipgloss.Style
	styleSelected  lipgloss.Style
	styleError     lipgloss.Style
	styleBodyFrame lipgloss.Style
)

func init() {
	ApplyTheme(theme.DefaultTheme())
}

// ApplyTheme rebuilds every package-level style from t.
func ApplyTheme(t theme.Theme) {
	c := t.Chrome

	styleVersion = theme.Version(t).ApplyTo(lipgloss.NewStyle())
	styleArrow = c.Accent.ApplyTo(lipgloss.NewStyle())
	styleDim = c.MutedText.ApplyTo(lipgloss.NewStyle())
	styleHeader = lipgloss.NewStyle().Bold(true)
	styleSelected = lipgloss.NewStyle().Bold(true)
	styleError = c.Error.ApplyTo(lipgloss.NewStyle())
	styleBodyFrame = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(c.Border.Color)).
		Padding(0, 1).
		MarginLeft(2)
}
