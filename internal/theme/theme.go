// Package theme defines brew-changes's themeable color schema, on top of
// bubblekit/theme's generic chrome+groups engine, and its embedded default
// theme.
package theme

import (
	_ "embed"
	"io"

	bktheme "github.com/garthdw/bubblekit/theme"
)

// Token and Theme are brew-changes's theme types, re-exported from
// bubblekit so callers don't need to import both packages.
type Token = bktheme.Token
type Theme = bktheme.Theme

//go:embed default.json
var defaultThemeJSON []byte

var defaultTheme = mustDefaultTheme()

func mustDefaultTheme() Theme {
	t, err := bktheme.Merge(defaultThemeJSON, bktheme.Theme{}, io.Discard)
	if err != nil {
		panic("theme: embedded default.json is invalid: " + err.Error())
	}
	return t
}

// DefaultTheme returns brew-changes's built-in theme.
func DefaultTheme() Theme {
	return defaultTheme
}

// Version returns the token used to color package version numbers, a
// brew-changes-specific chrome extension not generic enough for
// bktheme.ChromeTokens.
func Version(t Theme) Token {
	return t.Groups["chrome"]["version"]
}
