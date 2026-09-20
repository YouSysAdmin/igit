package gitops

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yousysadmin/igit/internal/git"
)

// emptyTree is git's well-known empty tree object, the parent of a root commit
// for diff purposes.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Commit is one entry of the log.
type Commit struct {
	Hash      string
	ShortHash string
	Author    string
	Email     string
	Subject   string
	Body      string // message after the subject, trimmed, "" when absent
	Date      time.Time
	Parents   []string
	Refs      []string // decorations: branch and tag names pointing here
}

// Message returns the full message: subject plus body when there is one.
func (c Commit) Message() string {
	if c.Body == "" {
		return c.Subject
	}
	return c.Subject + "\n\n" + c.Body
}

// DiffRef returns the "parent..hash" ref for showing the commit's changes in a
// FileDiffRequest. a root commit diffs against the empty tree.
func (c Commit) DiffRef() string {
	parent := emptyTree
	if len(c.Parents) > 0 {
		parent = c.Parents[0]
	}
	return parent + ".." + c.Hash
}

// Log returns up to limit commits reachable from ref ("" = HEAD), newest first.
func (g *Git) Log(ctx context.Context, ref string, limit int) ([]Commit, error) {
	if limit <= 0 {
		limit = 300
	}
	args := []string{"log", "-n", strconv.Itoa(limit), "--no-show-signature", "-z",
		"--pretty=format:%H%x1f%h%x1f%aN%x1f%aE%x1f%ct%x1f%P%x1f%D%x1f%s%x1f%b"}
	if ref != "" {
		args = append(args, ref)
	}
	out, err := g.Run(ctx, git.RunOpts{Background: true}, args...)
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	return parseLog(out)
}

func parseLog(out string) ([]Commit, error) {
	var commits []Commit
	for rec := range strings.SplitSeq(out, "\x00") {
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 9)
		if len(f) < 9 {
			return nil, fmt.Errorf("log: malformed record %q", rec)
		}
		c := Commit{Hash: f[0], ShortHash: f[1], Author: f[2], Email: f[3], Subject: f[7], Body: strings.TrimSpace(f[8])}
		if ts, err := strconv.ParseInt(f[4], 10, 64); err == nil {
			c.Date = time.Unix(ts, 0)
		}
		if f[5] != "" {
			c.Parents = strings.Fields(f[5])
		}
		if f[6] != "" {
			for ref := range strings.SplitSeq(f[6], ",") {
				ref = strings.TrimSpace(ref)
				ref = strings.TrimPrefix(ref, "HEAD -> ")
				ref = strings.TrimPrefix(ref, "tag: ")
				if ref != "" && ref != "HEAD" {
					c.Refs = append(c.Refs, ref)
				}
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// CommitFiles lists the files a commit changed against its first parent.
func (g *Git) CommitFiles(ctx context.Context, hash string) ([]git.FileEntry, error) {
	out, err := g.Run(ctx, git.RunOpts{Background: true}, "diff-tree", "--no-commit-id", "-r", "--root", "--name-status", "-M", "-z", hash)
	if err != nil {
		return nil, err
	}
	return git.ParseNameStatus(out), nil
}
