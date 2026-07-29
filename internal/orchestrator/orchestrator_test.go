package orchestrator

import (
	"encoding/json"
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

func TestStatusJSON(t *testing.T) {
	payload := []byte(`{
		"engine":{"version":"1.2.3","edition":"enterprise","channel":"stable"},
		"system":{"os":"linux","arch":"arm64","cpu":{"availableCores":8,"usedCores":2},"memory":{"total":100,"used":40,"available":60},"storage":{"max":1000,"used":300,"available":700}},
		"license":{"status":"valid","validUntil":"2027-01-01"},
		"events":{"count":42,"subjects":7,"types":3,"storageSize":2048}
	}`)
	var status Status
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatal(err)
	}
	if status.Engine.Version != "1.2.3" || status.Events.Count != 42 || status.System.Storage.Max != 1000 {
		t.Fatalf("unexpected status: %#v", status)
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
