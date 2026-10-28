package cli

import "testing"

// validToken has the 32 bytes GenesisDB requires at minimum.
const validToken = "0123456789abcdef0123456789abcdef"

func TestParseCreateAnyOrder(t *testing.T) {
	name, token, err := parseCreate([]string{"--auth-token", validToken, "dev-1"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "dev-1" || token != validToken {
		t.Fatalf("unexpected values: %q %q", name, token)
	}
}

func TestCreateWithAllArgumentsSkipsWizard(t *testing.T) {
	name, token, err := parseCreate([]string{"dev", "--auth-token", validToken})
	if err != nil {
		t.Fatal(err)
	}
	name, token, err = completeCreateWizard(name, token)
	if err != nil {
		t.Fatal(err)
	}
	if name != "dev" || token != validToken {
		t.Fatalf("unexpected values: %q %q", name, token)
	}
}

func TestParseCreateAllowsWizardArguments(t *testing.T) {
	cases := [][]string{
		{},
		{"dev"},
		{"dev", "--auth-token", validToken},
	}
	for _, args := range cases {
		if _, _, err := parseCreate(args); err != nil {
			t.Errorf("expected wizard-compatible arguments %#v to parse: %v", args, err)
		}
	}
}

func TestParseCreateRejectsMalformedArguments(t *testing.T) {
	cases := [][]string{
		{"dev", "--auth-token"},
		{"dev", "--auth-token", "too-short"},
		{"dev", "--license-key", "license"},
		{"dev", "--unknown", "value", "--auth-token", validToken},
	}
	for _, args := range cases {
		if _, _, err := parseCreate(args); err == nil {
			t.Errorf("expected error for %#v", args)
		}
	}
}
