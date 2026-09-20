// Package update checks GitHub releases for a newer igit and replaces the
// running binary with it. Release assets follow the goreleaser naming used by
// .goreleaser.yml: igit_<version>_<os>_<arch>.tar.gz next to a
// igit_<version>_checksums.txt.
package update

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// RepoURL is the project page, shown when a self-update cannot proceed.
	RepoURL = "https://github.com/YouSysAdmin/igit"
	// DefaultAPIURL is the GitHub API endpoint for the latest release.
	DefaultAPIURL = "https://api.github.com/repos/YouSysAdmin/igit/releases/latest"

	binaryName = "igit"
	// userAgent identifies the updater. GitHub rejects unauthenticated
	// requests that do not send one.
	userAgent = "igit-updater (+" + RepoURL + ")"

	// apiTimeout bounds the release metadata and checksum requests, which are
	// small. The archive download gets its own, longer budget.
	apiTimeout      = 30 * time.Second
	downloadTimeout = 10 * time.Minute

	// maxBinarySize caps what is read out of an archive, so a crafted release
	// cannot expand into unbounded memory.
	maxBinarySize = 256 << 20
)

// release is the subset of the GitHub release payload the updater reads.
type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Result is the outcome of a version check.
type Result struct {
	Current string // running version, empty for a development build
	Latest  string // newest released version
	Newer   bool   // true when Latest is ahead of Current
}

// Updater talks to the GitHub releases API. The zero value is usable and hits
// the real endpoint. The fields exist so tests can point it elsewhere.
type Updater struct {
	Client   *http.Client
	APIURL   string
	ExecPath func() (string, error) // resolved path of the running binary
	// GOOS and GOARCH name the asset to fetch, defaulting to the build's own.
	GOOS, GOARCH string
}

func (u Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

func (u Updater) apiURL() string { return cmp.Or(u.APIURL, DefaultAPIURL) }

func (u Updater) platform() (goos, goarch string) {
	return cmp.Or(u.GOOS, runtime.GOOS), cmp.Or(u.GOARCH, runtime.GOARCH)
}

func (u Updater) execPath() (string, error) {
	if u.ExecPath != nil {
		return u.ExecPath()
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", exe, err)
	}
	return resolved, nil
}

// Check reports the newest release and whether it is ahead of current. An
// unparseable current version (a development build) still reports the latest
// release, with Newer false, because there is nothing to compare against.
func (u Updater) Check(ctx context.Context, current string) (Result, error) {
	rel, err := u.fetchRelease(ctx)
	if err != nil {
		return Result{}, err
	}
	return compare(rel, current)
}

// compare turns a fetched release into a Result, so Apply can reuse the one
// release it already fetched instead of asking again.
func compare(rel release, current string) (Result, error) {
	latest, ok := parseTag(rel.TagName)
	if !ok {
		return Result{}, fmt.Errorf("cannot parse latest release tag %q", rel.TagName)
	}
	res := Result{Current: current, Latest: latest}
	res.Newer = current != "" && Compare(current, latest) < 0
	return res, nil
}

// Apply downloads the newest release and replaces the running binary with it,
// reporting progress on w. It is a no-op when the running version is already
// current.
func (u Updater) Apply(ctx context.Context, current string, w io.Writer) error {
	rel, err := u.fetchRelease(ctx)
	if err != nil {
		return err
	}
	res, err := compare(rel, current)
	if err != nil {
		return err
	}
	switch {
	case current == "":
		_, _ = fmt.Fprintln(w, "development build, current version unknown")
	case !res.Newer:
		_, _ = fmt.Fprintf(w, "already up to date (v%s)\n", current)
		return nil
	}

	goos, goarch := u.platform()
	name := AssetName(res.Latest, goos, goarch)
	src := assetURL(rel.Assets, name)
	if src == "" {
		return fmt.Errorf("release v%s has no asset for %s/%s (expected %s)", res.Latest, goos, goarch, name)
	}

	_, _ = fmt.Fprintf(w, "downloading igit v%s for %s/%s\n", res.Latest, goos, goarch)
	archive, err := u.download(ctx, src)
	if err != nil {
		return fmt.Errorf("download release: %w", err)
	}
	defer func() { _ = os.Remove(archive) }()

	_, _ = fmt.Fprintln(w, "verifying checksum")
	if verr := u.verify(ctx, rel.Assets, archive, res.Latest, name); verr != nil {
		return verr
	}

	bin, err := extractBinary(archive)
	if err != nil {
		return err
	}
	exe, err := u.execPath()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}

	_, _ = fmt.Fprintf(w, "replacing %s\n", exe)
	if err := replace(exe, bin); err != nil {
		return err
	}
	if current == "" {
		_, _ = fmt.Fprintf(w, "updated to v%s\n", res.Latest)
		return nil
	}
	_, _ = fmt.Fprintf(w, "updated v%s -> v%s\n", current, res.Latest)
	return nil
}

// AssetName builds the release archive name for a version and platform,
// matching the goreleaser name_template.
func AssetName(version, goos, goarch string) string {
	return fmt.Sprintf("%s_%s_%s_%s.tar.gz", binaryName, version, goos, goarch)
}

// ChecksumName builds the name of the checksum file goreleaser publishes
// alongside the archives.
func ChecksumName(version string) string {
	return fmt.Sprintf("%s_%s_checksums.txt", binaryName, version)
}

// Version extracts the released version from a build revision. Release builds
// carry "<tag>-<commit>-<date>", so the tag is the part before the first dash
// and a development build, whose first field is a branch name, reports false.
func Version(revision string) (string, bool) {
	tag, _, _ := strings.Cut(revision, "-")
	return parseTag(tag)
}

// Compare orders two versions, returning -1, 0 or +1. A leading "v" is
// ignored. Pre-release suffixes are not ordered: v1.0.0-rc1 and v1.0.0 compare
// equal, which keeps a release candidate from being offered an update to the
// version it already is.
func Compare(a, b string) int {
	aMaj, aMin, aPatch, _ := splitVersion(a)
	bMaj, bMin, bPatch, _ := splitVersion(b)
	if c := cmp.Compare(aMaj, bMaj); c != 0 {
		return c
	}
	if c := cmp.Compare(aMin, bMin); c != 0 {
		return c
	}
	return cmp.Compare(aPatch, bPatch)
}

// parseTag validates a release tag and returns it without the "v" prefix.
func parseTag(tag string) (string, bool) {
	v := strings.TrimPrefix(tag, "v")
	if _, _, _, err := splitVersion(v); err != nil {
		return "", false
	}
	return v, true
}

func splitVersion(s string) (major, minor, patch int, err error) {
	s = strings.TrimPrefix(s, "v")
	majorStr, rest, ok := strings.Cut(s, ".")
	if !ok {
		return 0, 0, 0, fmt.Errorf("invalid version %q", s)
	}
	minorStr, patchStr, ok := strings.Cut(rest, ".")
	if !ok {
		return 0, 0, 0, fmt.Errorf("invalid version %q", s)
	}
	// drop a pre-release suffix so "1.2.3-rc1" still yields a patch number
	patchStr, _, _ = strings.Cut(patchStr, "-")
	if major, err = strconv.Atoi(majorStr); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid major version in %q", s)
	}
	if minor, err = strconv.Atoi(minorStr); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid minor version in %q", s)
	}
	if patch, err = strconv.Atoi(patchStr); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid patch version in %q", s)
	}
	return major, minor, patch, nil
}

func (u Updater) fetchRelease(ctx context.Context) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()

	body, err := u.get(ctx, u.apiURL(), "application/vnd.github+json")
	if err != nil {
		return release{}, fmt.Errorf("fetch release info: %w", err)
	}
	defer body.Close()

	var rel release
	if err := json.UnmarshalRead(body, &rel); err != nil {
		return release{}, fmt.Errorf("parse release info: %w", err)
	}
	return rel, nil
}

// get issues the request and hands back the body, which the caller closes. A
// non-200 response is an error and the body is closed here.
func (u Updater) get(ctx context.Context, url, accept string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", url, err)
	}
	req.Header.Set("User-Agent", userAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s returned status %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}

func assetURL(assets []asset, name string) string {
	if i := slices.IndexFunc(assets, func(a asset) bool { return a.Name == name }); i >= 0 {
		return assets[i].URL
	}
	return ""
}

// download streams the archive to a temporary file and returns its path. The
// caller removes it.
func (u Updater) download(ctx context.Context, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	body, err := u.get(ctx, url, "")
	if err != nil {
		return "", err
	}
	defer body.Close()

	tmp, err := os.CreateTemp("", "igit-update-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	if _, cerr := io.Copy(tmp, body); cerr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("write %s: %w", tmp.Name(), cerr)
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("write %s: %w", tmp.Name(), cerr)
	}
	return tmp.Name(), nil
}

func (u Updater) verify(ctx context.Context, assets []asset, archivePath, version, assetName string) error {
	sums := ChecksumName(version)
	src := assetURL(assets, sums)
	if src == "" {
		return fmt.Errorf("release v%s has no %s to verify against", version, sums)
	}

	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()

	body, err := u.get(ctx, src, "")
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	defer body.Close()

	want, err := checksumFor(body, assetName)
	if err != nil {
		return err
	}
	got, err := hashFile(archivePath)
	if err != nil {
		return err
	}
	if want != got {
		return fmt.Errorf("checksum mismatch for %s: want %s, got %s", assetName, want, got)
	}
	return nil
}

// checksumFor reads a sha256sum-style listing and returns the digest of the
// named file. The name may carry a "*" binary marker or a directory prefix.
func checksumFor(r io.Reader, name string) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if filepath.Base(strings.TrimPrefix(fields[len(fields)-1], "*")) == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", name)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // path is the temp file download just wrote
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, cerr := io.Copy(h, f); cerr != nil {
		return "", fmt.Errorf("read %s: %w", path, cerr)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary pulls the igit executable out of the release tarball, which
// also carries the license files and the shell completions.
func extractBinary(archivePath string) ([]byte, error) {
	f, err := os.Open(archivePath) //nolint:gosec // path is the temp file download just wrote
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", archivePath, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != binaryName {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxBinarySize+1))
		if err != nil {
			return nil, fmt.Errorf("read %s from archive: %w", binaryName, err)
		}
		if len(data) > maxBinarySize {
			return nil, fmt.Errorf("%s in archive exceeds %d bytes", binaryName, maxBinarySize)
		}
		return data, nil
	}
	return nil, fmt.Errorf("archive does not contain %s", binaryName)
}

// replace swaps the running binary for data. The new file is written next to
// it and renamed over it, so the swap is atomic and a failed write leaves the
// old binary in place. Unlinking a running executable is fine on unix: the
// process keeps its own open inode.
func replace(exePath string, data []byte) error {
	info, err := os.Stat(exePath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", exePath, err)
	}
	mode := info.Mode().Perm()

	tmp, err := os.CreateTemp(filepath.Dir(exePath), ".igit-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w (update it with the package manager that installed it, or reinstall)", exePath, err)
	}
	newPath := tmp.Name()
	if _, werr := tmp.Write(data); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(newPath)
		return fmt.Errorf("write %s: %w", newPath, werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("write %s: %w", newPath, cerr)
	}
	// CreateTemp makes the file 0600, so the old binary's mode is restored
	if cerr := os.Chmod(newPath, mode); cerr != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("set mode on %s: %w", newPath, cerr)
	}
	if err := os.Rename(newPath, exePath); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("replace %s: %w", exePath, err)
	}
	return nil
}
