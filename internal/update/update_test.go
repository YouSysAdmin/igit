package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersion(t *testing.T) {
	tests := []struct {
		name string
		rev  string
		want string
		ok   bool
	}{
		{"release build", "v0.1.0-abc1234-20260920T112233", "0.1.0", true},
		{"tag without v", "0.2.3-abc1234-20260920T112233", "0.2.3", true},
		{"bare tag", "v1.2.3", "1.2.3", true},
		{"branch build", "master-abc1234-20260920T112233", "", false},
		{"unknown", "unknown", "", false},
		{"empty", "", "", false},
		{"two components", "v1.2-abc-def", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Version(tt.rev)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCompare(t *testing.T) {
	assert.Negative(t, Compare("0.1.0", "0.2.0"))
	assert.Negative(t, Compare("0.9.9", "1.0.0"))
	assert.Negative(t, Compare("1.0.0", "1.0.1"))
	assert.Zero(t, Compare("1.2.3", "v1.2.3"))
	assert.Positive(t, Compare("1.2.3", "1.2.2"))
	assert.Positive(t, Compare("2.0.0", "1.99.99"))
	assert.Zero(t, Compare("1.0.0-rc1", "1.0.0"), "pre-release suffixes are not ordered")
}

func TestAssetName(t *testing.T) {
	assert.Equal(t, "igit_0.1.0_darwin_arm64.tar.gz", AssetName("0.1.0", "darwin", "arm64"))
	assert.Equal(t, "igit_1.2.3_linux_amd64.tar.gz", AssetName("1.2.3", "linux", "amd64"))
	assert.Equal(t, "igit_0.1.0_checksums.txt", ChecksumName("0.1.0"))
}

func TestChecksumFor(t *testing.T) {
	list := "aaa  igit_0.1.0_linux_amd64.tar.gz\nbbb *igit_0.1.0_darwin_arm64.tar.gz\n\nccc\n"
	got, err := checksumFor(strings.NewReader(list), "igit_0.1.0_darwin_arm64.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, "bbb", got)

	_, err = checksumFor(strings.NewReader(list), "igit_0.1.0_linux_arm64.tar.gz")
	assert.Error(t, err)
}

func TestExtractBinary(t *testing.T) {
	t.Run("finds the binary among other files", func(t *testing.T) {
		archive := writeArchive(t, map[string]string{
			"LICENSE":              "mit",
			"completions/igit.zsh": "compdef",
			"igit":                 "binary-bytes",
		})
		got, err := extractBinary(archive)
		require.NoError(t, err)
		assert.Equal(t, "binary-bytes", string(got))
	})

	t.Run("missing binary", func(t *testing.T) {
		archive := writeArchive(t, map[string]string{"LICENSE": "mit"})
		_, err := extractBinary(archive)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not contain igit")
	})

	t.Run("not an archive", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "junk.tar.gz")
		require.NoError(t, os.WriteFile(path, []byte("not gzip"), 0o600))
		_, err := extractBinary(path)
		assert.Error(t, err)
	})
}

func TestReplace(t *testing.T) {
	t.Run("swaps content and keeps the mode", func(t *testing.T) {
		dir := t.TempDir()
		exe := filepath.Join(dir, "igit")
		require.NoError(t, os.WriteFile(exe, []byte("old"), 0o755)) //nolint:gosec // an executable under test needs the exec bit

		require.NoError(t, replace(exe, []byte("new")))

		data, err := os.ReadFile(exe) //nolint:gosec // fixed temp-dir path
		require.NoError(t, err)
		assert.Equal(t, "new", string(data))

		info, err := os.Stat(exe)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Len(t, entries, 1, "no leftover temp file")
	})

	t.Run("missing target", func(t *testing.T) {
		err := replace(filepath.Join(t.TempDir(), "absent"), []byte("new"))
		assert.Error(t, err)
	})
}

func TestUpdaterCheck(t *testing.T) {
	t.Run("newer release", func(t *testing.T) {
		srv := releaseServer(t, "v0.2.0", nil)
		u := Updater{Client: srv.Client(), APIURL: srv.URL + "/release"}

		res, err := u.Check(t.Context(), "0.1.0")
		require.NoError(t, err)
		assert.Equal(t, Result{Current: "0.1.0", Latest: "0.2.0", Newer: true}, res)
	})

	t.Run("already current", func(t *testing.T) {
		srv := releaseServer(t, "v0.2.0", nil)
		u := Updater{Client: srv.Client(), APIURL: srv.URL + "/release"}

		res, err := u.Check(t.Context(), "0.2.0")
		require.NoError(t, err)
		assert.False(t, res.Newer)
	})

	t.Run("development build never looks newer", func(t *testing.T) {
		srv := releaseServer(t, "v9.9.9", nil)
		u := Updater{Client: srv.Client(), APIURL: srv.URL + "/release"}

		res, err := u.Check(t.Context(), "")
		require.NoError(t, err)
		assert.False(t, res.Newer)
		assert.Equal(t, "9.9.9", res.Latest)
	})

	t.Run("unparseable tag", func(t *testing.T) {
		srv := releaseServer(t, "nightly", nil)
		u := Updater{Client: srv.Client(), APIURL: srv.URL + "/release"}

		_, err := u.Check(t.Context(), "0.1.0")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot parse latest release tag")
	})

	t.Run("api error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		t.Cleanup(srv.Close)
		u := Updater{Client: srv.Client(), APIURL: srv.URL}

		_, err := u.Check(t.Context(), "0.1.0")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403")
	})

	t.Run("malformed json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "{not json")
		}))
		t.Cleanup(srv.Close)
		u := Updater{Client: srv.Client(), APIURL: srv.URL}

		_, err := u.Check(t.Context(), "0.1.0")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse release info")
	})
}

func TestUpdaterApply(t *testing.T) {
	t.Run("downloads, verifies and replaces", func(t *testing.T) {
		srv := releaseServer(t, "v0.2.0", map[string]string{"igit": "fresh-binary"})
		exe := filepath.Join(t.TempDir(), "igit")
		require.NoError(t, os.WriteFile(exe, []byte("stale"), 0o755)) //nolint:gosec // an executable under test needs the exec bit

		u := Updater{
			Client: srv.Client(), APIURL: srv.URL + "/release",
			ExecPath: func() (string, error) { return exe, nil },
			GOOS:     "linux", GOARCH: "amd64",
		}

		var out bytes.Buffer
		require.NoError(t, u.Apply(t.Context(), "0.1.0", &out))

		data, err := os.ReadFile(exe) //nolint:gosec // fixed temp-dir path
		require.NoError(t, err)
		assert.Equal(t, "fresh-binary", string(data))
		assert.Contains(t, out.String(), "updated v0.1.0 -> v0.2.0")
	})

	t.Run("no-op when current", func(t *testing.T) {
		srv := releaseServer(t, "v0.2.0", map[string]string{"igit": "fresh-binary"})
		u := Updater{Client: srv.Client(), APIURL: srv.URL + "/release", GOOS: "linux", GOARCH: "amd64"}

		var out bytes.Buffer
		require.NoError(t, u.Apply(t.Context(), "0.2.0", &out))
		assert.Contains(t, out.String(), "already up to date")
	})

	t.Run("no asset for the platform", func(t *testing.T) {
		srv := releaseServer(t, "v0.2.0", map[string]string{"igit": "fresh-binary"})
		u := Updater{Client: srv.Client(), APIURL: srv.URL + "/release", GOOS: "plan9", GOARCH: "mips"}

		err := u.Apply(t.Context(), "0.1.0", &bytes.Buffer{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no asset for plan9/mips")
	})

	t.Run("checksum mismatch leaves the binary alone", func(t *testing.T) {
		srv := releaseServer(t, "v0.2.0", map[string]string{"igit": "fresh-binary"})
		srv.corruptChecksums = true
		exe := filepath.Join(t.TempDir(), "igit")
		require.NoError(t, os.WriteFile(exe, []byte("stale"), 0o755)) //nolint:gosec // an executable under test needs the exec bit

		u := Updater{
			Client: srv.Client(), APIURL: srv.URL + "/release",
			ExecPath: func() (string, error) { return exe, nil },
			GOOS:     "linux", GOARCH: "amd64",
		}

		err := u.Apply(t.Context(), "0.1.0", &bytes.Buffer{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "checksum mismatch")

		data, err := os.ReadFile(exe) //nolint:gosec // fixed temp-dir path
		require.NoError(t, err)
		assert.Equal(t, "stale", string(data))
	})

	t.Run("development build updates anyway", func(t *testing.T) {
		srv := releaseServer(t, "v0.2.0", map[string]string{"igit": "fresh-binary"})
		exe := filepath.Join(t.TempDir(), "igit")
		require.NoError(t, os.WriteFile(exe, []byte("stale"), 0o755)) //nolint:gosec // an executable under test needs the exec bit

		u := Updater{
			Client: srv.Client(), APIURL: srv.URL + "/release",
			ExecPath: func() (string, error) { return exe, nil },
			GOOS:     "linux", GOARCH: "amd64",
		}

		var out bytes.Buffer
		require.NoError(t, u.Apply(t.Context(), "", &out))
		assert.Contains(t, out.String(), "development build")
		assert.Contains(t, out.String(), "updated to v0.2.0")
	})
}

// testServer serves a GitHub-shaped release plus its assets.
type testServer struct {
	*httptest.Server
	corruptChecksums bool
}

// releaseServer publishes tag with a linux/amd64 archive built from files, and
// the matching checksum listing. A nil files map publishes metadata only.
func releaseServer(t *testing.T, tag string, files map[string]string) *testServer {
	t.Helper()
	version := strings.TrimPrefix(tag, "v")
	assetName := AssetName(version, "linux", "amd64")
	sumsName := ChecksumName(version)

	var archive []byte
	if files != nil {
		archive = buildArchive(t, files)
	}

	ts := &testServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		body := `{"tag_name":%q,"assets":[]}`
		if files != nil {
			body = fmt.Sprintf(`{"tag_name":%%q,"assets":[{"name":%q,"browser_download_url":%q},{"name":%q,"browser_download_url":%q}]}`,
				assetName, base+"/asset", sumsName, base+"/sums")
		}
		_, _ = fmt.Fprintf(w, body, tag) //nolint:gosec // test fixture, tag is a literal
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		sum := sha256.Sum256(archive)
		digest := hex.EncodeToString(sum[:])
		if ts.corruptChecksums {
			digest = strings.Repeat("0", len(digest))
		}
		_, _ = fmt.Fprintf(w, "%s  %s\n", digest, assetName)
	})

	ts.Server = httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func writeArchive(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.tar.gz")
	require.NoError(t, os.WriteFile(path, buildArchive(t, files), 0o600))
	return path
}

func buildArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}
