package orchestrator

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	// The only API v1 call is the export fallback for 1.x instances, which
	// must stay exportable so their data can move to a 2.0 instance.
	if got := strings.Count(string(source), "/api/v1/"); got != 1 || !strings.Contains(string(source), `"/api/v1/backup/create"`) {
		t.Fatalf("management.go has %d API v1 references, want only the backup export fallback", got)
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

func answer(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

// An instance created before GenesisDB 2.0 runs a 1.x image that answers 404
// on API v2. The error must name the instance and explain how to recreate it
// instead of surfacing a bare HTTP 404.
func TestVersion1InstanceIsExplained(t *testing.T) {
	_, err := checkResponse("orders", "/api/v2/status", answer(http.StatusNotFound, "404 page not found"))
	if !errors.Is(err, errVersion1Instance) {
		t.Fatalf("404 on API v2 = %v, want errVersion1Instance", err)
	}
	for _, text := range []string{"orders", "GenesisDB 1.x", "backup"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("error %q does not mention %q", err, text)
		}
	}
}

func TestCheckResponseKeepsOtherErrors(t *testing.T) {
	if _, err := checkResponse("orders", "/api/v1/backup/create", answer(http.StatusNotFound, "")); errors.Is(err, errVersion1Instance) {
		t.Fatal("a 404 on API v1 is not a 1.x instance")
	}
	_, err := checkResponse("orders", "/api/v2/admin/restore", answer(http.StatusConflict, "store is not empty"))
	if err == nil || errors.Is(err, errVersion1Instance) || !strings.Contains(err.Error(), "409") || !strings.Contains(err.Error(), "store is not empty") {
		t.Fatalf("409 = %v", err)
	}
	body, err := checkResponse("orders", "/api/v2/status", answer(http.StatusOK, "{}"))
	if err != nil || body == nil {
		t.Fatalf("200 = %v", err)
	}
	body.Close()
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
