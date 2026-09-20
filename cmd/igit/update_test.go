package main

import (
	"bytes"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yousysadmin/igit/internal/update"
)

func TestDetectUpdateSubcommand(t *testing.T) {
	tests := []struct {
		name string
		base string
		vs   string
		args []string
		want bool
	}{
		{"bare word", "update", "", []string{"update"}, true},
		{"with --check", "update", "", []string{"update", "--check"}, true},
		{"forced positional", "update", "", []string{"--", "update"}, false},
		{"two refs", "update", "main", []string{"update", "main"}, false},
		{"other ref", "main", "", []string{"main"}, false},
		{"no args", "", "", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts options
			opts.Refs.Base, opts.Refs.Against = tt.base, tt.vs

			got := detectUpdateSubcommand(opts, tt.args)

			assert.Equal(t, tt.want, got.updateSubcommand)
			if tt.want {
				assert.Empty(t, got.Refs.Base, "the subcommand word is not a ref")
			}
		})
	}
}

func TestParseArgs_update(t *testing.T) {
	t.Run("subcommand", func(t *testing.T) {
		opts, err := parseArgs([]string{"update"})
		require.NoError(t, err)
		assert.True(t, opts.updateSubcommand)
		assert.False(t, opts.Check)
	})

	t.Run("subcommand with --check", func(t *testing.T) {
		opts, err := parseArgs([]string{"update", "--check"})
		require.NoError(t, err)
		assert.True(t, opts.updateSubcommand)
		assert.True(t, opts.Check)
	})

	t.Run("--check without the subcommand is rejected", func(t *testing.T) {
		_, err := parseArgs([]string{"--check"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--check is only valid with igit update")
	})
}

func TestCheckMessage(t *testing.T) {
	tests := []struct {
		name string
		res  update.Result
		want string
	}{
		{
			"newer available",
			update.Result{Current: "0.1.0", Latest: "0.2.0", Newer: true},
			"igit v0.1.0 is out of date, v0.2.0 is available, run `igit update` to install it",
		},
		{
			"up to date",
			update.Result{Current: "0.2.0", Latest: "0.2.0"},
			"igit v0.2.0 is up to date",
		},
		{
			"development build",
			update.Result{Latest: "0.2.0"},
			"development build, latest release is v0.2.0, run `igit update` to install it",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, checkMessage(tt.res))
		})
	}
}

func TestRunUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		assert.NoError(t, json.MarshalWrite(w, map[string]any{"tag_name": "v9.9.9"}))
	}))
	t.Cleanup(srv.Close)
	upd := update.Updater{Client: srv.Client(), APIURL: srv.URL}

	t.Run("check reports a newer release", func(t *testing.T) {
		var out bytes.Buffer
		err := runUpdate(options{Check: true}, "v0.1.0-abc1234-20260920T112233", &out, upd)
		require.NoError(t, err)
		assert.Contains(t, out.String(), "igit v0.1.0 is out of date, v9.9.9 is available")
	})

	t.Run("check on a development build", func(t *testing.T) {
		var out bytes.Buffer
		err := runUpdate(options{Check: true}, "master-abc1234-20260920T112233", &out, upd)
		require.NoError(t, err)
		assert.Contains(t, out.String(), "development build, latest release is v9.9.9")
	})

	t.Run("check surfaces the API error", func(t *testing.T) {
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(bad.Close)

		var out bytes.Buffer
		err := runUpdate(options{Check: true}, "v0.1.0-abc-1", &out, update.Updater{Client: bad.Client(), APIURL: bad.URL})
		require.Error(t, err)
		assert.Empty(t, out.String())
	})

	t.Run("without --check it installs", func(t *testing.T) {
		exe := filepath.Join(t.TempDir(), "igit")
		require.NoError(t, os.WriteFile(exe, []byte("stale"), 0o755)) //nolint:gosec // an executable under test needs the exec bit
		installer := upd
		installer.ExecPath = func() (string, error) { return exe, nil }

		// the release carries no assets, so the attempt fails after the version
		// comparison, which is as far as this test needs to go
		err := runUpdate(options{}, "v0.1.0-abc-1", &bytes.Buffer{}, installer)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no asset for")
	})

	t.Run("without --check it is a no-op when current", func(t *testing.T) {
		var out bytes.Buffer
		err := runUpdate(options{}, "v9.9.9-abc-1", &out, upd)
		require.NoError(t, err)
		assert.Contains(t, out.String(), "already up to date (v9.9.9)")
	})
}
