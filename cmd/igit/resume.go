package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/yousysadmin/igit/internal/annot"
	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/session"
	"github.com/yousysadmin/igit/internal/tui"
)

// resumeReview finds the saved review to continue. The entry of the current
// scope (working tree, ref range or pull request) is taken when it exists.
// Otherwise, with explicit set (--resume), the repository's other entries are
// offered through pick and an empty history is an error. without it (a
// pull-request session picking up its own draft) nothing happens. found
// reports whether an entry was chosen. A saved commit that differs from HEAD
// only warns: the preloader drops annotations the current diff cannot place.
func resumeReview(hist *session.Service, p session.Params, explicit bool, pick pickFunc, warn io.Writer) (session.Entry, bool, error) {
	if hist == nil {
		if explicit {
			return session.Entry{}, false, errors.New("--resume: the history is disabled (--history-max 0)")
		}
		return session.Entry{}, false, nil
	}
	entries, err := hist.List(p)
	if err != nil {
		return session.Entry{}, false, fmt.Errorf("--resume: %w", err)
	}
	var entry session.Entry
	found := false
	scope := session.ScopeName(p)
	for _, e := range entries {
		if e.Scope == scope {
			entry, found = e, true
			break
		}
	}
	switch {
	case found:
	case !explicit:
		return session.Entry{}, false, nil
	case len(entries) == 0:
		return session.Entry{}, false, errors.New("--resume: no saved reviews for this repository")
	default:
		entry, err = pickEntry(entries, pick)
		if err != nil {
			return session.Entry{}, false, err
		}
	}
	if warn != nil {
		_, _ = fmt.Fprintf(warn, "continuing the saved review of %s (%s)\n", entry.Scope, entry.Label())
		if now := currentCommit(p); now != "" && entry.Commit != "" && now != entry.Commit {
			_, _ = fmt.Fprintf(warn, "warning: it was saved at %s and the review is now at %s, annotations on changed lines are dropped\n", entry.Commit, now)
		}
	}
	return entry, true, nil
}

// currentCommit is what the session reviews right now: the pull request head
// for a pull-request review, otherwise the repository HEAD.
func currentCommit(p session.Params) string {
	if p.Commit != "" {
		return p.Commit
	}
	return session.HeadCommit(p.GitRoot)
}

// pickEntry offers the repository's saved reviews of other scopes.
func pickEntry(entries []session.Entry, pick pickFunc) (session.Entry, error) {
	if pick == nil {
		return session.Entry{}, errors.New("--resume: no saved review for this scope, a terminal is needed to choose another")
	}
	items := make([]tui.PickItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, tui.PickItem{ID: e.File, Label: e.Label(), Detail: e.Commit})
	}
	id, err := pick(items)
	if err != nil {
		return session.Entry{}, fmt.Errorf("--resume: %w", err)
	}
	for _, e := range entries {
		if e.File == id {
			return e, nil
		}
	}
	return session.Entry{}, errors.New("--resume: no saved review chosen")
}

// savedAnnotationsReq is what loadSavedAnnotations needs from the session setup.
type savedAnnotationsReq struct {
	store              *annot.Store
	source             tui.DiffSource
	gitRoot            string
	workDir            string
	pr                 int
	prHead             string
	prKind             string
	untrackedFn        func() ([]string, error)
	untrackedRenamesFn func([]string) ([]git.FileEntry, error)
	pick               pickFunc
}

// loadSavedAnnotations seeds the store before the TUI starts: from the file
// --annotations names, or from the history when --resume was given or a
// pull-request session has a saved draft. restored reports that the session
// continues the saved entry of its scope, so deleting every annotation clears
// that entry on exit.
func loadSavedAnnotations(opts options, r savedAnnotationsReq) (restored bool, err error) {
	if opts.Review.Annotations != "" {
		return false, preloadAnnotations(opts.Review.Annotations, r.store, r.source, opts.scopeRef(), opts.Review.Staged, r.untrackedFn, r.untrackedRenamesFn, r.workDir, os.Stderr)
	}
	if !opts.Review.Resume && r.pr == 0 {
		return false, nil
	}
	params := historyParams(histReq{opts: opts, gitRoot: r.gitRoot, workDir: r.workDir, pr: r.pr, prHead: r.prHead, prKind: r.prKind})
	entry, found, err := resumeReview(historyService(opts), params, opts.Review.Resume, r.pick, os.Stderr)
	if err != nil || !found {
		return false, err
	}
	return true, preloadRecords(entry.Records, r.store, r.source, opts.scopeRef(), opts.Review.Staged, r.untrackedFn, r.untrackedRenamesFn, r.workDir, os.Stderr)
}

// terminalPicker returns a pickFunc factory: each call runs tui.PickList as
// its own bubbletea program with the session's terminal options.
func terminalPicker(opts []tea.ProgramOption) func(title string) pickFunc {
	return func(title string) pickFunc {
		return func(items []tui.PickItem) (string, error) {
			final, err := tea.NewProgram(tui.NewPickList(title, items), opts...).Run()
			if err != nil {
				return "", fmt.Errorf("picker: %w", err)
			}
			list, _ := final.(tui.PickList)
			return list.Chosen(), nil
		}
	}
}
