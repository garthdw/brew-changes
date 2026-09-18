package ui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

func testItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{Name: "pkg", Kind: "formula", Installed: "1.0", Current: "2.0"}
	}
	return items
}

func readyModel(items []Item) model {
	m := newModel(items)
	m.viewport = viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))
	m.ready = true
	m.refreshViewport()
	return m
}

func TestMoveCursor_ClampsAtBounds(t *testing.T) {
	m := readyModel(testItems(3))

	m.moveCursor(-5)
	if m.cursor != 0 {
		t.Errorf("moving past the start: got cursor %d, want 0", m.cursor)
	}

	m.moveCursor(10)
	if m.cursor != 2 {
		t.Errorf("moving past the end: got cursor %d, want 2", m.cursor)
	}
}

func TestMoveCursor_EmptyList(t *testing.T) {
	m := readyModel(nil)
	m.moveCursor(1)
	if m.cursor != 0 {
		t.Errorf("got cursor %d, want 0", m.cursor)
	}
}

func TestUpdate_ArrowKeysMoveCursor(t *testing.T) {
	m := readyModel(testItems(3))

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	nm := updated.(model)
	if nm.cursor != 1 {
		t.Errorf("got cursor %d, want 1", nm.cursor)
	}

	updated, _ = nm.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	nm = updated.(model)
	if nm.cursor != 0 {
		t.Errorf("got cursor %d, want 0", nm.cursor)
	}
}

func TestUpdate_JKMoveCursorWhenCollapsed(t *testing.T) {
	m := readyModel(testItems(3))

	updated, _ := m.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	nm := updated.(model)
	if nm.cursor != 1 {
		t.Errorf("'j' on collapsed item: got cursor %d, want 1", nm.cursor)
	}

	updated, _ = nm.Update(tea.KeyPressMsg{Text: "k", Code: 'k'})
	nm = updated.(model)
	if nm.cursor != 0 {
		t.Errorf("'k' on collapsed item: got cursor %d, want 0", nm.cursor)
	}
}

func TestUpdate_JKScrollWhenExpanded(t *testing.T) {
	m := readyModel(testItems(1))
	m.items[0].Expanded = true
	m.items[0].Body = strings.Repeat("line\n", 50)
	m.refreshViewport()

	updated, _ := m.Update(tea.KeyPressMsg{Text: "j", Code: 'j'})
	nm := updated.(model)
	if nm.cursor != 0 {
		t.Errorf("'j' on expanded item should scroll, not move cursor: got cursor %d", nm.cursor)
	}
	if nm.viewport.YOffset() == 0 {
		t.Error("'j' on expanded item should scroll the viewport down")
	}
}

func TestUpdate_EnterTogglesExpandedAndTriggersFetch(t *testing.T) {
	m := readyModel(testItems(1))

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	nm := updated.(model)
	if !nm.items[0].Expanded {
		t.Error("expected item to be expanded after enter")
	}
	if !nm.items[0].Loading {
		t.Error("expected item to be marked loading since body is empty")
	}
	if cmd == nil {
		t.Error("expected a fetch command to be returned")
	}

	// Collapsing again shouldn't trigger another fetch.
	updated, cmd = nm.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	nm2 := updated.(model)
	if nm2.items[0].Expanded {
		t.Error("expected item to be collapsed after second enter")
	}
	if cmd != nil {
		t.Error("expected no fetch command when collapsing")
	}
}

func TestResolveChangelogCmd_NoRepoWithManualURL(t *testing.T) {
	it := Item{Name: "rust", ManualChangelogURL: "https://example.com/rust/RELEASES.md"}
	msg := resolveChangelogCmd(0, it)().(bodyFetchedMsg)
	if !strings.Contains(msg.body, "https://example.com/rust/RELEASES.md") {
		t.Errorf("expected body to mention the manual changelog URL, got %q", msg.body)
	}
}

func TestResolveChangelogCmd_ManualURLServesHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html><body>not a changelog</body></html>"))
	}))
	defer srv.Close()

	it := Item{Name: "rust", ManualChangelogURL: srv.URL}
	msg := resolveChangelogCmd(0, it)().(bodyFetchedMsg)
	if !strings.Contains(msg.body, "HTML page") {
		t.Errorf("expected body to mention the HTML-specific reason, got %q", msg.body)
	}
}

func TestResolveChangelogCmd_NoRepoNoManualURL(t *testing.T) {
	it := Item{Name: "mystery-pkg"}
	msg := resolveChangelogCmd(0, it)().(bodyFetchedMsg)
	if strings.Contains(msg.body, "http") {
		t.Errorf("expected no URL in body when none is known, got %q", msg.body)
	}
}

func TestUpdate_EnterOnEmptyList(t *testing.T) {
	m := readyModel(nil)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected no command on empty list")
	}
	if len(updated.(model).items) != 0 {
		t.Error("expected items to remain empty")
	}
}

func TestUpdate_BodyFetchedMsg(t *testing.T) {
	m := readyModel(testItems(2))
	m.items[0].Loading = true

	updated, _ := m.Update(bodyFetchedMsg{index: 0, body: "changelog content"})
	nm := updated.(model)
	if nm.items[0].Body != "changelog content" {
		t.Errorf("got body %q", nm.items[0].Body)
	}
	if nm.items[0].Loading {
		t.Error("expected Loading to be cleared")
	}
}

func TestUpdate_BodyFetchedMsg_OutOfRangeIndexIgnored(t *testing.T) {
	m := readyModel(testItems(1))
	updated, _ := m.Update(bodyFetchedMsg{index: 5, body: "x"})
	if len(updated.(model).items) != 1 {
		t.Error("out-of-range index should be a no-op, not panic or mutate")
	}
}

func TestUpdate_AKeyQuitsWithUpgradeAll(t *testing.T) {
	m := readyModel(testItems(2))
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "a", Code: 'a'})
	nm := updated.(model)
	if nm.action != ActionUpgradeAll {
		t.Errorf("got action %v, want ActionUpgradeAll", nm.action)
	}
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

func TestUpdate_UKeyStartsInPlaceUpgradeWithoutQuitting(t *testing.T) {
	m := readyModel(testItems(2))
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: 'u'})
	nm := updated.(model)

	if nm.action != ActionNone {
		t.Errorf("'u' should not set a quit action, got %v", nm.action)
	}
	if !nm.items[0].Upgrading {
		t.Error("expected the hovered item to be marked Upgrading")
	}
	if cmd == nil {
		t.Error("expected an upgrade command to be returned")
	}
}

func TestUpdate_UKeyNoopOnEmptyList(t *testing.T) {
	m := readyModel(nil)
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: 'u'})
	if cmd != nil {
		t.Error("'u' on an empty list should return no command")
	}
	if updated.(model).action != ActionNone {
		t.Error("expected action to remain ActionNone on empty list")
	}
}

func TestUpdate_UKeyNoopWhenAlreadyUpgradedOrUpgrading(t *testing.T) {
	tests := []struct {
		name string
		item Item
	}{
		{"already upgraded", Item{Upgraded: true}},
		{"already upgrading", Item{Upgrading: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := readyModel([]Item{tt.item})
			_, cmd := m.Update(tea.KeyPressMsg{Text: "u", Code: 'u'})
			if cmd != nil {
				t.Error("expected no command when the item is already upgraded or upgrading")
			}
		})
	}
}

func TestUpdate_QuestionMarkOpensHelp(t *testing.T) {
	m := readyModel(testItems(2))
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "?", Code: '?'})
	nm := updated.(model)
	if !nm.showHelp {
		t.Error("expected showHelp to be true after '?'")
	}
	if cmd != nil {
		t.Error("expected no command when opening help")
	}
}

func TestUpdate_AnyKeyClosesHelp(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"q", tea.KeyPressMsg{Text: "q", Code: 'q'}},
		{"?", tea.KeyPressMsg{Text: "?", Code: '?'}},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := readyModel(testItems(2))
			m.showHelp = true

			updated, cmd := m.Update(tt.key)
			nm := updated.(model)

			if nm.showHelp {
				t.Error("expected showHelp to be false after keypress")
			}
			if cmd != nil {
				t.Error("expected no command when closing help")
			}
			if nm.action != ActionNone || nm.quitting {
				t.Error("closing help should not also quit or trigger an action")
			}
		})
	}
}

func TestUpdate_UpgradeDoneMsg_Success(t *testing.T) {
	m := readyModel(testItems(1))
	m.items[0].Upgrading = true

	updated, _ := m.Update(upgradeDoneMsg{index: 0, err: nil})
	nm := updated.(model)
	if nm.items[0].Upgrading {
		t.Error("expected Upgrading to be cleared")
	}
	if !nm.items[0].Upgraded {
		t.Error("expected Upgraded to be set on success")
	}
}

func TestUpdate_UpgradeDoneMsg_Failure(t *testing.T) {
	m := readyModel(testItems(1))
	m.items[0].Upgrading = true

	updated, _ := m.Update(upgradeDoneMsg{index: 0, err: errors.New("brew upgrade: boom")})
	nm := updated.(model)
	if nm.items[0].Upgraded {
		t.Error("expected Upgraded to remain false on failure")
	}
	if nm.items[0].UpgradeErr == "" {
		t.Error("expected UpgradeErr to be set on failure")
	}
}

func TestUpdate_UpgradeDoneMsg_OutOfRangeIndexIgnored(t *testing.T) {
	m := readyModel(testItems(1))
	updated, _ := m.Update(upgradeDoneMsg{index: 5, err: nil})
	if len(updated.(model).items) != 1 {
		t.Error("out-of-range index should be a no-op, not panic or mutate")
	}
}

func TestRenderList_MarksCursorAndExpandedBody(t *testing.T) {
	items := []Item{
		{Name: "alpha", Kind: "formula", Installed: "1.0", Current: "2.0"},
		{Name: "beta", Kind: "cask", Installed: "1.0", Current: "2.0", Expanded: true, Body: "release notes"},
	}
	m := newModel(items)
	m.cursor = 1

	content, itemLines := m.renderList()

	if !strings.Contains(content, "alpha") || !strings.Contains(content, "beta") {
		t.Errorf("expected both items in rendered content, got %q", content)
	}
	if !strings.Contains(content, "release notes") {
		t.Error("expected expanded item's body to be rendered")
	}
	if len(itemLines) != 2 {
		t.Fatalf("got %d item line offsets, want 2", len(itemLines))
	}
	if itemLines[0] != 0 {
		t.Errorf("first item should start at line 0, got %d", itemLines[0])
	}
	if itemLines[1] <= itemLines[0] {
		t.Errorf("second item's offset (%d) should be after the first's (%d)", itemLines[1], itemLines[0])
	}
}

func TestRenderList_UpgradedItemShowsTagAndAllowsExpand(t *testing.T) {
	items := []Item{{Name: "alpha", Kind: "formula", Upgraded: true, Expanded: true, Body: "changelog body"}}
	m := newModel(items)

	content, _ := m.renderList()
	if !strings.Contains(content, "upgraded") {
		t.Errorf("expected an [upgraded] tag in rendered content, got %q", content)
	}
	if !strings.Contains(content, "changelog body") {
		t.Error("expected the changelog body to still be viewable for an upgraded item")
	}
}

func TestRenderList_UpgradingShowsIndicator(t *testing.T) {
	items := []Item{{Name: "alpha", Kind: "formula", Upgrading: true}}
	m := newModel(items)

	content, _ := m.renderList()
	if !strings.Contains(content, "upgrading") {
		t.Errorf("expected an upgrading indicator in rendered content, got %q", content)
	}
}

func TestRenderList_LoadingShowsSpinner(t *testing.T) {
	items := []Item{{Name: "alpha", Kind: "formula", Expanded: true, Loading: true, LoadingStage: "CHANGELOG.md"}}
	m := newModel(items)

	content, _ := m.renderList()
	if !strings.Contains(content, "checking CHANGELOG.md") {
		t.Errorf("expected loading indicator in content, got %q", content)
	}
}

func TestRenderList_LoadingReleasesShowsSpinner(t *testing.T) {
	items := []Item{{Name: "alpha", Kind: "formula", Expanded: true, Loading: true, LoadingStage: "releases"}}
	m := newModel(items)

	content, _ := m.renderList()
	if !strings.Contains(content, "fetching releases") {
		t.Errorf("expected releases loading indicator in content, got %q", content)
	}
}

func TestScrollToCursor_ScrollsDownWhenBelowViewport(t *testing.T) {
	m := readyModel(testItems(20))
	m.viewport.SetHeight(5)
	m.viewport.SetContent(strings.Repeat("line\n", 100))
	m.itemLines = make([]int, 20)
	for i := range m.itemLines {
		m.itemLines[i] = i * 3
	}

	m.cursor = 19
	m.scrollToCursor()

	target := m.itemLines[19]
	if m.viewport.YOffset() != target-m.viewport.Height()+1 {
		t.Errorf("got YOffset %d, want %d", m.viewport.YOffset(), target-m.viewport.Height()+1)
	}
}

func TestScrollToCursor_ScrollsUpWhenAboveViewport(t *testing.T) {
	m := readyModel(testItems(20))
	m.viewport.SetHeight(5)
	m.itemLines = make([]int, 20)
	for i := range m.itemLines {
		m.itemLines[i] = i * 3
	}
	m.viewport.SetYOffset(30)

	m.cursor = 0
	m.scrollToCursor()

	if m.viewport.YOffset() != 0 {
		t.Errorf("got YOffset %d, want 0", m.viewport.YOffset())
	}
}

func TestWindowSizeMsg_SetsViewportDimensions(t *testing.T) {
	m := newModel(testItems(2))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	nm := updated.(model)

	if !nm.ready {
		t.Fatal("expected model to be ready after WindowSizeMsg")
	}
	wantHeight := 20 - headerLines - footerLines
	if nm.viewport.Height() != wantHeight {
		t.Errorf("got viewport height %d, want %d", nm.viewport.Height(), wantHeight)
	}
	if nm.viewport.Width() != 100 {
		t.Errorf("got viewport width %d, want 100", nm.viewport.Width())
	}
}

func TestWindowSizeMsg_ClampsTinyHeightToOne(t *testing.T) {
	m := newModel(testItems(1))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 1})
	nm := updated.(model)

	if nm.viewport.Height() != 1 {
		t.Errorf("got viewport height %d, want 1 (clamped)", nm.viewport.Height())
	}
}
