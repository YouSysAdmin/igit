// Package patch parses unified diffs, selects a subset of their changed lines
// and re-emits a patch that `git apply` accepts. It powers hunk- and line-level
// staging, unstaging and discarding.
//
// Portions derived from lazygit (https://github.com/jesseduffield/lazygit),
// Copyright (c) 2018 Jesse Duffield, MIT License. See NOTICE.
package patch

import "slices"

// LineKind classifies one line of a unified diff.
type LineKind int

// Line kinds in the order they appear in a patch.
const (
	KindHeader     LineKind = iota // "diff --git", "index", "---", "+++" and other pre-hunk lines
	KindHunkHeader                 // "@@ -a,b +c,d @@ context"
	KindAddition                   // "+..."
	KindDeletion                   // "-..."
	KindContext                    // " ..."
	KindNoNewline                  // "\ No newline at end of file"
)

// Line is one patch line. Content keeps the leading marker character so the
// bytes can be written back verbatim.
type Line struct {
	Kind    LineKind
	Content string
}

// IsChange reports whether the line is an addition or a deletion.
func (l *Line) IsChange() bool { return l.Kind == KindAddition || l.Kind == KindDeletion }

// IsAddition reports whether the line is an addition.
func (l *Line) IsAddition() bool { return l.Kind == KindAddition }

// IsDeletion reports whether the line is a deletion.
func (l *Line) IsDeletion() bool { return l.Kind == KindDeletion }

// countKinds returns how many lines have one of the given kinds.
func countKinds(lines []*Line, kinds ...LineKind) int {
	n := 0
	for _, line := range lines {
		if slices.Contains(kinds, line.Kind) {
			n++
		}
	}
	return n
}
