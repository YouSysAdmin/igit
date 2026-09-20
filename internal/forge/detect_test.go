package forge

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect(t *testing.T) {
	tests := map[string]Kind{
		"git@github.com:acme/widgets.git":            KindGitHub,
		"https://github.com/acme/widgets.git":        KindGitHub,
		"ssh://git@github.corp.example/acme/w.git":   KindGitHub,
		"git@gitlab.com:group/sub/proj.git":          KindGitLab,
		"https://gitlab.coffebro.com/infra/ansible":  KindGitLab,
		"ssh://git@code.example.org:2222/team/x.git": KindGitLab,
		"/srv/git/local.git":                         KindGitLab,
	}
	for remote, want := range tests {
		assert.Equal(t, want, Detect(remote), remote)
	}
}

func TestRemoteHost(t *testing.T) {
	assert.Equal(t, "github.com", remoteHost("git@GitHub.com:a/b.git"))
	assert.Equal(t, "gitlab.com", remoteHost("https://gitlab.com/a/b"))
	assert.Equal(t, "code.example.org", remoteHost("ssh://git@code.example.org:2222/a/b"))
	assert.Equal(t, "host", remoteHost("host:path"))
}

func TestOpenWith(t *testing.T) {
	f := &fakeRunner{answers: map[string]string{"git remote get-url origin": "git@gitlab.com:g/p.git\n"}}
	c, err := openWith(context.Background(), "/repo", "", f.run)
	require.NoError(t, err)
	assert.Equal(t, KindGitLab, c.Kind(), "the remote decides")

	fo := &fakeRunner{}
	c, err = openWith(context.Background(), "/repo", KindGitHub, fo.run)
	require.NoError(t, err)
	assert.Equal(t, KindGitHub, c.Kind(), "the override wins")
	assert.Empty(t, fo.calls, "no remote lookup with an override")

	_, err = openWith(context.Background(), "/repo", Kind("bitbucket"), f.run)
	require.ErrorContains(t, err, "unknown forge")

	f2 := &fakeRunner{fail: map[string]string{"git remote get-url origin": "error: No such remote 'origin'"}}
	_, err = openWith(context.Background(), "/repo", "", f2.run)
	require.ErrorContains(t, err, "origin remote")
}
