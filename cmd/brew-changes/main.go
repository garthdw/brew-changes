// Command brew-changes shows changelogs/release notes for every outdated
// Homebrew package (formulae + casks) before optionally upgrading them.
package main

import (
	"fmt"
	"os"
	"sync"

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

	var outdatedFormulae, outdatedCasks []string
	for _, pkg := range packages {
		if pkg.IsCask {
			outdatedCasks = append(outdatedCasks, pkg.Name)
		} else {
			outdatedFormulae = append(outdatedFormulae, pkg.Name)
		}
	}
	// Errors leave the map nil, which reads as "no info" for every package.
	var formulaInfos, caskInfos map[string]homebrew.Info
	var wg sync.WaitGroup
	wg.Go(func() { formulaInfos, _ = homebrew.ResolveInfoBatch(outdatedFormulae, false) })
	wg.Go(func() { caskInfos, _ = homebrew.ResolveInfoBatch(outdatedCasks, true) })
	wg.Wait()

	items := make([]ui.Item, 0, len(packages))
	for _, pkg := range packages {
		kind := "formula"
		infos := formulaInfos
		if pkg.IsCask {
			kind = "cask"
			infos = caskInfos
		}

		info := infos[pkg.Name]
		items = append(items, ui.Item{
			Name:      pkg.Name,
			Kind:      kind,
			Installed: pkg.Installed,
			Current:   pkg.Current,
			Homepage:  info.Homepage,
			Source:    ghsource.ResolveSource(pkg.Name, info.Homepage, info.URL),
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
