package orchestrator

import "testing"

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

func TestEnsureCertificates(t *testing.T) {
	dir := t.TempDir()
	if err := ensureCertificates(dir); err != nil {
		t.Fatal(err)
	}
	if !certificatesValid(dir+"/ca.pem", dir+"/server.pem") {
		t.Fatal("generated certificates are not valid")
	}
}
