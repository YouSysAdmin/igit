package tui

import (
	"cmp"
	"slices"
	"strconv"

	"github.com/yousysadmin/igit/internal/git"
)

// remoteNoteMarker leads a comment that came from the request itself. The
// session's own annotations keep the configured marker, so two comments on one
// line stay told apart.
const remoteNoteMarker = "\U0001f464"

// RemoteNote is a review comment that already exists on the request. It is
// drawn under its line like an annotation but lives outside the store, so it
// is never written to the output or posted again.
type RemoteNote struct {
	File     string
	Line     int    // line on Side (GitHub numbering: new file for RIGHT, old file for LEFT), 0 when it belongs to the file
	Side     string // "LEFT" for removed lines, "RIGHT" (or empty) otherwise
	Author   string
	Body     string
	Outdated bool   // the request moved past the revision it was written on
	OrigPath string // path it was written against, empty when it is File
	OrigLine int    // line it was written against, 0 when unknown

	atFile bool // drawn in the file block because its line is not in the diff
}

// text renders the note the way it appears under its row. A note drawn away
// from the line it points at says where that line was, so nothing reads as a
// comment on the wrong code.
func (n RemoteNote) text() string {
	body := n.Body
	if n.Author != "" {
		body = "@" + n.Author + ": " + body
	}
	if tag := n.tag(); tag != "" {
		body = tag + " " + body
	}
	return body
}

// tag names the note's origin when it is not where it is drawn.
func (n RemoteNote) tag() string {
	switch {
	case n.Outdated:
		return "(outdated " + n.where() + ")"
	case n.atFile:
		return "(" + n.where() + ")"
	}
	return ""
}

// where is the position the note was written against.
func (n RemoteNote) where() string {
	path, line := cmp.Or(n.OrigPath, n.File), cmp.Or(n.OrigLine, n.Line)
	if line == 0 {
		return path
	}
	return path + ":" + strconv.Itoa(line)
}

// groupRemoteNotes indexes notes by file.
func groupRemoteNotes(notes []RemoteNote) map[string][]RemoteNote {
	if len(notes) == 0 {
		return nil
	}
	byFile := make(map[string][]RemoteNote)
	for _, n := range notes {
		byFile[n.File] = append(byFile[n.File], n)
	}
	return byFile
}

// remoteNotePrefix leads a remote comment row.
func (m Model) remoteNotePrefix() string { return remoteNoteMarker + " " }

// indexRemoteNotes anchors the current file's remote comments to annotation
// keys, the same keys the store's annotations use. A note is placed through the
// loaded diff lines: LEFT notes by old line number, RIGHT notes by new line
// number. A note with no line, and one whose line the loaded diff does not
// show, goes to the file block so it stays readable. A renamed file is looked
// up under its old name too, where the request still records the comments.
// handleFileLoaded rebuilds this, the notes of a request never change during a
// session.
func (m Model) indexRemoteNotes() map[string][]RemoteNote {
	notes := m.remoteNotes[m.file.name]
	if old := m.file.oldName; old != "" && old != m.file.name {
		notes = append(slices.Clone(notes), m.remoteNotes[old]...)
	}
	if len(notes) == 0 {
		return nil
	}
	byOld := make(map[int]string)
	byNew := make(map[int]string)
	for _, dl := range m.file.lines {
		switch dl.ChangeType { //nolint:exhaustive // dividers and placeholders carry no line numbers
		case git.ChangeRemove:
			byOld[dl.OldNum] = m.annotationKey(dl.OldNum, string(dl.ChangeType))
		case git.ChangeAdd:
			byNew[dl.NewNum] = m.annotationKey(dl.NewNum, string(dl.ChangeType))
		case git.ChangeContext:
			key := m.annotationKey(dl.NewNum, string(dl.ChangeType))
			byNew[dl.NewNum] = key
			if _, taken := byOld[dl.OldNum]; !taken {
				byOld[dl.OldNum] = key
			}
		}
	}
	index := make(map[string][]RemoteNote, len(notes))
	for _, n := range notes {
		side := byNew
		if n.Side == "LEFT" {
			side = byOld
		}
		if key, ok := side[n.Line]; ok && n.Line > 0 {
			index[key] = append(index[key], n)
			continue
		}
		n.atFile = true
		index[annotKeyFile] = append(index[annotKeyFile], n)
	}
	slices.SortStableFunc(index[annotKeyFile], func(a, b RemoteNote) int {
		return cmp.Compare(cmp.Or(a.OrigLine, a.Line), cmp.Or(b.OrigLine, b.Line))
	})
	return index
}
