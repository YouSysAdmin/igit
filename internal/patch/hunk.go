package patch

import "fmt"

// Hunk is one "@@" section of a patch.
//
// Example:
//
//	@@ -16,2 +14,3 @@ func (f *CommitFile) Description() string {
//	 	return f.Name
//	-}
//	+
//	+// test
type Hunk struct {
	oldStart      int     // first line number in the old file (16 above)
	newStart      int     // first line number in the new file (14 above)
	headerContext string  // text after the closing "@@" (" func (f *CommitFile) ..." above)
	bodyLines     []*Line // body without the header line
}

// oldLength is the hunk's line count in the old file (2 above).
func (h *Hunk) oldLength() int { return countKinds(h.bodyLines, KindContext, KindDeletion) }

// newLength is the hunk's line count in the new file (3 above).
func (h *Hunk) newLength() int { return countKinds(h.bodyLines, KindContext, KindAddition) }

// containsChanges reports whether the hunk has any addition or deletion. A
// transform that selects none of a hunk's lines leaves a context-only hunk,
// which is dropped from the output.
func (h *Hunk) containsChanges() bool { return countKinds(h.bodyLines, KindAddition, KindDeletion) > 0 }

// lineCount is the number of lines including the header line.
func (h *Hunk) lineCount() int { return len(h.bodyLines) + 1 }

// allLines returns the header line followed by the body.
func (h *Hunk) allLines() []*Line {
	lines := make([]*Line, 1, 1+len(h.bodyLines))
	lines[0] = &Line{Content: h.formatHeaderLine(), Kind: KindHunkHeader}
	return append(lines, h.bodyLines...)
}

// formatHeaderLine returns the full header line including the trailing context.
func (h *Hunk) formatHeaderLine() string { return h.formatHeaderStart() + h.headerContext }

// formatHeaderStart returns the "@@ -a,b +c,d @@" part. The new length is
// omitted when it is 1, matching what git prints.
func (h *Hunk) formatHeaderStart() string {
	newLengthDisplay := ""
	if newLength := h.newLength(); newLength != 1 {
		newLengthDisplay = fmt.Sprintf(",%d", newLength)
	}
	return fmt.Sprintf("@@ -%d,%d +%d%s @@", h.oldStart, h.oldLength(), h.newStart, newLengthDisplay)
}
