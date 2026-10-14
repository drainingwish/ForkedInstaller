/*
 * SPDX-License-Identifier: GPL-3.0
 * Vencord Installer, a cross platform gui/cli app for installing Vencord
 * Copyright (c) 2023 Vendicated and Vencord contributors
 */

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	path "path/filepath"
	"strings"
	"sync"
)

type GithubRelease struct {
	Name    string `json:"name"`
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

var ReleaseData GithubRelease
var GithubError error
var GithubDoneChan chan bool

var InstalledHash = "None"
var LatestHash = "Unknown"
var IsDevInstall bool

func GetGithubRelease(url, fallbackUrl string) (*GithubRelease, error) {
	Log.Debug("Fetching", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		Log.Error("Failed to create Request", err)
		return nil, err
	}

	req.Header.Set("User-Agent", UserAgent)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		Log.Error("Failed to send Request", err)
		return nil, err
	}

	defer res.Body.Close()

	if res.StatusCode >= 300 {
		isRateLimitedOrBlocked := res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 429
		triedFallback := url == fallbackUrl

		// GitHub has a very strict 60 req/h rate limit and some (mostly indian) isps block github for some reason.
		// If that is the case, try our fallback at https://vencord.dev/releases/project
		if isRateLimitedOrBlocked && !triedFallback {
			Log.Error(fmt.Sprintf("Failed to fetch %s (status code %d). Trying fallback url %s", url, res.StatusCode, fallbackUrl))
			return GetGithubRelease(fallbackUrl, fallbackUrl)
		}

		err = errors.New(res.Status)
		Log.Error(url, "returned Non-OK status", GithubError)
		return nil, err
	}

	var data GithubRelease

	if err = json.NewDecoder(res.Body).Decode(&data); err != nil {
		Log.Error("Failed to decode GitHub JSON Response", err)
		return nil, err
	}

	return &data, nil
}

func InitGithubDownloader() {
	GithubDoneChan = make(chan bool, 1)

	IsDevInstall = os.Getenv("VENCORD_DEV_INSTALL") == "1"
	Log.Debug("Is Dev Install: ", IsDevInstall)
	if IsDevInstall {
		GithubDoneChan <- true
		return
	}

	go func() {
		// Make sure UI updates once the request either finished or failed
		defer func() {
			GithubDoneChan <- GithubError == nil
		}()

		data, err := GetGithubRelease(ReleaseUrl, ReleaseUrlFallback)
		if err != nil {
			GithubError = err
			return
		}

		ReleaseData = *data

		i := strings.LastIndex(data.Name, " ") + 1
		LatestHash = data.Name[i:]
		Log.Debug("Finished fetching GitHub Data")
		Log.Debug("Latest hash is", LatestHash, "Local Install is", Ternary(LatestHash == InstalledHash, "up to date!", "outdated!"))
	}()

	// Check hash of installed version if exists
	f, err := os.Open(Patcher)
	if err != nil {
		return
	}
	//goland:noinspection GoUnhandledErrorResult
	defer f.Close()

	Log.Debug("Found existing Vencord Install. Checking for hash...")
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "// Vencord ") || strings.HasPrefix(line, "// Cutcord ") {
			InstalledHash = line[11:]
			Log.Debug("Existing hash is", InstalledHash)
		} else {
			Log.Debug("Didn't find hash")
		}
	}
}

var requiredBuildFiles = []string{"patcher.js", "preload.js", "renderer.js", "renderer.css"}

func isBuildAsset(name string) bool {
	for _, required := range requiredBuildFiles {
		if name == required || name == required+".map" || name == required+".LEGAL.txt" {
			return true
		}
	}
	return false
}

func downloadBuildAsset(url, destination string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("download failed: %s", res.Status)
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, res.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 {
		return errors.New("downloaded file is empty")
	}
	if res.ContentLength >= 0 && written != res.ContentLength {
		return errors.New("download ended before the complete file was received")
	}
	return nil
}

func installLatestBuilds() error {
	if FilesDirErr != nil {
		return FilesDirErr
	}
	assets := make(map[string]string)
	for _, asset := range ReleaseData.Assets {
		if isBuildAsset(asset.Name) {
			assets[asset.Name] = asset.DownloadURL
		}
	}
	for _, name := range requiredBuildFiles {
		if assets[name] == "" {
			return fmt.Errorf("release is missing required file %s", name)
		}
	}
	staging, err := os.MkdirTemp(FilesDir, ".download-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	failures := make(chan error, len(assets))
	var wg sync.WaitGroup
	for name, url := range assets {
		wg.Add(1)
		go func(name, url string) {
			defer wg.Done()
			if err := downloadBuildAsset(url, path.Join(staging, name)); err != nil {
				failures <- fmt.Errorf("failed to download %s: %w", name, err)
			}
		}(name, url)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		return err
	}
	// Only replace installed files once every download has completed successfully.
	for name := range assets {
		if err := os.Rename(path.Join(staging, name), path.Join(FilesDir, name)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path.Join(FilesDir, "package.json"), []byte("{}"), 0644); err != nil {
		return err
	}
	if err := FixOwnership(FilesDir); err != nil {
		return err
	}
	InstalledHash = LatestHash
	return nil
}
