// Package ui implements the interactive Bubble Tea list used to review and
// select outdated Homebrew packages before upgrading.
package ui

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"

	"github.com/garthdw/brew-changes/internal/ghsource"
	"github.com/garthdw/brew-changes/internal/homebrew"
	"github.com/garthdw/bubblekit/pane"
	"github.com/garthdw/bubblekit/statusline"
)

// Item is one outdated package in the list.
type Item struct {
	Name      string
	Kind      string // "formula" or "cask"
	Installed string
	Current   string
	Homepage  string // package homepage, empty if unknown
	Owner     string // GitHub owner, empty if no repo could be resolved
	Repo      string // GitHub repo name

	// ManualChangelogURL is a manually curated changelog/release-notes
	// link (see internal/changelogconfig) used when no GitHub repo could
	// be resolved automatically. Empty if none is known.
	ManualChangelogURL string

	// ChangelogFilename is the changelog file resolveChangelogCmd found in
	// the GitHub repo, if any. Set once resolution has happened (which
	// requires the item to have been expanded at least once); empty until
	// then or if the repo uses releases instead of a changelog file.
	ChangelogFilename string

	// LaxHeadings relaxes version-heading matching for formulae recorded
	// in internal/changelogconfig as needing it — e.g. a changelog file
	// that marks version sections with plain, unmarked lines rather than
	// Markdown headings.
	LaxHeadings bool

	Expanded     bool
	Loading      bool
	LoadingStage string // "changelog" while resolving/fetching the changelog file, or "releases" once falling back to release notes
	Body         string // rendered changelog/releases content, empty until fetched

	// Upgrading, Upgraded, and UpgradeErr track an in-place single-package
	// upgrade triggered by the "u" key; unlike ActionUpgradeAll, this
	// happens without quitting the list so the changelog stays browsable.
	Upgrading  bool
	Upgraded   bool
	UpgradeErr string
}

// GitHubURL returns the item's GitHub repo URL, or empty if none could be
// resolved.
func (it Item) GitHubURL() string {
	if it.Owner == "" {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/%s", it.Owner, it.Repo)
}

// ChangelogURL returns the best link currently known for viewing this
// item's changelog: a manual override if one is configured, the exact
// changelog file once resolveChangelogCmd has found it, or the repo's
// releases page as a reasonable default before/without that resolution.
// Empty if no GitHub repo could be resolved and no manual override exists.
func (it Item) ChangelogURL() string {
	if it.ManualChangelogURL != "" {
		return it.ManualChangelogURL
	}
	if it.Owner == "" {
		return ""
	}
	if it.ChangelogFilename != "" {
		return fmt.Sprintf("https://github.com/%s/%s/blob/HEAD/%s", it.Owner, it.Repo, it.ChangelogFilename)
	}
	return fmt.Sprintf("https://github.com/%s/%s/releases", it.Owner, it.Repo)
}

// Action is what the user asked to do when they quit the list.
type Action int

const (
	// ActionNone means the user quit without upgrading anything.
	ActionNone Action = iota
	// ActionUpgradeAll means every outdated package should be upgraded.
	ActionUpgradeAll
)

// Result is returned by Run: what the user asked to do when they quit, and
// the final state of every item (reflecting any in-place single-package
// upgrades performed via the "u" key before quitting).
type Result struct {
	Action Action
	Items  []Item
}

const (
	headerLines = 2 // "Outdated packages:" + blank line
	footerLines = 2 // blank line + keybinding hint
)

type model struct {
	items     []Item
	cursor    int
	spinner   spinner.Model
	viewport  viewport.Model
	ready     bool
	quitting  bool
	action    Action
	width     int
	itemLines []int // line offset of each item's header within the rendered content
	showHelp  bool

	// bodyFrameCache memoizes styleBodyFrame.Render output per item index,
	// keyed on the body content it was rendered from, so navigating the
	// list doesn't re-run lipgloss border/padding rendering over large
	// changelog bodies that haven't changed since the last keypress.
	bodyFrameCache map[int]bodyFrame
}

type bodyFrame struct {
	body     string
	rendered string
}

func newModel(items []Item) model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleDim
	return model{items: items, spinner: s, bodyFrameCache: make(map[int]bodyFrame)}
}

// Run launches the interactive, fullscreen list for the given packages and
// blocks until the user quits or confirms an upgrade.
func Run(items []Item) (Result, error) {
	mainMap = loadMainMap()
	// Must run before tea.NewProgram claims stdin, or this races with its input reader.
	darkBackground = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	m := newModel(items)
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return Result{}, err
	}
	fm := finalModel.(model)
	return Result{Action: fm.action, Items: fm.items}, nil
}

func (m model) Init() tea.Cmd {
	return m.spinner.Tick
}

type bodyFetchedMsg struct {
	index int
	body  string
}

// changelogResolvedMsg reports which changelog filename (if any) should be
// used. An empty filename means either none of the well-known candidates
// exist, or the repo's releases are more recently updated than its
// changelog file (see ghsource.ResolveChangelogSource) — either way, the
// caller should fall back to releases.
type changelogResolvedMsg struct {
	index    int
	filename string
}

// resolveChangelogCmd determines whether to use the repo's changelog file
// or its releases (ghsource.ResolveChangelogSource), which costs at most a
// couple of small requests rather than probing each candidate changelog
// filename with its own round trip.
func resolveChangelogCmd(index int, it Item) tea.Cmd {
	return func() tea.Msg {
		if it.Owner == "" {
			if it.ManualChangelogURL != "" {
				if section, ok := ghsource.FetchManualSection(it.ManualChangelogURL, it.Current, it.Installed, it.LaxHeadings); ok {
					body := fmt.Sprintf("(from %s)", it.ManualChangelogURL) + "\n\n" + renderMarkdown(section)
					return bodyFetchedMsg{index: index, body: body}
				}
				body := "No GitHub repository could be resolved automatically for this package.\n\n" +
					renderMarkdown(fmt.Sprintf("Known changelog: %s", it.ManualChangelogURL))
				return bodyFetchedMsg{index: index, body: body}
			}
			return bodyFetchedMsg{index: index, body: "No GitHub repository could be resolved for this package; changelog unavailable."}
		}
		name, useReleases := ghsource.ResolveChangelogSource(it.Owner, it.Repo)
		if useReleases {
			return changelogResolvedMsg{index: index}
		}
		return changelogResolvedMsg{index: index, filename: name}
	}
}

// fetchChangelogFileCmd fetches and trims the changelog filename
// resolveChangelogCmd found (see ghsource.FetchChangelogSection). It falls
// back to releases if that source turns out to be unusable — either the
// fetch fails despite the listing saying the file exists (e.g. a race with
// the repo changing), or the file doesn't actually document the target
// version.
func fetchChangelogFileCmd(index int, it Item, filename string) tea.Cmd {
	return func() tea.Msg {
		section, ok := ghsource.FetchChangelogSection(it.Owner, it.Repo, filename, it.Current, it.Installed, it.LaxHeadings)
		if !ok {
			return changelogResolvedMsg{index: index}
		}
		body := fmt.Sprintf("(from %s)", filename) + "\n\n" + renderMarkdown(section)
		return bodyFetchedMsg{index: index, body: body}
	}
}

// FetchReleases can return more than this when it.Current's tag sits deep in
// the page (see its doc comment), so the TUI caps what it renders here.
const maxDisplayedReleases = 10

// fetchReleasesCmd checks GitHub releases, the second fetch stage, used only
// when no changelog file was found.
func fetchReleasesCmd(index int, it Item) tea.Cmd {
	return func() tea.Msg {
		releases, err := ghsource.FetchReleases(it.Owner, it.Repo, it.Installed, it.Current)
		if err != nil || len(releases) == 0 {
			return bodyFetchedMsg{index: index, body: "No changelog file or GitHub releases found for this package."}
		}
		if len(releases) > maxDisplayedReleases {
			releases = releases[:maxDisplayedReleases]
		}
		var b strings.Builder
		for _, r := range releases {
			fmt.Fprintf(&b, "### %s (%s)\n\n%s\n\n", r.TagName, cmp.Or(r.PublishedAt, "unknown date"), cmp.Or(r.Body, "_no release notes_"))
		}
		body := "(from GitHub releases)\n\n" + renderMarkdown(b.String())
		return bodyFetchedMsg{index: index, body: body}
	}
}

type upgradeDoneMsg struct {
	index int
	err   error
}

func upgradePackage(index int, it Item) tea.Cmd {
	return func() tea.Msg {
		var err error
		if it.Kind == "cask" {
			err = homebrew.UpgradeQuiet(nil, []string{it.Name})
		} else {
			err = homebrew.UpgradeQuiet([]string{it.Name}, nil)
		}
		return upgradeDoneMsg{index: index, err: err}
	}
}

// openURL opens url in the user's default browser via macOS's `open`.
func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		_ = exec.Command("open", url).Start()
		return nil
	}
}

// darkBackground is resolved once in Run (see there for why).
var darkBackground bool

func renderMarkdown(raw string) string {
	style := glamourstyles.LightStyle
	if darkBackground {
		style = glamourstyles.DarkStyle
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(96))
	if err != nil {
		return raw
	}
	out, err := r.Render(raw)
	if err != nil {
		return raw
	}
	return strings.TrimRight(out, "\n")
}

func (m *model) refreshViewport() {
	content, itemLines := m.renderList()
	m.itemLines = itemLines
	m.viewport.SetContent(content)
}

// scrollToCursor adjusts the viewport offset so the highlighted item's
// header line is visible, scrolling as little as possible.
func (m *model) scrollToCursor() {
	if m.cursor < 0 || m.cursor >= len(m.itemLines) {
		return
	}
	target := m.itemLines[m.cursor]
	if target < m.viewport.YOffset() {
		m.viewport.SetYOffset(target)
	} else if target > m.viewport.YOffset()+m.viewport.Height()-1 {
		m.viewport.SetYOffset(target - m.viewport.Height() + 1)
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		vpHeight := msg.Height - headerLines - footerLines
		if vpHeight < 1 {
			vpHeight = 1
		}
		if !m.ready {
			m.viewport = viewport.New(viewport.WithWidth(msg.Width), viewport.WithHeight(vpHeight))
			m.ready = true
		} else {
			m.viewport.SetWidth(msg.Width)
			m.viewport.SetHeight(vpHeight)
		}
		m.refreshViewport()
		m.scrollToCursor()
		return m, nil

	case tea.KeyPressMsg:
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}

		action, _ := mainMap.Lookup(msg.String())
		switch action {
		case "help":
			m.showHelp = true

		case "quit":
			m.quitting = true
			return m, tea.Quit

		case "move-up":
			m.moveCursor(-1)
			m.refreshViewport()

		case "move-down":
			m.moveCursor(1)
			m.refreshViewport()

		case "move-up-or-scroll-up":
			if len(m.items) > 0 && m.items[m.cursor].Expanded {
				m.viewport.ScrollUp(1)
			} else {
				m.moveCursor(-1)
				m.refreshViewport()
			}

		case "move-down-or-scroll-down":
			if len(m.items) > 0 && m.items[m.cursor].Expanded {
				m.viewport.ScrollDown(1)
			} else {
				m.moveCursor(1)
				m.refreshViewport()
			}

		case "toggle-expand":
			if len(m.items) == 0 {
				return m, nil
			}
			it := &m.items[m.cursor]
			it.Expanded = !it.Expanded
			if it.Expanded && it.Body == "" && !it.Loading {
				it.Loading = true
				it.LoadingStage = "changelog"
				m.refreshViewport()
				m.scrollToCursor()
				return m, resolveChangelogCmd(m.cursor, *it)
			}
			m.refreshViewport()
			m.scrollToCursor()

		case "open-homepage":
			if len(m.items) == 0 {
				return m, nil
			}
			it := m.items[m.cursor]
			url := it.Homepage
			if url == "" {
				url = it.GitHubURL()
			}
			if url == "" {
				return m, nil
			}
			return m, openURL(url)

		case "open-github":
			if len(m.items) == 0 {
				return m, nil
			}
			url := m.items[m.cursor].GitHubURL()
			if url == "" {
				return m, nil
			}
			return m, openURL(url)

		case "open-changelog":
			if len(m.items) == 0 {
				return m, nil
			}
			url := m.items[m.cursor].ChangelogURL()
			if url == "" {
				return m, nil
			}
			return m, openURL(url)

		case "upgrade-all":
			for i := range m.items {
				if m.items[i].Upgrading {
					return m, nil
				}
			}
			m.action = ActionUpgradeAll
			return m, tea.Quit

		case "upgrade-current":
			if len(m.items) == 0 {
				return m, nil
			}
			it := &m.items[m.cursor]
			if it.Upgraded || it.Upgrading {
				return m, nil
			}
			it.Upgrading = true
			it.UpgradeErr = ""
			m.refreshViewport()
			m.scrollToCursor()
			return m, upgradePackage(m.cursor, *it)
		}

	case changelogResolvedMsg:
		if msg.index >= 0 && msg.index < len(m.items) {
			it := m.items[msg.index]
			if msg.filename != "" {
				m.items[msg.index].LoadingStage = msg.filename
				m.items[msg.index].ChangelogFilename = msg.filename
				m.refreshViewport()
				return m, fetchChangelogFileCmd(msg.index, it, msg.filename)
			}
			m.items[msg.index].LoadingStage = "releases"
			m.refreshViewport()
			return m, fetchReleasesCmd(msg.index, it)
		}

	case bodyFetchedMsg:
		if msg.index >= 0 && msg.index < len(m.items) {
			m.items[msg.index].Body = msg.body
			m.items[msg.index].Loading = false
			m.items[msg.index].LoadingStage = ""
			m.refreshViewport()
		}

	case upgradeDoneMsg:
		if msg.index >= 0 && msg.index < len(m.items) {
			it := &m.items[msg.index]
			it.Upgrading = false
			if msg.err != nil {
				it.UpgradeErr = msg.err.Error()
			} else {
				it.Upgraded = true
			}
			m.refreshViewport()
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		for _, it := range m.items {
			if it.Loading || it.Upgrading {
				m.refreshViewport()
				break
			}
		}
		return m, cmd
	}

	return m, nil
}

// moveCursor moves the highlighted item by delta and scrolls it into view.
func (m *model) moveCursor(delta int) {
	if len(m.items) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor > len(m.items)-1 {
		m.cursor = len(m.items) - 1
	}
	m.scrollToCursor()
}

func (m model) View() tea.View {
	if m.quitting {
		v := tea.NewView("")
		v.AltScreen = true
		return v
	}
	if !m.ready {
		v := tea.NewView("Loading...")
		v.AltScreen = true
		return v
	}

	var b strings.Builder
	b.WriteString(styleHeader.Render("Outdated packages:"))
	b.WriteString("\n\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n\n")
	b.WriteString(strings.Join(m.helpText(), "\n"))

	content := b.String()
	if m.showHelp {
		content = pane.CompositeCenteredPopup(content, renderHelpPopup(m))
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// helpText builds the footer hint line(s), including [o] only when the
// highlighted item has a homepage distinct from its GitHub repo, [g] only
// when it has a resolved GitHub repo, and [c] whenever ChangelogURL has
// something to open (a manual override, or a resolved GitHub repo). Hints
// are dropped lowest-priority-first if they don't fit m.width.
func (m model) helpText() []string {
	hint := func(text string, priority int) statusline.Node {
		return statusline.Node{
			Provider: func() string { return styleDim.Render(text) },
			Align:    statusline.Left,
			Priority: priority,
		}
	}

	nodes := []statusline.Node{hint("[enter] expand/collapse", 100)}
	if len(m.items) > 0 {
		it := m.items[m.cursor]
		var githubUrl = it.GitHubURL()
		if it.Homepage != "" && it.Homepage != githubUrl {
			nodes = append(nodes, hint("[o] open homepage", 10))
		}
		if githubUrl != "" {
			nodes = append(nodes, hint("[g] open github", 20))
		}
		if it.ChangelogURL() != "" {
			nodes = append(nodes, hint("[c] open changelog", 30))
		}
	}
	nodes = append(nodes, hint("[a] upgrade all", 40))
	if len(m.items) > 0 && !m.items[m.cursor].Upgraded {
		nodes = append(nodes, hint("[u] upgrade current", 50))
	}
	nodes = append(nodes, hint("[?] help", 15))
	nodes = append(nodes, hint("[q] quit", 90))

	return statusline.Render(nodes, m.width, "  ", 1)
}

// renderBodyFrame returns styleBodyFrame.Render(body) for the item at index,
// reusing the last render for that index if body hasn't changed since.
func (m model) renderBodyFrame(index int, body string) string {
	if cached, ok := m.bodyFrameCache[index]; ok && cached.body == body {
		return cached.rendered
	}
	rendered := styleBodyFrame.Render(body)
	m.bodyFrameCache[index] = bodyFrame{body: body, rendered: rendered}
	return rendered
}

// renderList builds the scrollable document: one line per package, with the
// expanded ones' changelog bodies inlined beneath them. Returns the content
// and the line offset of each item's header, used to scroll a given item
// into view.
func (m model) renderList() (string, []int) {
	var b strings.Builder
	itemLines := make([]int, len(m.items))
	line := 0

	writeLine := func(s string) {
		b.WriteString(s)
		b.WriteString("\n")
		line += strings.Count(s, "\n") + 1
	}

	for i, it := range m.items {
		itemLines[i] = line

		// Styled per-segment (rather than rendering the whole line and
		// wrapping it in an outer style) because lipgloss.Render emits a
		// full SGR reset at the end of each call; nesting one Render's
		// output inside another clips the outer style at that reset.
		selected := i == m.cursor && !it.Expanded && !it.Upgraded

		cursorStyle, versionStyle, hintStyle := styleArrow, styleVersion, styleDim
		switch {
		case it.Upgraded:
			cursorStyle, versionStyle = styleDim, styleDim
		case selected:
			cursorStyle = cursorStyle.Bold(true)
			versionStyle = versionStyle.Bold(true)
			hintStyle = hintStyle.Bold(true)
		}

		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("> ")
		}

		sourceHint := hintStyle.Render("[no changelog source]")
		if it.Owner != "" {
			sourceHint = hintStyle.Render(fmt.Sprintf("[%s/%s]", it.Owner, it.Repo))
		} else if it.ManualChangelogURL != "" {
			sourceHint = hintStyle.Render("[known changelog]")
		}

		var statusTag string
		switch {
		case it.Upgraded:
			statusTag = "  " + styleDim.Render("[upgraded]")
		case it.Upgrading:
			statusTag = "  " + m.spinner.View() + " upgrading..."
		case it.UpgradeErr != "":
			statusTag = "  " + styleError.Render("[upgrade failed]")
		}

		nameKind := fmt.Sprintf("%s (%s)", it.Name, it.Kind)
		switch {
		case it.Upgraded:
			nameKind = styleDim.Render(nameKind)
		case selected:
			nameKind = styleSelected.Render(nameKind)
		}

		line := fmt.Sprintf("%s%s  %s -> %s  %s%s",
			cursor, nameKind,
			versionStyle.Render(it.Installed), versionStyle.Render(it.Current),
			sourceHint, statusTag)
		writeLine(line)

		if it.Expanded {
			switch {
			case it.Loading:
				var content string
				if it.LoadingStage == "releases" {
					content = m.spinner.View() + " fetching releases..."
				} else {
					content = m.spinner.View() + " checking " + it.LoadingStage + "..."
				}
				writeLine(styleBodyFrame.Render(content))
			case it.Body != "":
				writeLine(m.renderBodyFrame(i, it.Body))
			default:
				writeLine(styleBodyFrame.Render(styleDim.Render("(empty)")))
			}
		}
	}

	return b.String(), itemLines
}
