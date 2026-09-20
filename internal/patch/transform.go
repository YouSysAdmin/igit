package patch

import "strings"

// TransformOpts selects which lines of a patch to keep and how to rewrite its
// header.
type TransformOpts struct {
	// Reverse builds a patch for `git apply --reverse`. It changes how
	// unselected lines are treated when only part of a hunk is selected:
	// normally unselected '-' lines become context and unselected '+' lines are
	// dropped. in reverse, '+' lines become context and '-' lines are dropped.
	Reverse bool

	// FileNameOverride replaces the original header with "--- a/<name>" and
	// "+++ b/<name>". Staging and unstaging want this because the original
	// header confuses git for added and deleted files.
	FileNameOverride string

	// TurnAddedFilesIntoDiffAgainstEmptyFile rewrites a new-file header into a
	// diff against an empty file, which applies more reliably in most cases.
	TurnAddedFilesIntoDiffAgainstEmptyFile bool

	// StripRename removes rename metadata from the header and points the diff
	// at the new path, so a partial patch of a renamed file changes only the
	// contents and leaves the rename in place.
	StripRename bool

	// IncludedLineIndices are the patch line indices (as returned by Lines) of
	// the changes to keep. Context and no-newline markers are handled
	// automatically.
	IncludedLineIndices []int
}

type transformer struct {
	patch    *Patch
	opts     TransformOpts
	included map[int]struct{}
}

func transform(p *Patch, opts TransformOpts) *Patch {
	included := make(map[int]struct{}, len(opts.IncludedLineIndices))
	for _, idx := range opts.IncludedLineIndices {
		included[idx] = struct{}{}
	}
	tr := &transformer{patch: p, opts: opts, included: included}
	return &Patch{header: tr.transformHeader(), hunks: tr.transformHunks()}
}

// ExpandRange returns every index from start to end inclusive.
func ExpandRange(start, end int) []int {
	if end < start {
		return []int{}
	}
	out := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, i)
	}
	return out
}

func (t *transformer) transformHeader() []string {
	if t.opts.FileNameOverride != "" {
		return []string{"--- a/" + t.opts.FileNameOverride, "+++ b/" + t.opts.FileNameOverride}
	}
	header := t.patch.header
	if t.opts.StripRename {
		header = stripRenameFromHeader(header)
	}
	if t.opts.TurnAddedFilesIntoDiffAgainstEmptyFile {
		result := make([]string, 0, len(header))
		for idx, line := range header {
			if strings.HasPrefix(line, "new file mode") {
				continue
			}
			if line == "--- /dev/null" && idx+1 < len(header) && strings.HasPrefix(header[idx+1], "+++ b/") {
				line = "--- a/" + header[idx+1][6:]
			}
			result = append(result, line)
		}
		return result
	}
	return header
}

// stripRenameFromHeader rewrites a rename header into a plain modification of
// the new path, keeping the index line so `git apply --3way` can still fall
// back to a blob merge.
func stripRenameFromHeader(header []string) []string {
	newPath := ""
	for _, line := range header {
		if path, ok := strings.CutPrefix(line, "+++ b/"); ok {
			newPath = path
			break
		}
	}
	result := make([]string, 0, len(header))
	for _, line := range header {
		switch {
		case strings.HasPrefix(line, "similarity index "),
			strings.HasPrefix(line, "dissimilarity index "),
			strings.HasPrefix(line, "rename from "),
			strings.HasPrefix(line, "rename to "):
			// rename metadata is dropped
		case strings.HasPrefix(line, "diff --git "):
			result = append(result, "diff --git a/"+newPath+" b/"+newPath)
		case strings.HasPrefix(line, "--- "):
			result = append(result, "--- a/"+newPath)
		default:
			result = append(result, line)
		}
	}
	return result
}

func (t *transformer) transformHunks() []*Hunk {
	newHunks := make([]*Hunk, 0, len(t.patch.hunks))
	startOffset := 0
	for i, hunk := range t.patch.hunks {
		var formatted *Hunk
		startOffset, formatted = t.transformHunk(hunk, startOffset, t.patch.HunkStartIdx(i))
		if formatted.containsChanges() {
			newHunks = append(newHunks, formatted)
		}
	}
	return newHunks
}

func (t *transformer) transformHunk(hunk *Hunk, startOffset, firstLineIdx int) (int, *Hunk) {
	newLines := t.transformHunkLines(hunk, firstLineIdx)
	newNewStart, newStartOffset := transformHunkHeader(newLines, hunk.oldStart, startOffset)
	return newStartOffset, &Hunk{
		bodyLines:     newLines,
		oldStart:      hunk.oldStart,
		newStart:      newNewStart,
		headerContext: hunk.headerContext,
	}
}

// transformHunkLines keeps the selected changes of one hunk and converts the
// unselected ones so the result still applies. Unselected old-file lines
// become context, buffered so they land after the selected additions of the
// same block, and flushed earlier when a selected addition must follow them.
func (t *transformer) transformHunkLines(hunk *Hunk, firstLineIdx int) []*Line {
	skippedNoNewlineIdx := -1
	newLines := []*Line{}
	pendingContext := []*Line{}
	sawUnselectedNewFileLine := false

	flush := func() {
		newLines = append(newLines, pendingContext...)
		pendingContext = pendingContext[:0]
	}

	for i, line := range hunk.bodyLines {
		lineIdx := i + firstLineIdx + 1 // +1 for the header line
		if line.Content == "" {
			break
		}
		_, selected := t.included[lineIdx]

		switch line.Kind {
		case KindContext:
			flush()
			sawUnselectedNewFileLine = false
			newLines = append(newLines, line)
			continue
		case KindNoNewline:
			if skippedNoNewlineIdx != lineIdx {
				flush()
				newLines = append(newLines, line)
			}
			continue
		case KindHeader, KindHunkHeader, KindAddition, KindDeletion:
		}

		isOldFileLine := (line.Kind == KindDeletion && !t.opts.Reverse) || (line.Kind == KindAddition && t.opts.Reverse)

		if selected {
			// selected old-file lines flush first to keep old-file ordering. so
			// does a selected addition that follows skipped new-file lines.
			if isOldFileLine || sawUnselectedNewFileLine {
				flush()
			}
			newLines = append(newLines, line)
			continue
		}

		if isOldFileLine {
			pendingContext = append(pendingContext, &Line{Kind: KindContext, Content: " " + line.Content[1:]})
			continue
		}

		sawUnselectedNewFileLine = true
		if line.Kind == KindAddition {
			// the no-newline marker belongs to an addition we are dropping
			skippedNoNewlineIdx = lineIdx + 1
		}
	}
	flush()
	return newLines
}

// transformHunkHeader recomputes the new-file start of a hunk after its body
// changed, and returns the running offset for the following hunks.
func transformHunkHeader(body []*Line, oldStart, startOffset int) (newStart, nextOffset int) {
	oldLength := countKinds(body, KindContext, KindDeletion)
	newLength := countKinds(body, KindContext, KindAddition)

	// a hunk that goes from zero to positive length starts one line later. one
	// that goes from positive to zero length starts one line earlier.
	adjust := 0
	switch {
	case oldLength == 0:
		adjust = 1
	case newLength == 0:
		adjust = -1
	}
	newStart = oldStart + startOffset + adjust
	nextOffset = startOffset + newLength - oldLength
	return newStart, nextOffset
}
