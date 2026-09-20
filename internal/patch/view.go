package patch

// ViewLine is one displayable line of a patch: its index into Lines, its kind,
// the old/new file line numbers (0 when the side does not have the line) and
// the content with the leading marker stripped.
type ViewLine struct {
	PatchIdx int
	Kind     LineKind
	OldNum   int
	NewNum   int
	Content  string
	HunkIdx  int // -1 for header lines
}

// ViewLines walks the hunks and numbers every body line the way a diff viewer
// does, so a display row can be mapped back to a patch line index for
// Transform. Header lines are included with HunkIdx -1 and no numbers.
func (p *Patch) ViewLines() []ViewLine {
	out := make([]ViewLine, 0, p.LineCount())
	idx := 0
	for _, h := range p.header {
		out = append(out, ViewLine{PatchIdx: idx, Kind: KindHeader, Content: h, HunkIdx: -1})
		idx++
	}
	for hi, hunk := range p.hunks {
		out = append(out, ViewLine{PatchIdx: idx, Kind: KindHunkHeader, Content: hunk.formatHeaderLine(), HunkIdx: hi})
		idx++
		oldNum, newNum := hunk.oldStart, hunk.newStart
		for _, line := range hunk.bodyLines {
			vl := ViewLine{PatchIdx: idx, Kind: line.Kind, Content: stripMarker(line.Content), HunkIdx: hi}
			switch line.Kind {
			case KindAddition:
				vl.NewNum = newNum
				newNum++
			case KindDeletion:
				vl.OldNum = oldNum
				oldNum++
			case KindContext:
				vl.OldNum, vl.NewNum = oldNum, newNum
				oldNum++
				newNum++
			case KindNoNewline, KindHeader, KindHunkHeader:
			}
			out = append(out, vl)
			idx++
		}
	}
	return out
}

func stripMarker(content string) string {
	if content == "" {
		return ""
	}
	return content[1:]
}
