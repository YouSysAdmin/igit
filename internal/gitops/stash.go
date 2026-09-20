package gitops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yousysadmin/igit/internal/git"
)

// StashEntry is one line of `git stash list`.
type StashEntry struct {
	Index   int // N in stash@{N}
	Hash    string
	Time    time.Time
	Message string
}

// Ref returns the stash@{N} reference.
func (s StashEntry) Ref() string { return fmt.Sprintf("stash@{%d}", s.Index) }

// StashPushOpts tunes `git stash push`.
type StashPushOpts struct {
	Message          string
	KeepIndex        bool     // --keep-index: leave staged changes in the index
	IncludeUntracked bool     // --include-untracked
	Staged           bool     // --staged: stash only the index (git >= 2.35)
	Paths            []string // limit to these paths
}

// Stashes lists the stash entries, newest first.
func (g *Git) Stashes(ctx context.Context) ([]StashEntry, error) {
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "stash", "list", "-z", "--pretty=format:%gd%x1f%H%x1f%ct%x1f%gs")
	if err != nil {
		return nil, err
	}
	return parseStashList(out)
}

func parseStashList(out string) ([]StashEntry, error) {
	var entries []StashEntry
	for rec := range strings.SplitSeq(out, "\x00") {
		if rec == "" {
			continue
		}
		fields := strings.SplitN(rec, "\x1f", 4)
		if len(fields) < 4 {
			return nil, fmt.Errorf("stash list: malformed record %q", rec)
		}
		idx, err := parseStashIndex(fields[0])
		if err != nil {
			return nil, err
		}
		ts, _ := strconv.ParseInt(fields[2], 10, 64)
		entries = append(entries, StashEntry{Index: idx, Hash: fields[1], Time: time.Unix(ts, 0), Message: fields[3]})
	}
	return entries, nil
}

// parseStashIndex extracts N from "stash@{N}".
func parseStashIndex(ref string) (int, error) {
	start := strings.IndexByte(ref, '{')
	end := strings.IndexByte(ref, '}')
	if start < 0 || end < start {
		return 0, fmt.Errorf("stash list: malformed ref %q", ref)
	}
	n, err := strconv.Atoi(ref[start+1 : end])
	if err != nil {
		return 0, fmt.Errorf("stash list: malformed ref %q: %w", ref, err)
	}
	return n, nil
}

// StashPush saves the working tree (and index) into a new stash.
func (g *Git) StashPush(ctx context.Context, o StashPushOpts) error {
	args := []string{"stash", "push", "-q"}
	if o.KeepIndex {
		args = append(args, "--keep-index")
	}
	if o.IncludeUntracked {
		args = append(args, "--include-untracked")
	}
	if o.Staged {
		args = append(args, "--staged")
	}
	if o.Message != "" {
		args = append(args, "-m", o.Message)
	}
	if len(o.Paths) > 0 {
		args = append(args, "--")
		args = append(args, o.Paths...)
	}
	_, err := g.Run(ctx, git.RunOpts{GlobPathspecs: true}, args...)
	return err
}

// StashPop applies stash@{index} and drops it.
func (g *Git) StashPop(ctx context.Context, index int) error {
	_, err := g.Run(ctx, git.RunOpts{}, "stash", "pop", "-q", stashRef(index))
	return err
}

// StashApply applies stash@{index} and keeps it.
func (g *Git) StashApply(ctx context.Context, index int) error {
	_, err := g.Run(ctx, git.RunOpts{}, "stash", "apply", "-q", stashRef(index))
	return err
}

// StashDrop deletes stash@{index}.
func (g *Git) StashDrop(ctx context.Context, index int) error {
	_, err := g.Run(ctx, git.RunOpts{}, "stash", "drop", "-q", stashRef(index))
	return err
}

// StashFiles lists the files a stash touches, for the diff pane. The diff of
// one file is then read through the review renderer with StashDiffRef.
func (g *Git) StashFiles(ctx context.Context, index int) ([]git.FileEntry, error) {
	ref := stashRef(index)
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "diff", "--no-color", "--no-ext-diff", "--name-status", "-M", "-z", ref+"^", ref)
	if err != nil {
		return nil, err
	}
	return git.ParseNameStatus(out), nil
}

// StashDiffRef returns the "A..B" ref that shows stash@{index} against its
// parent in a FileDiffRequest.
func StashDiffRef(index int) string {
	ref := stashRef(index)
	return ref + "^.." + ref
}

func stashRef(index int) string { return fmt.Sprintf("stash@{%d}", index) }

// StashStagedSupported reports whether `git stash push --staged` exists (git >= 2.35).
func (g *Git) StashStagedSupported(ctx context.Context) bool {
	return g.Supports(ctx, 2, 35)
}
