package tui

import (
	"strings"

	"github.com/yousysadmin/igit/internal/git"
	"github.com/yousysadmin/igit/internal/gitops"
	"github.com/yousysadmin/igit/internal/patch"
)

// stagingState is the diff currently shown in the diff pane: which file and
// side, the raw patch it was built from, and the mapping between displayed
// rows and patch line indices that line operations need.
type stagingState struct {
	spec        gitops.DiffSpec // what is loaded, Path == "" means nothing
	raw         string
	viewToPatch []int // display row -> patch line index (Patch.Lines)
	hunks       int
	binary      bool
	seq         uint64 // last diff request, stale results are dropped

	mode       selectMode // how the diff cursor selects lines
	anchor     int        // range start row for selectRange
	restoreRow int        // cursor row to restore after a line operation reloads the diff, -1 = none
}

// installRaw parses a raw diff into display lines, feeds them to the embedded
// diff model and records the row-to-patch mapping.
func (c *CommitModel) installRaw(spec gitops.DiffSpec, raw string) {
	sameView := c.staging.spec.Path == spec.Path && c.staging.spec.Cached == spec.Cached
	prevRow := c.diff.paneCursor()
	c.staging.spec = spec
	c.staging.raw = raw
	lines, mapping, hunks, binary := rawToDiffLines(raw)
	c.staging.viewToPatch = mapping
	c.staging.hunks = hunks
	c.staging.binary = binary
	c.staging.mode = selectLine
	c.diff.setHeaderNote("")
	// review annotations are keyed by worktree line numbers. only the unstaged
	// side of a worktree diff shares that numbering
	c.diff.setPaneAnnotationsHidden(spec.Cached)
	c.installDiffLines(spec.Path, spec.OrigPath, lines)
	switch {
	case c.staging.restoreRow >= 0:
		c.restoreCursorRow()
	case sameView && len(lines) > 0:
		// a refresh of the view being looked at keeps the cursor where it was
		c.diff.setPaneCursor(prevRow)
	}
}

// installDiffLines hands lines to the embedded review model through its own
// file-loaded path so highlighting, widths and cursor reset behave as in
// review mode.
func (c *CommitModel) installDiffLines(path, orig string, lines []git.DiffLine) {
	next, _ := c.diff.handleFileLoaded(fileLoadedMsg{file: path, oldName: orig, seq: c.diff.nextPaneLoad(), lines: lines})
	if m, ok := next.(Model); ok {
		c.diff = m
	}
	c.diff.focusPaneDiff()
}

// clearDiff empties the diff pane.
func (c *CommitModel) clearDiff() {
	c.staging = stagingState{seq: c.staging.seq, restoreRow: -1}
	c.hist.note = ""
	c.diff.setHeaderNote("")
	c.diff.setPaneAnnotationsHidden(false)
	c.installDiffLines("", "", nil)
}

// rawToDiffLines converts a raw unified diff into the review model's DiffLine
// rows. Header lines and "\ No newline" markers are hidden. hunk headers become
// divider rows. mapping[i] is the patch line index of display row i (-1 for a
// divider). A binary diff yields one placeholder row.
func rawToDiffLines(raw string) (lines []git.DiffLine, mapping []int, hunks int, binary bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil, 0, false
	}
	p, err := patch.Parse(raw)
	if err != nil {
		return []git.DiffLine{{ChangeType: git.ChangeContext, Content: "(cannot parse diff: " + err.Error() + ")", IsPlaceholder: true}}, []int{-1}, 0, false
	}
	if p.HunkCount() == 0 {
		if strings.Contains(raw, "Binary files") || strings.Contains(raw, "GIT binary patch") {
			return []git.DiffLine{{ChangeType: git.ChangeContext, Content: "(binary file)", IsBinary: true}}, []int{-1}, 0, true
		}
		return nil, nil, 0, false
	}
	views := p.ViewLines()
	lines = make([]git.DiffLine, 0, len(views))
	mapping = make([]int, 0, len(views))
	for _, v := range views {
		switch v.Kind {
		case patch.KindHeader, patch.KindNoNewline:
			continue
		case patch.KindHunkHeader:
			lines = append(lines, git.DiffLine{ChangeType: git.ChangeDivider, Content: v.Content})
			mapping = append(mapping, -1)
		case patch.KindAddition:
			lines = append(lines, git.DiffLine{NewNum: v.NewNum, Content: v.Content, ChangeType: git.ChangeAdd})
			mapping = append(mapping, v.PatchIdx)
		case patch.KindDeletion:
			lines = append(lines, git.DiffLine{OldNum: v.OldNum, Content: v.Content, ChangeType: git.ChangeRemove})
			mapping = append(mapping, v.PatchIdx)
		case patch.KindContext:
			lines = append(lines, git.DiffLine{OldNum: v.OldNum, NewNum: v.NewNum, Content: v.Content, ChangeType: git.ChangeContext})
			mapping = append(mapping, v.PatchIdx)
		}
	}
	return lines, mapping, p.HunkCount(), false
}

// patchFile is one file section of a multi-file patch.
type patchFile struct {
	display string // path shown in the divider, "old -> new" for a rename
	path    string // path the syntax highlighter is keyed by
	raw     string
}

// splitPatchFiles cuts a patch into its per-file sections.
func splitPatchFiles(raw string) []patchFile {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	var out []patchFile
	start := -1
	for i, line := range lines {
		if !strings.HasPrefix(line, "diff --git ") {
			continue
		}
		if start >= 0 {
			out = append(out, patchSection(lines[start:i]))
		}
		start = i
	}
	if start >= 0 {
		out = append(out, patchSection(lines[start:]))
	}
	return out
}

// patchSection builds one section from its lines, reading the paths out of the
// header. Only the header is scanned, so body lines starting with "--" cannot
// be mistaken for path markers.
func patchSection(lines []string) patchFile {
	var oldPath, newPath string
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			break
		}
		switch {
		case strings.HasPrefix(line, "--- "):
			oldPath = trimPatchPath(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ "):
			newPath = trimPatchPath(strings.TrimPrefix(line, "+++ "))
		}
	}
	if oldPath == "" && newPath == "" {
		oldPath, newPath = gitHeaderPaths(lines[0])
	}
	f := patchFile{path: newPath, display: newPath, raw: strings.Join(lines, "\n")}
	switch {
	case newPath == "":
		f.path, f.display = oldPath, oldPath+" (deleted)"
	case oldPath != "" && oldPath != newPath:
		f.display = oldPath + " -> " + newPath
	}
	return f
}

// trimPatchPath strips the a/ or b/ prefix git puts on diff paths, and maps
// /dev/null to the empty string.
func trimPatchPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "/dev/null" {
		return ""
	}
	if len(p) > 2 && (strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/")) {
		return p[2:]
	}
	return p
}

// gitHeaderPaths reads both paths out of a "diff --git a/x b/y" line, the only
// source for a binary file section.
func gitHeaderPaths(header string) (oldPath, newPath string) {
	rest, ok := strings.CutPrefix(header, "diff --git ")
	if !ok {
		return "", ""
	}
	fields := strings.Fields(rest)
	if len(fields) != 2 {
		return "", ""
	}
	return trimPatchPath(fields[0]), trimPatchPath(fields[1])
}
