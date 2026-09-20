package gitops

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/yousysadmin/igit/internal/git"
)

// gitVersion is a parsed `git version` triple.
type gitVersion struct {
	major, minor, patch int
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// parseGitVersion extracts the numeric version from `git version` output such
// as "git version 2.39.3 (Apple Git-146)".
func parseGitVersion(out string) (gitVersion, error) {
	m := versionRe.FindStringSubmatch(out)
	if m == nil {
		return gitVersion{}, fmt.Errorf("unrecognized git version output %q", out)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3]) // empty group parses as 0
	return gitVersion{major: major, minor: minor, patch: patch}, nil
}

// atLeast reports whether v >= major.minor.
func (v gitVersion) atLeast(major, minor int) bool {
	return v.major > major || (v.major == major && v.minor >= minor)
}

// Supports reports whether the installed git is at least major.minor. The
// version is read once and cached. a failed probe reports false.
func (g *Git) Supports(ctx context.Context, major, minor int) bool {
	g.versionOnce.Do(func() {
		out, err := g.Run(ctx, git.RunOpts{}, "version")
		if err != nil {
			g.versionErr = err
			return
		}
		g.version, g.versionErr = parseGitVersion(out)
	})
	if g.versionErr != nil {
		return false
	}
	return g.version.atLeast(major, minor)
}
