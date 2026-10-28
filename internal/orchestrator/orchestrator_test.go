package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	valid := []string{"dev", "dev-1", "a", "a1"}
	invalid := []string{"", "Dev", "-dev", "dev-", "dev.local", "genesisdb", "a_b"}

	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Errorf("%q should be valid: %v", name, err)
		}
	}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Errorf("%q should be invalid", name)
		}
	}
}

func TestValidateToken(t *testing.T) {
	if err := ValidateToken(strings.Repeat("a", MinimumTokenLength)); err != nil {
		t.Errorf("token of minimum length rejected: %v", err)
	}
	for _, token := range []string{"", "development-secret", strings.Repeat("a", MinimumTokenLength-1)} {
		if err := ValidateToken(token); err == nil {
			t.Errorf("token of %d bytes should be rejected", len(token))
		}
	}
}

// The status payload is the GET /api/v2/status answer of GenesisDB 2.0.
func TestStatusJSON(t *testing.T) {
	payload := []byte(`{
		"engine":{"version":"2.0.0","channel":"stable"},
		"system":{"os":"linux","arch":"arm64","cpu":{"availableCores":8,"usedCores":2},"memory":{"total":100,"used":40,"available":60},"storage":{"max":1000,"used":300,"available":700}},
		"events":{"count":42,"subjects":7,"types":3,"storageSize":2048},
		"storage":{"storageFormat":"current","ready":true}
	}`)
	var status Status
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatal(err)
	}
	if status.Engine.Version != "2.0.0" || status.Events.Count != 42 || status.System.Storage.Max != 1000 {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.Storage.Format != "current" || !status.Storage.Ready {
		t.Fatalf("storage status not decoded: %#v", status.Storage)
	}
}

// The orchestrator talks to the current API. API v1 is frozen and new
// routes such as /api/v2/admin/backup do not exist there.
func TestManagementUsesAPIv2(t *testing.T) {
	source, err := os.ReadFile("management.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "/api/v1/") {
		t.Fatal("management.go still calls /api/v1")
	}
	for _, route := range []string{"/api/v2/status", "/api/v2/admin/backup", "/api/v2/admin/restore"} {
		if !strings.Contains(string(source), route) {
			t.Errorf("management.go does not call %s", route)
		}
	}
}

// The GenesisDB image runs as UID 10001 and only /app/data is writable for
// it. A volume mounted elsewhere is owned by root and GenesisDB fails to start.
func TestInstanceUsesImageDataDirectory(t *testing.T) {
	if genesisDataDir != "/app/data" {
		t.Fatalf("data directory = %q, want /app/data", genesisDataDir)
	}
}

func TestEnsureCertificates(t *testing.T) {
	dir := t.TempDir()
	if err := ensureCertificates(dir); err != nil {
		t.Fatal(err)
	}
	if !certificatesValid(dir+"/ca.pem", dir+"/server.pem") {
		t.Fatal("generated certificates are not valid")
	}
}

func TestAPIClientHasNoGlobalTimeout(t *testing.T) {
	dir := t.TempDir()
	if err := ensureCertificates(dir); err != nil {
		t.Fatal(err)
	}
	client, err := (&Orchestrator{certsDir: dir}).apiClient()
	if err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 0 {
		t.Fatalf("API client timeout = %s, want no global timeout", client.Timeout)
	}
}

func TestSiteSupportsHTTPForLocalDevelopmentClients(t *testing.T) {
	dir := t.TempDir()
	app := &Orchestrator{sitesDir: dir}
	if err := app.writeSite("orders"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "orders.caddy"))
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if !strings.Contains(config, "http://orders.genesisdb.local {\n\treverse_proxy genesisdb-local-orders:8080") {
		t.Fatalf("HTTP route does not proxy to the instance: %s", config)
	}
	if strings.Contains(config, "redir https://") {
		t.Fatalf("HTTP route unexpectedly redirects to HTTPS: %s", config)
	}
}
