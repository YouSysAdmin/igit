package tui

import (
	"context"
	"errors"
	"log"
	"strings"

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

// commitProcessDoneMsg reports an interactive process (editor, commit, push)
// exiting. output is what the process wrote to the mirrored streams, so a
// hook's complaint survives the return to the alternate screen.
type commitProcessDoneMsg struct {
	name         string
	err          error
	output       string
	restoreMouse bool
}

// editorAbortMarker is what git prints when the editor leaves the message empty.
const editorAbortMarker = "Aborting commit due to empty commit message"

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

// runOp executes a write operation off the UI goroutine and reports its
// result. While another write or interactive process runs (a slow hook, for
// instance) the request is refused with a hint instead of racing it.
func (c *CommitModel) runOp(name string, fn func(ctx context.Context) error) tea.Cmd {
	if !c.beginOp(name) {
		return nil
	}
	return func() tea.Msg {
		return commitOpDoneMsg{name: name, err: fn(context.Background())}
	}
}

// beginOp marks name as the running operation. It reports false, with a
// hint, when one is already in flight.
func (c *CommitModel) beginOp(name string) bool {
	if c.busy != "" {
		c.hint = c.busy + " in progress, wait for it to finish"
		return false
	}
	c.busy = name
	return true
}

// endOp clears the running operation and its progress hint.
func (c *CommitModel) endOp() {
	if c.hint == c.busy+" running" {
		c.hint = ""
	}
	c.busy = ""
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
	c.endOp()
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
	c.endOp()
	var cmds []tea.Cmd
	if msg.restoreMouse {
		cmds = append(cmds, tea.EnableMouseCellMotion)
	}
	if msg.err != nil {
		log.Printf("[WARN] commit mode: %s: %v", msg.name, msg.err)
		c.reportProcessFailure(msg)
	} else if msg.name == "commit" || msg.name == "amend" {
		c.hint = msg.name + " done"
	}
	cmds = append(cmds, c.Refresh())
	return tea.Batch(cmds...)
}

// reportProcessFailure turns a failed interactive process into a hint for an
// editor abort and a popup with the process output otherwise.
func (c *CommitModel) reportProcessFailure(msg commitProcessDoneMsg) {
	output := strings.TrimSpace(msg.output)
	if strings.Contains(output, editorAbortMarker) {
		c.hint = msg.name + " aborted: empty message"
		return
	}
	if msg.name == "editor" {
		c.hint = msg.name + " failed: " + msg.err.Error()
		return
	}
	if output == "" {
		output = msg.err.Error() + ", no output. a hook may have rejected the " + msg.name
	}
	c.overlay.OpenError(overlay.ErrorSpec{Title: msg.name + " failed", Summary: msg.err.Error(), Detail: output})
}

// showError opens the error popup with the git command's full stderr when
// available. A silent commit failure names the likely culprit, a hook.
func (c *CommitModel) showError(title string, err error) {
	detail := err.Error()
	if gerr, ok := errors.AsType[*git.Error](err); ok {
		detail = gerr.Detail()
		if gerr.Stderr == "" && (title == "commit failed" || title == "amend failed") {
			detail += ", no output. a hook may have rejected the commit"
		}
	}
	c.overlay.OpenError(overlay.ErrorSpec{Title: title, Summary: err.Error(), Detail: detail})
}
