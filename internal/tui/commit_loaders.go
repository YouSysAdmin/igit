package tui

import (
	"context"
	"errors"
	"log"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/tui/overlay"
)

// commitStatusMsg carries a finished status load. seq ties it to the request
// so a stale result from before a refresh is dropped.
type commitStatusMsg struct {
	seq    uint64
	status gitops.Status
	err    error
}

// commitDiffMsg carries a finished staging-diff load for one file side.
type commitDiffMsg struct {
	seq  uint64
	spec gitops.DiffSpec
	raw  string
	err  error
}

// commitOpDoneMsg reports a finished write operation (stage, unstage, discard, ...).
type commitOpDoneMsg struct {
	name string
	err  error
}

// commitProcessDoneMsg reports an interactive process (editor, commit, push) exiting.
type commitProcessDoneMsg struct {
	name         string
	err          error
	restoreMouse bool
}

func (c *CommitModel) loadStatus(seq uint64) tea.Cmd {
	repo := c.repo
	return func() tea.Msg {
		st, err := repo.Status(context.Background())
		return commitStatusMsg{seq: seq, status: st, err: err}
	}
}

func (c *CommitModel) loadDiff(spec gitops.DiffSpec) tea.Cmd {
	c.staging.seq++
	seq := c.staging.seq
	repo := c.repo
	return func() tea.Msg {
		raw, err := repo.StagingDiff(context.Background(), spec)
		return commitDiffMsg{seq: seq, spec: spec, raw: raw, err: err}
	}
}

// runOp executes a write operation off the UI goroutine and reports its result.
func (c *CommitModel) runOp(name string, fn func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		return commitOpDoneMsg{name: name, err: fn(context.Background())}
	}
}

func (c *CommitModel) handleStatusLoaded(msg commitStatusMsg) tea.Cmd {
	if msg.seq != c.statusSeq {
		return nil
	}
	if msg.err != nil {
		log.Printf("[WARN] commit mode: status: %v", msg.err)
		c.showError("status failed", msg.err)
		return nil
	}
	c.staleHistoryTabs(msg.status.Head)
	c.status = msg.status
	c.statusLoaded = true
	c.rebuildRows()
	if c.focusStagedOnLoad {
		c.focusStagedOnLoad = false
		c.tab = tabFiles
		c.hist.detail = nil
		if staged := c.scoped(c.status.Staged()); len(staged) > 0 {
			c.list.SelectByKey("s:" + staged[0].Path)
		}
		c.focus = focusSide
	}
	if c.tab != tabFiles {
		return nil // the history tab owns the diff pane
	}
	return c.loadDiffForCursor()
}

func (c *CommitModel) handleDiffLoaded(msg commitDiffMsg) tea.Cmd {
	if msg.seq != c.staging.seq {
		return nil
	}
	if msg.err != nil {
		log.Printf("[WARN] commit mode: diff %s: %v", msg.spec.Path, msg.err)
		c.installRaw(msg.spec, "")
		c.hint = "diff failed: " + msg.err.Error()
		return nil
	}
	c.installRaw(msg.spec, msg.raw)
	return nil
}

func (c *CommitModel) handleOpDone(msg commitOpDoneMsg) tea.Cmd {
	if msg.err != nil {
		if errors.Is(msg.err, gitops.ErrNoChangesSelected) {
			c.hint = "nothing to apply: select changed lines"
			return nil
		}
		c.showError(msg.name+" failed", msg.err)
		return c.Refresh()
	}
	if msg.name == "commit" || msg.name == "amend" {
		c.hint = msg.name + " done"
	}
	return c.Refresh()
}

func (c *CommitModel) handleProcessDone(msg commitProcessDoneMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.restoreMouse {
		cmds = append(cmds, tea.EnableMouseCellMotion)
	}
	if msg.err != nil {
		log.Printf("[WARN] commit mode: %s: %v", msg.name, msg.err)
		c.hint = msg.name + " failed: " + msg.err.Error()
	}
	cmds = append(cmds, c.Refresh())
	return tea.Batch(cmds...)
}

// showError opens the error popup with the git command's full stderr when available.
func (c *CommitModel) showError(title string, err error) {
	detail := err.Error()
	if gerr, ok := errors.AsType[*git.Error](err); ok {
		detail = gerr.Detail()
	}
	c.overlay.OpenError(overlay.ErrorSpec{Title: title, Summary: err.Error(), Detail: detail})
}
