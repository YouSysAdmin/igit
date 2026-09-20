package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yousysadmin/igit/internal/keymap"
)

func TestRenderLegend(t *testing.T) {
	km := keymap.Default()

	t.Run("names the bound key of every item", func(t *testing.T) {
		got := renderLegend(km, 100, legendItem{action: keymap.ActionSearch, label: "search"}, legendItem{action: keymap.ActionHelp, label: "help"})
		assert.Contains(t, got, "] search")
		assert.Contains(t, got, "] help")
	})

	t.Run("drops trailing items that do not fit", func(t *testing.T) {
		wide := renderLegend(km, 100, legendItem{action: keymap.ActionSearch, label: "search"}, legendItem{action: keymap.ActionHelp, label: "help"})
		narrow := renderLegend(km, len("[/] search"), legendItem{action: keymap.ActionSearch, label: "search"}, legendItem{action: keymap.ActionHelp, label: "help"})
		assert.Contains(t, narrow, "search")
		assert.NotContains(t, narrow, "help")
		assert.NotEqual(t, wide, narrow)
	})

	t.Run("an item that does not fit at all leaves the legend empty", func(t *testing.T) {
		assert.Empty(t, renderLegend(km, 2, legendItem{action: keymap.ActionSearch, label: "search"}))
	})

	t.Run("unbound actions are skipped", func(t *testing.T) {
		bare := keymap.Default()
		for _, k := range bare.KeysFor(keymap.ActionSearch) {
			bare.Unbind(k)
		}
		assert.Empty(t, renderLegend(bare, 100, legendItem{action: keymap.ActionSearch, label: "search"}))
	})
}

func TestLegendKey(t *testing.T) {
	km := keymap.Default()
	assert.NotEmpty(t, legendKey(km, keymap.ActionHelp))

	bare := keymap.Default()
	for _, k := range bare.KeysFor(keymap.ActionHelp) {
		bare.Unbind(k)
	}
	assert.Empty(t, legendKey(bare, keymap.ActionHelp), "an unbound action has no display key")
}

func TestLegendKey_prefersTheShortestBinding(t *testing.T) {
	km := keymap.Default()
	km.Bind("ctrl+alt+s", keymap.ActionSearch)
	assert.Less(t, len(legendKey(km, keymap.ActionSearch)), len("ctrl+alt+s"))
}

func TestRenderLegend_preferredKey(t *testing.T) {
	km := keymap.Default()

	got := renderLegend(km, 100, legendItem{action: keymap.ActionConfirm, label: "open", prefer: "enter"})
	assert.Contains(t, got, "[enter] open", "the preferred binding wins over the shortest one")

	assert.Contains(t, renderLegend(km, 100, legendItem{action: keymap.ActionConfirm, label: "open"}),
		"[a] open", "without a preference the shortest binding is shown")

	bare := keymap.Default()
	bare.Unbind("enter")
	assert.Contains(t, renderLegend(bare, 100, legendItem{action: keymap.ActionConfirm, label: "open", prefer: "enter"}),
		"[a] open", "an unbound preference falls back to the shortest binding")
}
