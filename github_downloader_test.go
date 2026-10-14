package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configureTestDownload(t *testing.T, base string, names []string) string {
	t.Helper()
	oldRelease, oldDir, oldErr := ReleaseData, FilesDir, FilesDirErr
	oldInstalled, oldLatest := InstalledHash, LatestHash
	t.Cleanup(func() {
		ReleaseData, FilesDir, FilesDirErr = oldRelease, oldDir, oldErr
		InstalledHash, LatestHash = oldInstalled, oldLatest
	})
	FilesDir, FilesDirErr = t.TempDir(), nil
	LatestHash = "new-build"
	InstalledHash = "old-build"
	assets := []map[string]string{}
	for _, name := range names {
		assets = append(assets, map[string]string{"name": name, "browser_download_url": base + "/" + name})
	}
	data, _ := json.Marshal(map[string]any{"assets": assets})
	if err := json.Unmarshal(data, &ReleaseData); err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredBuildFiles {
		if err := os.WriteFile(filepath.Join(FilesDir, name), []byte("old "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return FilesDir
}

func TestSuccessfulBuildDownloadSupportsChunkedResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != UserAgent {
			t.Error("missing user agent")
		}
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("new " + strings.TrimPrefix(r.URL.Path, "/")))
	}))
	defer server.Close()
	dir := configureTestDownload(t, server.URL, requiredBuildFiles)
	if err := installLatestBuilds(); err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredBuildFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "new "+name {
			t.Fatalf("incorrect installed file %s", name)
		}
	}
	if InstalledHash != LatestHash {
		t.Fatal("installed hash not updated")
	}
}

func TestFailedDownloadPreservesInstalledBuild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/renderer.css" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("new file"))
	}))
	defer server.Close()
	dir := configureTestDownload(t, server.URL, requiredBuildFiles)
	if err := installLatestBuilds(); err == nil {
		t.Fatal("failed download reported success")
	}
	for _, name := range requiredBuildFiles {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "old "+name {
			t.Fatalf("failed download changed %s", name)
		}
	}
	if InstalledHash != "old-build" {
		t.Fatal("failed download changed installed hash")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != len(requiredBuildFiles) {
		t.Fatal("staging files were not cleaned up")
	}
}

func TestReleaseRequiresEveryRuntimeFile(t *testing.T) {
	configureTestDownload(t, "http://127.0.0.1:1", []string{"patcher.js", "patcher.js.map", "renderer.js", "renderer.css"})
	err := installLatestBuilds()
	if err == nil || !strings.Contains(err.Error(), "preload.js") {
		t.Fatalf("missing file was not detected: %v", err)
	}
}

func TestInstallerDownloadsStayOnFork(t *testing.T) {
	if !strings.Contains(InstallerReleaseUrl, "drainingwish/ForkedInstaller/") {
		t.Fatal("self-update API points upstream")
	}
	if !strings.HasPrefix(GetInstallerDownloadLink(), "https://github.com/drainingwish/ForkedInstaller/") {
		t.Fatal("installer download points upstream")
	}
	if InstallerReleaseUrlFallback != InstallerReleaseUrl {
		t.Fatal("self-update fallback can leave the fork")
	}
}

func TestPublishedCutcordBuild(t *testing.T) {
	if os.Getenv("CUTCORD_RELEASE_INTEGRATION") != "1" {
		t.Skip("set CUTCORD_RELEASE_INTEGRATION=1 for the live download check")
	}
	release, err := GetGithubRelease(ReleaseUrl, ReleaseUrlFallback)
	if err != nil {
		t.Fatal(err)
	}
	configureTestDownload(t, "", requiredBuildFiles)
	ReleaseData = *release
	if err := installLatestBuilds(); err != nil {
		t.Fatal(err)
	}
	for _, name := range requiredBuildFiles {
		info, err := os.Stat(filepath.Join(FilesDir, name))
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing downloaded runtime file %s", name)
		}
	}
}
