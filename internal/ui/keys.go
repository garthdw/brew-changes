package ui

import "github.com/garthdw/bubblekit/keymap"

// mainMap holds every keybinding for the (single-pane) list view. "k" and
// "j" are dual-purpose in Update: they scroll an expanded item's changelog
// body when the highlighted item is expanded, otherwise they move the
// cursor like the arrow keys — see move-up-or-scroll-up/move-down-or-
// scroll-down.
var mainMap = keymap.New("main", nil, map[string]string{
	"ctrl+c": "quit",
	"q":      "quit",
	"up":     "move-up",
	"K":      "move-up",
	"down":   "move-down",
	"J":      "move-down",
	"k":      "move-up-or-scroll-up",
	"j":      "move-down-or-scroll-down",
	"enter":  "toggle-expand",
	"o":      "open-homepage",
	"g":      "open-github",
	"c":      "open-changelog",
	"a":      "upgrade-all",
	"u":      "upgrade-current",
	"?":      "help",
})
