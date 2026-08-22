package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xolan/xoldot/internal/status"
)

func TestReleaseUpdateReplacesExecutableWithMatchingVerifiedAsset(t *testing.T) {
	t.Parallel()
	const (
		latestVersion = "v1.3.0"
		binaryName    = "xoldot-v1.3.0-linux-arm64"
		archiveName   = binaryName + ".tar.gz"
	)
	newBinary := []byte("new xoldot binary\n")
	archive := releaseArchive(t, binaryName, newBinary)
	digest := sha256.Sum256(archive)

	server := releaseServer(t, latestVersion, map[string][]byte{
		archiveName:  archive,
		"SHA256SUMS": []byte(fmt.Sprintf("%x  ./%s\n", digest, archiveName)),
	})
	defer server.Close()

	executable := filepath.Join(t.TempDir(), "xoldot")
	if err := os.WriteFile(executable, []byte("old xoldot binary\n"), 0o751); err != nil {
		t.Fatal(err)
	}
	var reports strings.Builder
	updater := testReleaseUpdater(server, executable, &reports)
	if err := updater.Update(context.Background()); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newBinary) {
		t.Errorf("installed binary = %q, want %q", got, newBinary)
	}
	info, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o751 {
		t.Errorf("installed mode = %o, want 751", gotMode)
	}
	for _, want := range []string{
		"Checking GitHub for a newer release",
		"Downloading xoldot v1.3.0 for linux/arm64",
		"Updated xoldot from v1.2.3 to v1.3.0",
	} {
		if !strings.Contains(reports.String(), want) {
			t.Errorf("reports = %q, want %q", reports.String(), want)
		}
	}
}

func TestReleaseUpdateDoesNothingWhenCurrentIsLatest(t *testing.T) {
	t.Parallel()
	server := releaseServer(t, "v1.2.3", nil)
	defer server.Close()
	executable := filepath.Join(t.TempDir(), "xoldot")
	if err := os.WriteFile(executable, []byte("keep me\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var reports strings.Builder
	updater := testReleaseUpdater(server, executable, &reports)
	if err := updater.Update(context.Background()); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep me\n" {
		t.Errorf("executable changed to %q", got)
	}
	if !strings.Contains(reports.String(), "xoldot v1.2.3 is already current") {
		t.Errorf("reports = %q", reports.String())
	}
}

func TestReleaseUpdateRejectsChecksumMismatchWithoutChangingExecutable(t *testing.T) {
	t.Parallel()
	const (
		binaryName  = "xoldot-v2.0.0-linux-arm64"
		archiveName = binaryName + ".tar.gz"
	)
	archive := releaseArchive(t, binaryName, []byte("untrusted update\n"))
	server := releaseServer(t, "v2.0.0", map[string][]byte{
		archiveName:  archive,
		"SHA256SUMS": []byte(strings.Repeat("0", 64) + "  " + archiveName + "\n"),
	})
	defer server.Close()
	executable := filepath.Join(t.TempDir(), "xoldot")
	if err := os.WriteFile(executable, []byte("keep me\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	updater := testReleaseUpdater(server, executable, nil)
	err := updater.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Update() error = %v, want checksum mismatch", err)
	}
	got, readErr := os.ReadFile(executable)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "keep me\n" {
		t.Errorf("executable changed to %q", got)
	}
}

func TestReleaseUpdateRequiresMatchingPlatformAsset(t *testing.T) {
	t.Parallel()
	server := releaseServer(t, "v1.3.0", map[string][]byte{
		"xoldot-v1.3.0-darwin-arm64.tar.gz": []byte("wrong platform"),
		"SHA256SUMS":                        []byte("unused"),
	})
	defer server.Close()
	executable := filepath.Join(t.TempDir(), "xoldot")
	if err := os.WriteFile(executable, []byte("keep me\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	updater := testReleaseUpdater(server, executable, nil)
	err := updater.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "xoldot-v1.3.0-linux-arm64.tar.gz") {
		t.Fatalf("Update() error = %v, want missing platform asset", err)
	}
}

func testReleaseUpdater(server *httptest.Server, executable string, reports *strings.Builder) Updater {
	var reporter status.Reporter
	if reports != nil {
		reporter = status.ReporterFunc(func(kind status.Kind, text string) error {
			fmt.Fprintf(reports, "%d %s\n", kind, text)
			return nil
		})
	}
	return Updater{
		Version:  "v1.2.3",
		Reporter: reporter,
		apiURL:   server.URL + "/latest",
		client:   server.Client(),
		executable: func() (string, error) {
			return executable, nil
		},
		runtimeOS:   "linux",
		runtimeArch: "arm64",
	}
}

func releaseServer(t *testing.T, version string, assets map[string][]byte) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/latest" {
			type asset struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}
			payload := struct {
				TagName string  `json:"tag_name"`
				Assets  []asset `json:"assets"`
			}{TagName: version}
			for name := range assets {
				payload.Assets = append(payload.Assets, asset{Name: name, URL: server.URL + "/assets/" + name})
			}
			response.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(response).Encode(payload); err != nil {
				t.Errorf("encode release response: %v", err)
			}
			return
		}
		const prefix = "/assets/"
		if !strings.HasPrefix(request.URL.Path, prefix) {
			http.NotFound(response, request)
			return
		}
		contents, ok := assets[strings.TrimPrefix(request.URL.Path, prefix)]
		if !ok {
			http.NotFound(response, request)
			return
		}
		if _, err := response.Write(contents); err != nil {
			t.Errorf("write asset response: %v", err)
		}
	}))
	return server
}

func releaseArchive(t *testing.T, binaryName string, contents []byte) []byte {
	t.Helper()
	var result bytes.Buffer
	compressed := gzip.NewWriter(&result)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{
		Name: binaryName,
		Mode: 0o755,
		Size: int64(len(contents)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}
