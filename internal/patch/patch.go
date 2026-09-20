package patch

import "slices"

// Patch is a parsed unified diff of one file.
type Patch struct {
	// header lines, e.g.
	//   diff --git a/filename b/filename
	//   index dcd3485..1ba5540 100644
	//   --- a/filename
	//   +++ b/filename
	header []string
	hunks  []*Hunk
}

// Transform returns a new patch with the selection and header rewrites in
// opts applied. The receiver is left unchanged.
func (p *Patch) Transform(opts TransformOpts) *Patch { return transform(p, opts) }

// FormatPlain returns the patch as text ready for `git apply`. A patch with no
// changes formats as the empty string.
func (p *Patch) FormatPlain() string { return formatPlain(p) }

// FormatRangePlain returns the lines startIdx..endIdx (inclusive) as text.
func (p *Patch) FormatRangePlain(startIdx, endIdx int) string {
	return formatRangePlain(p, startIdx, endIdx)
}

// Lines returns every line of the patch: header lines, then each hunk's header
// and body. Indices into this slice are what TransformOpts.IncludedLineIndices
// and the Hunk* helpers refer to.
func (p *Patch) Lines() []*Line {
	lines := make([]*Line, 0, p.LineCount())
	for _, line := range p.header {
		lines = append(lines, &Line{Content: line, Kind: KindHeader})
	}
	for _, hunk := range p.hunks {
		lines = append(lines, hunk.allLines()...)
	}
	return lines
}

// HunkStartIdx returns the line index of the given hunk's header line.
func (p *Patch) HunkStartIdx(hunkIndex int) int {
	hunkIndex = clamp(hunkIndex, 0, len(p.hunks)-1)
	result := len(p.header)
	for i := range hunkIndex {
		result += p.hunks[i].lineCount()
	}
	return result
}

// HunkEndIdx returns the line index of the given hunk's last body line.
func (p *Patch) HunkEndIdx(hunkIndex int) int {
	hunkIndex = clamp(hunkIndex, 0, len(p.hunks)-1)
	return p.HunkStartIdx(hunkIndex) + p.hunks[hunkIndex].lineCount() - 1
}

// HunkContainingLine returns the index of the hunk that holds line idx, or -1.
func (p *Patch) HunkContainingLine(idx int) int {
	for hunkIdx, hunk := range p.hunks {
		start := p.HunkStartIdx(hunkIdx)
		if idx >= start && idx < start+hunk.lineCount() {
			return hunkIdx
		}
	}
	return -1
}

// HunkOldStartForLine returns the old-file start line of the hunk containing
// idx, or 0 when idx is outside every hunk.
func (p *Patch) HunkOldStartForLine(idx int) int {
	hunkIdx := p.HunkContainingLine(idx)
	if hunkIdx == -1 {
		return 0
	}
	return p.hunks[hunkIdx].oldStart
}

// ContainsChanges reports whether any hunk has an addition or deletion.
func (p *Patch) ContainsChanges() bool {
	for _, hunk := range p.hunks {
		if hunk.containsChanges() {
			return true
		}
	}
	return false
}

// LineNumberOfLine maps a patch line index to a line number in the new file.
// Header lines map to 1, a hunk header to the hunk's first new-file line, and
// an index past the end to the last line of the last hunk.
func (p *Patch) LineNumberOfLine(idx int) int {
	if idx < len(p.header) || len(p.hunks) == 0 {
		return 1
	}
	hunkIdx := p.HunkContainingLine(idx)
	if hunkIdx == -1 {
		last := p.hunks[len(p.hunks)-1]
		return last.newStart + last.newLength() - 1
	}
	hunk := p.hunks[hunkIdx]
	idxInHunk := idx - p.HunkStartIdx(hunkIdx)
	if idxInHunk == 0 {
		return hunk.newStart
	}
	return hunk.newStart + countKinds(hunk.bodyLines[:idxInHunk-1], KindAddition, KindContext)
}

// NextChangeIdx returns the index of the first addition or deletion at or after
// idx, falling back to the last change in the patch when none follows. It
// returns 0 when the patch has no changes at all.
func (p *Patch) NextChangeIdx(idx int) int {
	lines := p.Lines()
	if len(lines) == 0 {
		return 0
	}
	idx = clamp(idx, 0, len(lines)-1)
	for i := idx; i < len(lines); i++ {
		if lines[i].IsChange() {
			return i
		}
	}
	for i, line := range slices.Backward(lines) {
		if line.IsChange() {
			return i
		}
	}
	return 0
}

// LineCount returns the number of lines Lines would return.
func (p *Patch) LineCount() int {
	count := len(p.header)
	for _, hunk := range p.hunks {
		count += hunk.lineCount()
	}
	return count
}

// HunkCount returns the number of hunks.
func (p *Patch) HunkCount() int { return len(p.hunks) }

// IsSingleHunkForWholeFile reports whether the patch is one hunk made only of
// additions or only of deletions with no context, i.e. a whole new or deleted
// file (or a whole-file rewrite at context size 0).
func (p *Patch) IsSingleHunkForWholeFile() bool {
	if len(p.hunks) != 1 {
		return false
	}
	body := p.hunks[0].bodyLines
	return countKinds(body, KindDeletion, KindContext) == 0 || countKinds(body, KindAddition, KindContext) == 0
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}
