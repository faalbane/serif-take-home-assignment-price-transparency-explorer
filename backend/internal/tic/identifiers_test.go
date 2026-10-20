package tic

import (
	"errors"
	"strings"
	"testing"
)

func TestNPIChecksumAgainstCMSExample(t *testing.T) {
	// 1234567893 is the worked example in the CMS NPI check-digit guidance.
	if !npiChecksumOK("1234567893") {
		t.Error("CMS published example 1234567893 should validate")
	}
	// Flipping the check digit must fail.
	if npiChecksumOK("1234567890") {
		t.Error("1234567890 has the wrong check digit and should not validate")
	}
}

func TestParseNPI(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantNorm   string
		wantValid  bool
		wantWarnIs string
	}{
		{"clean", "1396747861", "1396747861", true, ""},
		{"dashed", "1396-747-861", "1396747861", true, ""},
		{"spaced and padded", "  1396 747 861  ", "1396747861", true, ""},
		{"bad checksum", "1396747862", "1396747862", false, "check-digit"},
		{"nine digits from source data", "124507857", "124507857", false, "10 digits"},
		{"placeholder zero", "0", "0", false, "10 digits"},
		{"too long", "13967478611", "13967478611", false, "10 digits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNPI(tt.in)
			if err != nil {
				t.Fatalf("ParseNPI(%q) returned error %v; malformed NPIs must stay searchable", tt.in, err)
			}
			if got.Normalized != tt.wantNorm {
				t.Errorf("Normalized = %q, want %q", got.Normalized, tt.wantNorm)
			}
			if got.Valid != tt.wantValid {
				t.Errorf("Valid = %v, want %v (warnings: %v)", got.Valid, tt.wantValid, got.Warnings)
			}
			if tt.wantWarnIs != "" {
				if len(got.Warnings) == 0 {
					t.Fatalf("expected a warning containing %q, got none", tt.wantWarnIs)
				}
				if !strings.Contains(strings.Join(got.Warnings, " "), tt.wantWarnIs) {
					t.Errorf("warnings = %v, want one containing %q", got.Warnings, tt.wantWarnIs)
				}
			} else if len(got.Warnings) != 0 {
				t.Errorf("unexpected warnings: %v", got.Warnings)
			}
		})
	}
}

func TestParseEIN(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantNorm  string
		wantDisp  string
		wantValid bool
	}{
		{"dashed", "11-3471596", "113471596", "11-3471596", true},
		{"bare", "113471596", "113471596", "11-3471596", true},
		{"noisy", "EIN 11 3471596", "113471596", "11-3471596", true},
		{"too short", "1134715", "1134715", "1134715", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEIN(tt.in)
			if err != nil {
				t.Fatalf("ParseEIN(%q): %v", tt.in, err)
			}
			if got.Normalized != tt.wantNorm {
				t.Errorf("Normalized = %q, want %q", got.Normalized, tt.wantNorm)
			}
			if got.Display != tt.wantDisp {
				t.Errorf("Display = %q, want %q", got.Display, tt.wantDisp)
			}
			if got.Valid != tt.wantValid {
				t.Errorf("Valid = %v, want %v", got.Valid, tt.wantValid)
			}
		})
	}
}

func TestParseIdentifiersRejectDigitlessInput(t *testing.T) {
	for _, in := range []string{"", "   ", "abc", "--", "N/A"} {
		if _, err := ParseNPI(in); !errors.Is(err, ErrNoDigits) {
			t.Errorf("ParseNPI(%q) error = %v, want ErrNoDigits", in, err)
		}
		if _, err := ParseEIN(in); !errors.Is(err, ErrNoDigits) {
			t.Errorf("ParseEIN(%q) error = %v, want ErrNoDigits", in, err)
		}
	}
}

func TestFormatEINLeavesNonCanonicalUntouched(t *testing.T) {
	if got, want := FormatEIN("12345"), "12345"; got != want {
		t.Errorf("FormatEIN(%q) = %q, want %q", "12345", got, want)
	}
}
