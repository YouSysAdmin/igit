// Package conflict reads the merge blocks git leaves in a working-tree file and
// rewrites one of them once a side is chosen. It works on the file as it is on
// disk, so the line numbers it reports line up with the new side of a diff and
// the caller needs no index surgery to apply a choice.
package conflict

import (
	"fmt"
	"strings"
)

// marker lengths, git always writes exactly seven characters.
const (
	oursMarker   = "<<<<<<<"
	baseMarker   = "|||||||"
	splitMarker  = "======="
	theirsMarker = ">>>>>>>"
)

// Choice is the side a region is resolved to.
type Choice string

const (
	// Ours keeps the current branch's lines.
	Ours Choice = "ours"
	// Theirs keeps the incoming branch's lines.
	Theirs Choice = "theirs"
	// Both keeps our lines followed by theirs, for the cases where the two
	// changes belong together and only the markers are in the way.
	Both Choice = "both"
)

// Region is one conflict block. Start and End are 1-based line numbers of the
// opening and closing markers, so a diff cursor on the new side maps straight
// onto them. Base is filled only under the diff3 and zdiff3 styles.
type Region struct {
	Start       int
	End         int
	OursLabel   string
	BaseLabel   string
	TheirsLabel string
	Ours        []string
	Base        []string
	Theirs      []string
	HasBase     bool
}

// Lines returns the replacement lines for a choice.
func (r Region) Lines(c Choice) []string {
	switch c {
	case Ours:
		return r.Ours
	case Theirs:
		return r.Theirs
	case Both:
		return append(append([]string{}, r.Ours...), r.Theirs...)
	}
	return nil
}

// marker reports whether line opens with m, either alone or followed by a label.
func marker(line, m string) (label string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimRight(line, "\r"), m)
	if !found {
		return "", false
	}
	if rest != "" && !strings.HasPrefix(rest, " ") {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// Parse lists the conflict blocks of content in file order. A block that never
// closes is not reported, and neither is one whose markers arrive out of order,
// so half-edited files simply yield fewer regions rather than an error.
func Parse(content string) []Region {
	lines := strings.Split(content, "\n")
	var regions []Region
	for i := 0; i < len(lines); i++ {
		oursLabel, ok := marker(lines[i], oursMarker)
		if !ok {
			continue
		}
		region, end, complete := parseRegion(lines, i, oursLabel)
		if !complete {
			continue
		}
		regions = append(regions, region)
		i = end
	}
	return regions
}

// parseRegion reads one block starting at the opening marker on line start.
// end is the index of the closing marker, complete is false for a block that
// does not close before the next one opens or before the file ends.
func parseRegion(lines []string, start int, oursLabel string) (region Region, end int, complete bool) {
	region = Region{Start: start + 1, OursLabel: oursLabel}
	side := &region.Ours
	for i := start + 1; i < len(lines); i++ {
		switch {
		case isMarker(lines[i], oursMarker):
			return Region{}, 0, false // a second block opened, this one is broken
		case isMarker(lines[i], baseMarker):
			label, _ := marker(lines[i], baseMarker)
			region.BaseLabel, region.HasBase = label, true
			side = &region.Base
		case isMarker(lines[i], splitMarker):
			side = &region.Theirs
		case isMarker(lines[i], theirsMarker):
			label, _ := marker(lines[i], theirsMarker)
			region.TheirsLabel = label
			region.End = i + 1
			return region, i, true
		default:
			*side = append(*side, lines[i])
		}
	}
	return Region{}, 0, false
}

func isMarker(line, m string) bool {
	_, ok := marker(line, m)
	return ok
}

// At returns the region holding the given 1-based file line and its index in
// regions. A line on any marker of a block counts as inside it.
func At(regions []Region, line int) (region Region, index int, ok bool) {
	for i, r := range regions {
		if line >= r.Start && line <= r.End {
			return r, i, true
		}
	}
	return Region{}, 0, false
}

// Resolve returns content with region index replaced by the chosen side, the
// markers gone. The rest of the file, its other conflicts included, is left
// byte for byte as it was.
func Resolve(content string, index int, choice Choice) (string, error) {
	regions := Parse(content)
	if index < 0 || index >= len(regions) {
		return "", fmt.Errorf("conflict %d is not in the file, it has %d", index+1, len(regions))
	}
	r := regions[index]
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	out = append(out, lines[:r.Start-1]...)
	out = append(out, r.Lines(choice)...)
	out = append(out, lines[r.End:]...)
	return strings.Join(out, "\n"), nil
}
