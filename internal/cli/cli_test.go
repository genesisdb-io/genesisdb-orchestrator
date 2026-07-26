package cli

import "testing"

func TestParseCreateAnyOrder(t *testing.T) {
	name, token, license, err := parseCreate([]string{"--license-key", "license", "dev-1", "--auth-token", "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "dev-1" || token != "secret" || license != "license" {
		t.Fatalf("unexpected values: %q %q %q", name, token, license)
	}
}

func TestCreateAllowsEmptyLicense(t *testing.T) {
	name, token, license, err := parseCreate([]string{"dev", "--auth-token", "secret", "--license-key", ""})
	if err != nil {
		t.Fatal(err)
	}
	name, token, license, err = completeCreateWizard(name, token, license)
	if err != nil {
		t.Fatal(err)
	}
	if name != "dev" || token != "secret" || license != "" {
		t.Fatalf("unexpected values: %q %q %q", name, token, license)
	}
}

func TestParseCreateAllowsWizardArguments(t *testing.T) {
	cases := [][]string{
		{},
		{"dev", "--auth-token", "secret"},
		{"dev", "--license-key", "license"},
	}
	for _, args := range cases {
		if _, _, _, err := parseCreate(args); err != nil {
			t.Errorf("expected wizard-compatible arguments %#v to parse: %v", args, err)
		}
	}
}

func TestParseCreateRejectsMalformedArguments(t *testing.T) {
	cases := [][]string{
		{"dev", "--auth-token"},
		{"dev", "--license-key"},
		{"dev", "--unknown", "value", "--auth-token", "secret", "--license-key", "license"},
	}
	for _, args := range cases {
		if _, _, _, err := parseCreate(args); err == nil {
			t.Errorf("expected error for %#v", args)
		}
	}
}
