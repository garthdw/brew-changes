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

// loadMainMap resolves mainMap merged with any user override at
// ~/.config/brew-changes/keymap.json, falling back to mainMap unchanged if
// there's no override or it can't be applied.
func loadMainMap() *keymap.Map {
	loaded, err := keymap.Loader{ConfigSubdir: "brew-changes"}.
		Load("", map[string]*keymap.Map{"main": mainMap})
	if err != nil {
		return mainMap
	}
	return loaded["main"]
}
