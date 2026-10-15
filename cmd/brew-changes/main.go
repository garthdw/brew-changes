// Command brew-changes shows changelogs/release notes for every outdated
// Homebrew package (formulae + casks) before optionally upgrading them.
package main

import (
	"fmt"
	"os"

	"github.com/garthdw/brew-changes/internal/ghsource"
	"github.com/garthdw/brew-changes/internal/homebrew"
	"github.com/garthdw/brew-changes/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("Checking for outdated packages...")

	packages, err := homebrew.Outdated()
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		fmt.Println("Everything is up to date.")
		return nil
	}

	infos := homebrew.ResolveInfo(packages)
	items := make([]ui.Item, 0, len(packages))
	for i, pkg := range packages {
		kind := "formula"
		if pkg.IsCask {
			kind = "cask"
		}

		info := infos[i]
		items = append(items, ui.Item{
			Name:      pkg.Name,
			Kind:      kind,
			Installed: pkg.Installed,
			Current:   pkg.Current,
			Homepage:  info.Homepage,
			Source:    ghsource.ResolveSource(pkg.Name, pkg.Current, info.Homepage, info.URL),
		})
	}

	result, err := ui.Run(items)
	if err != nil {
		return err
	}

	if result.Action != ui.ActionUpgradeAll {
		fmt.Println("No packages upgraded.")
		return nil
	}

	var formulaNames, caskNames []string
	for _, it := range result.Items {
		if it.Upgraded {
			continue // already upgraded in-place before quitting
		}
		if it.Kind == "cask" {
			caskNames = append(caskNames, it.Name)
		} else {
			formulaNames = append(formulaNames, it.Name)
		}
	}
	if len(formulaNames) == 0 && len(caskNames) == 0 {
		fmt.Println("Everything is already upgraded.")
		return nil
	}

	fmt.Printf("Upgrading %d package(s)...\n", len(formulaNames)+len(caskNames))
	return homebrew.Upgrade(formulaNames, caskNames)
}
