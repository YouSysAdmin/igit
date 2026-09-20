package forge

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Open picks the client for the clone at dir: the override when set, otherwise
// the service the origin remote points at. Whether the CLI is installed is the
// caller's question, see Client.Available.
func Open(ctx context.Context, dir string, override Kind) (Client, error) {
	return openWith(ctx, dir, override, execRunner)
}

func openWith(ctx context.Context, dir string, override Kind, run Runner) (Client, error) {
	kind := override
	if kind == "" {
		out, err := run(ctx, dir, "git", "", "remote", "get-url", "origin")
		if err != nil {
			return nil, fmt.Errorf("origin remote: %w", err)
		}
		kind = Detect(strings.TrimSpace(out))
	}
	switch kind {
	case KindGitHub:
		return NewWithRunner(dir, run), nil
	case KindGitLab:
		return NewGitLabWithRunner(dir, run), nil
	}
	return nil, fmt.Errorf("unknown forge %q", kind)
}

// Detect names the service from a remote URL: a host that mentions github is
// GitHub (github.com or GitHub Enterprise), anything else is GitLab, the usual
// self-hosted case.
func Detect(remoteURL string) Kind {
	if strings.Contains(remoteHost(remoteURL), "github") {
		return KindGitHub
	}
	return KindGitLab
}

// remoteHost extracts the host of an ssh://, https:// or scp-like
// user@host:path remote URL.
func remoteHost(remote string) string {
	if strings.Contains(remote, "://") {
		if u, err := url.Parse(remote); err == nil {
			return strings.ToLower(u.Hostname())
		}
	}
	hostPart, _, _ := strings.Cut(remote, ":")
	if _, host, ok := strings.Cut(hostPart, "@"); ok {
		hostPart = host
	}
	return strings.ToLower(hostPart)
}
