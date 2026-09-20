package gitops

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseGitVersion(t *testing.T) {
	v, err := parseGitVersion("git version 2.39.3 (Apple Git-146)\n")
	require.NoError(t, err)
	assert.Equal(t, gitVersion{2, 39, 3}, v)
	v, err = parseGitVersion("git version 2.55.0")
	require.NoError(t, err)
	assert.Equal(t, gitVersion{2, 55, 0}, v)
	v, err = parseGitVersion("git version 3.0")
	require.NoError(t, err)
	assert.Equal(t, gitVersion{3, 0, 0}, v)
	_, err = parseGitVersion("nope")
	require.Error(t, err)
}

func TestGitVersion_atLeast(t *testing.T) {
	v := gitVersion{2, 35, 1}
	assert.True(t, v.atLeast(2, 35))
	assert.True(t, v.atLeast(2, 11))
	assert.True(t, v.atLeast(1, 99))
	assert.False(t, v.atLeast(2, 36))
	assert.False(t, v.atLeast(3, 0))
}
