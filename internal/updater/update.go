// Package updater checks for GenesisDB CLI releases and replaces the current binary.
package updater

import (
	"archive/tar"
	"archive/zip"
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
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	githubAPI     = "https://api.github.com/repos/genesisdb-io/genesisdb-orchestrator/releases/latest"
	releasesBase  = "https://github.com/genesisdb-io/genesisdb-orchestrator/releases"
	cacheFileName = "update-check.json"
	cacheTTL      = 12 * time.Hour
)

// Info describes the latest published release relative to this binary.
type Info struct {
	Current   string
	Latest    string
	URL       string
	Available bool
}

type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	CurrentAt string    `json:"current_at"`
	Latest    string    `json:"latest"`
	URL       string    `json:"url"`
}

type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// Check checks for a newer release and caches the result for twelve hours.
// Network errors are returned so explicit checks can report them.
func Check(ctx context.Context, current string, useCache bool) (Info, error) {
	current = versionOnly(current)
	if isDevelopmentVersion(current) {
		return Info{Current: current}, nil
	}

	cachePath := updateCachePath()
	if useCache {
		if cached, ok := readCache(cachePath); ok && cached.CurrentAt == current && time.Since(cached.CheckedAt) < cacheTTL {
			return buildInfo(current, cached.Latest, cached.URL), nil
		}
	}

	latest, err := fetchLatest(ctx)
	if err != nil {
		return Info{}, err
	}
	_ = writeCache(cachePath, cache{
		CheckedAt: time.Now().UTC(),
		CurrentAt: current,
		Latest:    latest.TagName,
		URL:       latest.HTMLURL,
	})
	return buildInfo(current, latest.TagName, latest.HTMLURL), nil
}

// CheckAsync checks for an update without delaying the calling command.
func CheckAsync(current string) <-chan Info {
	result := make(chan Info, 1)
	go func() {
		defer close(result)
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		info, err := Check(ctx, current, true)
		if err == nil {
			result <- info
		}
	}()
	return result
}

// Install downloads, verifies, and installs the latest release over this binary.
func Install(ctx context.Context, current string, output io.Writer) error {
	current = versionOnly(current)
	if isDevelopmentVersion(current) {
		return fmt.Errorf("development build %s cannot self-update; install a published release from %s", current, releasesBase)
	}

	fmt.Fprintln(output, "Checking for the latest GenesisDB release...")
	info, err := Check(ctx, current, false)
	if err != nil {
		return fmt.Errorf("query latest release: %w", err)
	}
	if !info.Available {
		fmt.Fprintf(output, "GenesisDB %s is already up to date.\n", current)
		return nil
	}

	asset, format, err := assetName(info.Latest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	base := releasesBase + "/download/v" + info.Latest
	tmp, err := os.MkdirTemp("", "genesisdb-update-*")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(tmp)

	fmt.Fprintf(output, "Downloading GenesisDB %s...\n", info.Latest)
	sumsPath := filepath.Join(tmp, "checksums.txt")
	if err := download(ctx, base+"/checksums.txt", sumsPath); err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	want, err := lookupChecksum(sumsPath, asset)
	if err != nil {
		return err
	}
	archivePath := filepath.Join(tmp, asset)
	if err := download(ctx, base+"/"+asset, archivePath); err != nil {
		return fmt.Errorf("download release: %w", err)
	}
	got, err := sha256File(archivePath)
	if err != nil {
		return fmt.Errorf("verify release: %w", err)
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}

	newBinary := filepath.Join(tmp, executableName())
	if err := extractBinary(archivePath, format, newBinary); err != nil {
		return fmt.Errorf("extract release: %w", err)
	}
	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(currentBinary); err == nil {
		currentBinary = resolved
	}
	if err := replaceBinary(currentBinary, newBinary); err != nil {
		return fmt.Errorf("replace %s: %w", currentBinary, err)
	}
	fmt.Fprintf(output, "Updated GenesisDB from %s to %s.\n", current, info.Latest)
	return nil
}

func buildInfo(current, latest, url string) Info {
	latest = strings.TrimPrefix(latest, "v")
	return Info{Current: current, Latest: latest, URL: url, Available: versionLess(current, latest)}
}

func versionLess(a, b string) bool {
	av, bv := splitVersion(a), splitVersion(b)
	for i := 0; i < 3; i++ {
		if av[i] != bv[i] {
			return av[i] < bv[i]
		}
	}
	return false
}

func splitVersion(value string) [3]int {
	value = versionOnly(value)
	parts := strings.Split(value, ".")
	var result [3]int
	for i := 0; i < len(parts) && i < len(result); i++ {
		result[i], _ = strconv.Atoi(parts[i])
	}
	return result
}

func versionOnly(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	if i := strings.IndexAny(value, " ("); i > 0 {
		value = value[:i]
	}
	return value
}

func isDevelopmentVersion(version string) bool {
	return version == "" || version == "dev" || version == "0.0.0"
}

func fetchLatest(ctx context.Context) (release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPI, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}
	var result release
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return release{}, err
	}
	if result.TagName == "" {
		return release{}, errors.New("latest release has no tag")
	}
	return result, nil
}

func updateCachePath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return filepath.Join(".", cacheFileName)
	}
	return filepath.Join(base, "genesisdb", cacheFileName)
}

func readCache(path string) (cache, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cache{}, false
	}
	var result cache
	if json.Unmarshal(data, &result) != nil {
		return cache{}, false
	}
	return result, true
}

func writeCache(path string, value cache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func assetName(version, goos, goarch string) (string, string, error) {
	if goos != "linux" && goos != "darwin" && goos != "windows" {
		return "", "", fmt.Errorf("unsupported operating system: %s", goos)
	}
	if goarch != "amd64" && goarch != "arm64" {
		return "", "", fmt.Errorf("unsupported architecture: %s", goarch)
	}
	format := "tar.gz"
	if goos == "windows" {
		format = "zip"
	}
	return fmt.Sprintf("genesisdb_%s_%s_%s.%s", version, goos, goarch, format), format, nil
}

func download(ctx context.Context, url, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	return err
}

func lookupChecksum(path, asset string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum for %s is not listed", asset)
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "genesisdb.exe"
	}
	return "genesisdb"
}

func extractBinary(archivePath, format, destination string) error {
	switch format {
	case "tar.gz":
		file, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer file.Close()
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gzipReader.Close()
		reader := tar.NewReader(gzipReader)
		for {
			header, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if filepath.Base(header.Name) == "genesisdb" && header.Typeflag == tar.TypeReg {
				return writeExtracted(reader, destination)
			}
		}
	case "zip":
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if filepath.Base(file.Name) != "genesisdb.exe" || file.FileInfo().IsDir() {
				continue
			}
			input, err := file.Open()
			if err != nil {
				return err
			}
			err = writeExtracted(input, destination)
			input.Close()
			return err
		}
	default:
		return fmt.Errorf("unsupported archive format: %s", format)
	}
	return errors.New("release archive does not contain the GenesisDB executable")
}

func writeExtracted(input io.Reader, destination string) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func replaceBinary(current, replacement string) error {
	info, err := os.Stat(current)
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o755
	}
	if runtime.GOOS == "windows" {
		backup := current + ".old"
		_ = os.Remove(backup)
		if err := os.Rename(current, backup); err != nil {
			return err
		}
		if err := os.Rename(replacement, current); err != nil {
			_ = os.Rename(backup, current)
			return err
		}
		return nil
	}

	if err := os.Chmod(replacement, mode); err != nil {
		return err
	}
	if err := os.Rename(replacement, current); err == nil {
		return nil
	}
	input, err := os.Open(replacement)
	if err != nil {
		return err
	}
	defer input.Close()
	temporary := current + ".new"
	output, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		_ = os.Remove(temporary)
		return err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, current)
}
