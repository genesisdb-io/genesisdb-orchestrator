package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionLess(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"0.0.1", "0.0.2", true},
		{"0.0.99", "0.1.0", true},
		{"1.2.3", "1.2.3", false},
		{"1.3.0", "1.2.9", false},
		{"v1.2.3", "1.2.4", true},
	}
	for _, test := range tests {
		if got := versionLess(test.current, test.latest); got != test.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", test.current, test.latest, got, test.want)
		}
	}
}

func TestCheckAsyncSkipsDevelopmentBuild(t *testing.T) {
	select {
	case info := <-CheckAsync("0.0.0"):
		if info.Available || info.Current != "0.0.0" {
			t.Fatalf("unexpected update info: %#v", info)
		}
	case <-time.After(time.Second):
		t.Fatal("development update check did not return")
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch, name, format string
	}{
		{"darwin", "arm64", "genesisdb_1.2.3_darwin_arm64.tar.gz", "tar.gz"},
		{"linux", "amd64", "genesisdb_1.2.3_linux_amd64.tar.gz", "tar.gz"},
		{"windows", "arm64", "genesisdb_1.2.3_windows_arm64.zip", "zip"},
	}
	for _, test := range tests {
		name, format, err := assetName("1.2.3", test.goos, test.goarch)
		if err != nil {
			t.Fatal(err)
		}
		if name != test.name || format != test.format {
			t.Errorf("assetName: got %q %q, want %q %q", name, format, test.name, test.format)
		}
	}
	if _, _, err := assetName("1.2.3", "plan9", "amd64"); err == nil {
		t.Fatal("expected unsupported OS error")
	}
}

func TestExtractTarGzBinary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	body := "test binary"
	if err := tarWriter.WriteHeader(&tar.Header{Name: "genesisdb", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(dir, "genesisdb")
	if err := extractBinary(archive, "tar.gz", destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("extracted %q, want %q", got, body)
	}
}

func TestExtractZipBinary(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.zip")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("genesisdb.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strings.NewReader("windows binary").WriteTo(entry); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(dir, "genesisdb.exe")
	if err := extractBinary(archive, "zip", destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "windows binary" {
		t.Fatalf("unexpected extracted contents %q", got)
	}
}
