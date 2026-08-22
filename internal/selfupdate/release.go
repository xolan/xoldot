package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/xolan/xoldot/internal/status"
)

const (
	maxReleaseMetadataSize = 1 << 20
	maxChecksumFileSize    = 1 << 20
	maxArchiveSize         = 128 << 20
	maxBinarySize          = 128 << 20
)

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func (updater Updater) updateRelease(ctx context.Context, current releaseVersion) error {
	if err := updater.reportf(status.Progress, "Checking GitHub for a newer release"); err != nil {
		return err
	}
	release, err := updater.latestRelease(ctx)
	if err != nil {
		return err
	}
	latest, err := parseReleaseVersion(release.TagName)
	if err != nil {
		return fmt.Errorf("GitHub's latest release has invalid tag %q: %w", release.TagName, err)
	}
	if current.compare(latest) >= 0 {
		return updater.reportf(status.Success, "xoldot %s is already current", current)
	}

	binaryName := fmt.Sprintf("xoldot-%s-%s-%s", latest, updater.runtimeOS, updater.runtimeArch)
	archiveName := binaryName + ".tar.gz"
	archiveAsset, err := findAsset(release.Assets, archiveName)
	if err != nil {
		return err
	}
	checksumAsset, err := findAsset(release.Assets, "SHA256SUMS")
	if err != nil {
		return err
	}

	if err := updater.reportf(status.Progress, "Downloading xoldot %s for %s/%s", latest, updater.runtimeOS, updater.runtimeArch); err != nil {
		return err
	}
	checksums, err := updater.downloadBytes(ctx, checksumAsset.URL, maxChecksumFileSize)
	if err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	wantDigest, err := checksumFor(checksums, archiveName)
	if err != nil {
		return err
	}

	archivePath, gotDigest, err := updater.downloadArchive(ctx, archiveAsset.URL)
	if err != nil {
		return fmt.Errorf("download %s: %w", archiveName, err)
	}
	defer func() { _ = os.Remove(archivePath) }()
	if !bytes.Equal(gotDigest[:], wantDigest[:]) {
		return fmt.Errorf("checksum mismatch for %s", archiveName)
	}

	if err := updater.installArchive(archivePath, binaryName); err != nil {
		return err
	}
	return updater.reportf(status.Success, "Updated xoldot from %s to %s", current, latest)
}

func (updater Updater) latestRelease(ctx context.Context) (githubRelease, error) {
	data, err := updater.downloadBytes(ctx, updater.apiURL, maxReleaseMetadataSize)
	if err != nil {
		return githubRelease{}, fmt.Errorf("check GitHub releases: %w", err)
	}
	var release githubRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return githubRelease{}, fmt.Errorf("decode GitHub release: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubRelease{}, errors.New("GitHub's latest release has no tag")
	}
	return release, nil
}

func findAsset(assets []githubAsset, name string) (githubAsset, error) {
	var found githubAsset
	for _, asset := range assets {
		if asset.Name != name {
			continue
		}
		if found.Name != "" {
			return githubAsset{}, fmt.Errorf("GitHub release contains more than one %s asset", name)
		}
		found = asset
	}
	if found.Name == "" {
		return githubAsset{}, fmt.Errorf("GitHub release does not contain %s", name)
	}
	if strings.TrimSpace(found.URL) == "" {
		return githubAsset{}, fmt.Errorf("GitHub release asset %s has no download URL", name)
	}
	return found, nil
}

func (updater Updater) downloadBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	response, err := updater.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	return readLimited(response.Body, response.ContentLength, limit)
}

func (updater Updater) downloadArchive(ctx context.Context, rawURL string) (string, [sha256.Size]byte, error) {
	response, err := updater.get(ctx, rawURL)
	if err != nil {
		return "", [sha256.Size]byte{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.ContentLength > maxArchiveSize {
		return "", [sha256.Size]byte{}, fmt.Errorf("download is larger than %d bytes", maxArchiveSize)
	}

	archive, err := os.CreateTemp("", "xoldot-update-*.tar.gz")
	if err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("create temporary archive: %w", err)
	}
	archivePath := archive.Name()
	keep := false
	defer func() {
		_ = archive.Close()
		if !keep {
			_ = os.Remove(archivePath)
		}
	}()

	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(archive, digest), io.LimitReader(response.Body, maxArchiveSize+1))
	if err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("write temporary archive: %w", err)
	}
	if written > maxArchiveSize {
		return "", [sha256.Size]byte{}, fmt.Errorf("download is larger than %d bytes", maxArchiveSize)
	}
	if err := archive.Close(); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("close temporary archive: %w", err)
	}

	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	keep = true
	return archivePath, result, nil
}

func (updater Updater) get(ctx context.Context, rawURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "xoldot/"+updater.Version)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := updater.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = response.Body.Close() }()
		message, _ := readLimited(response.Body, response.ContentLength, 4<<10)
		message = bytes.TrimSpace(message)
		if len(message) == 0 {
			return nil, fmt.Errorf("GET %s returned %s", rawURL, response.Status)
		}
		return nil, fmt.Errorf("GET %s returned %s: %s", rawURL, response.Status, message)
	}
	return response, nil
}

func readLimited(reader io.Reader, contentLength, limit int64) ([]byte, error) {
	if contentLength > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	return data, nil
}

func checksumFor(contents []byte, archiveName string) ([sha256.Size]byte, error) {
	var found [sha256.Size]byte
	matches := 0
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != archiveName && name != "./"+archiveName {
			continue
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return [sha256.Size]byte{}, fmt.Errorf("SHA256SUMS has an invalid checksum for %s", archiveName)
		}
		copy(found[:], digest)
		matches++
	}
	if matches == 0 {
		return [sha256.Size]byte{}, fmt.Errorf("SHA256SUMS does not contain %s", archiveName)
	}
	if matches > 1 {
		return [sha256.Size]byte{}, fmt.Errorf("SHA256SUMS contains more than one checksum for %s", archiveName)
	}
	return found, nil
}

func (updater Updater) installArchive(archivePath, binaryName string) error {
	executablePath, err := updater.executable()
	if err != nil {
		return fmt.Errorf("locate the running xoldot executable: %w", err)
	}
	executablePath, err = filepath.Abs(executablePath)
	if err != nil {
		return fmt.Errorf("resolve the running xoldot executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executablePath); resolveErr == nil {
		executablePath = resolved
	} else {
		return fmt.Errorf("resolve the running xoldot executable: %w", resolveErr)
	}
	original, err := os.Stat(executablePath)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", executablePath, err)
	}
	if !original.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", executablePath)
	}

	candidate, err := os.CreateTemp(filepath.Dir(executablePath), ".xoldot-update-*")
	if err != nil {
		return fmt.Errorf("create an update beside %s: %w", executablePath, err)
	}
	candidatePath := candidate.Name()
	defer func() { _ = os.Remove(candidatePath) }()

	if err := extractBinary(archivePath, binaryName, candidate); err != nil {
		return errors.Join(err, candidate.Close())
	}
	if err := candidate.Chmod(original.Mode().Perm()); err != nil {
		return errors.Join(fmt.Errorf("set permissions on downloaded xoldot: %w", err), candidate.Close())
	}
	if err := candidate.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync downloaded xoldot: %w", err), candidate.Close())
	}
	if err := candidate.Close(); err != nil {
		return fmt.Errorf("close downloaded xoldot: %w", err)
	}

	current, err := os.Stat(executablePath)
	if err != nil {
		return fmt.Errorf("recheck %s before replacing it: %w", executablePath, err)
	}
	if !os.SameFile(original, current) {
		return fmt.Errorf("%s changed while the update was downloading", executablePath)
	}
	if err := os.Rename(candidatePath, executablePath); err != nil {
		return fmt.Errorf("replace %s: %w", executablePath, err)
	}
	return nil
}

func extractBinary(archivePath, binaryName string, destination *os.File) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open downloaded archive: %w", err)
	}
	defer func() { _ = archive.Close() }()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("open downloaded gzip archive: %w", err)
	}
	defer func() { _ = compressed.Close() }()

	reader := tar.NewReader(compressed)
	found := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read downloaded archive: %w", err)
		}
		if header.Name != binaryName {
			continue
		}
		if found {
			return fmt.Errorf("downloaded archive contains more than one %s", binaryName)
		}
		if !header.FileInfo().Mode().IsRegular() {
			return fmt.Errorf("downloaded archive entry %s is not a regular file", binaryName)
		}
		if header.Size < 0 || header.Size > maxBinarySize {
			return fmt.Errorf("downloaded binary is larger than %d bytes", maxBinarySize)
		}
		if _, err := io.Copy(destination, reader); err != nil {
			return fmt.Errorf("extract downloaded binary: %w", err)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("downloaded archive does not contain %s", binaryName)
	}
	return nil
}
