package patch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$`)

// Parse splits a unified diff into its header lines and hunks. Lines are split
// on "\n" only, so carriage returns and trailing whitespace survive verbatim.
// A malformed hunk header is an error. anything before the first hunk is header.
func Parse(patchStr string) (*Patch, error) {
	lines := strings.Split(strings.TrimSuffix(patchStr, "\n"), "\n")

	hunks := []*Hunk{}
	header := []string{}
	var current *Hunk
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "@@"):
			oldStart, newStart, ctx, err := headerInfo(line)
			if err != nil {
				return nil, err
			}
			current = &Hunk{oldStart: oldStart, newStart: newStart, headerContext: ctx, bodyLines: []*Line{}}
			hunks = append(hunks, current)
		case current != nil:
			current.bodyLines = append(current.bodyLines, newBodyLine(line))
		default:
			header = append(header, line)
		}
	}
	return &Patch{hunks: hunks, header: header}, nil
}

// MustParse is Parse for inputs known to be well-formed. it panics otherwise.
func MustParse(patchStr string) *Patch {
	p, err := Parse(patchStr)
	if err != nil {
		panic(err)
	}
	return p
}

func headerInfo(header string) (oldStart, newStart int, ctx string, err error) {
	m := hunkHeaderRe.FindStringSubmatch(header)
	if m == nil {
		return 0, 0, "", fmt.Errorf("patch: malformed hunk header %q", header)
	}
	oldStart, err = strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, "", fmt.Errorf("patch: hunk header %q: %w", header, err)
	}
	newStart, err = strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, "", fmt.Errorf("patch: hunk header %q: %w", header, err)
	}
	return oldStart, newStart, m[3], nil
}

func newBodyLine(line string) *Line {
	if line == "" {
		return &Line{Kind: KindContext, Content: ""}
	}
	return &Line{Kind: kindOf(line[0]), Content: line}
}

func kindOf(first byte) LineKind {
	switch first {
	case ' ':
		return KindContext
	case '+':
		return KindAddition
	case '-':
		return KindDeletion
	case '\\':
		return KindNoNewline
	}
	return KindContext
}
