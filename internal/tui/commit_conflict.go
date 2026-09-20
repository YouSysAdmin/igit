package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/conflict"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// regionChoiceIDs maps a menu item to the side it keeps.
var regionChoiceIDs = map[string]conflict.Choice{
	"region-ours":   conflict.Ours,
	"region-theirs": conflict.Theirs,
	"region-both":   conflict.Both,
}

// openRegionMenu offers the sides of the conflict block under the diff cursor.
// The cursor sits on the new side of the diff, whose line numbers are the
// working-tree line numbers the blocks are measured in.
func (c *CommitModel) openRegionMenu() {
	path := c.staging.spec.Path
	dl, ok := c.diff.cursorDiffLine()
	if !ok {
		c.hint = "put the cursor on a line of the conflict"
		return
	}
	regions, err := c.repo.ConflictRegions(path)
	if err != nil {
		c.hint = err.Error()
		return
	}
	region, index, found := conflict.At(regions, dl.NewNum)
	if !found {
		c.hint = fmt.Sprintf("no conflict on this line, %s has %s", path, pluralConflicts(len(regions)))
		return
	}
	c.pending = pendingAction{id: "region", name: path, index: index}
	c.overlay.OpenMenu(overlay.MenuSpec{
		Title: fmt.Sprintf("conflict %d/%d in %s", index+1, len(regions), path),
		Items: []overlay.MenuItem{
			{Key: 'o', Label: "keep ours" + sideLabel(region.OursLabel), ID: "region-ours"},
			{Key: 't', Label: "keep theirs" + sideLabel(region.TheirsLabel), ID: "region-theirs"},
			{Key: 'b', Label: "keep both, ours first", ID: "region-both"},
		},
	})
}

// sideLabel renders the branch name git wrote next to a marker, if any.
func sideLabel(label string) string {
	if label == "" {
		return ""
	}
	return " (" + label + ")"
}

func pluralConflicts(n int) string {
	if n == 1 {
		return "1 conflict"
	}
	return fmt.Sprintf("%d conflicts", n)
}

// handleRegionMenu rewrites the chosen block. ok is false for other menus.
func (c *CommitModel) handleRegionMenu(id string) (tea.Cmd, bool) {
	choice, ok := regionChoiceIDs[id]
	if !ok {
		return nil, false
	}
	path, index := c.pending.name, c.pending.index
	return c.runOp(fmt.Sprintf("conflict %d in %s: %s", index+1, path, choice), func(context.Context) error {
		return c.repo.ResolveConflictRegion(path, index, choice)
	}), true
}
