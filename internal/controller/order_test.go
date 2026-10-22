package controller

import (
	"errors"
	"testing"

	"example.com/learn-golang/internal/model"
)

func TestDecimalMinorUnits(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "25000", want: "25000.00"},
		{input: "12.3", want: "12.30"},
		{input: "0.05", want: "0.05"},
		{input: "999999999999999999999", want: "999999999999999999999.00"},
	}
	for _, test := range tests {
		minor, ok := decimalMinorUnits(test.input)
		if !ok {
			t.Fatalf("decimalMinorUnits(%q) rejected valid amount", test.input)
		}
		if got := minorUnitsDecimal(minor); got != test.want {
			t.Errorf("minorUnitsDecimal(decimalMinorUnits(%q)) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestDecimalMinorUnitsRejectsUnsupportedPrecision(t *testing.T) {
	for _, input := range []string{"1.234", "1e3", "-1", ""} {
		if _, ok := decimalMinorUnits(input); ok {
			t.Errorf("decimalMinorUnits(%q) accepted invalid amount", input)
		}
	}
}

func TestTrackingPhoneNormalization(t *testing.T) {
	tests := map[string]string{
		"081234567890":   "+6281234567890",
		"6281234567890":  "+6281234567890",
		"+6281234567890": "+6281234567890",
		"81234567890":    "+6281234567890",
	}
	for input, want := range tests {
		if got := normalizeTrackingPhone(input); got != want {
			t.Errorf("normalizeTrackingPhone(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTrackingLabelFallback(t *testing.T) {
	labels := map[string]string{"PAID": "Pembayaran terverifikasi"}
	if got := trackingLabel(labels, "PAID", "fallback"); got != "Pembayaran terverifikasi" {
		t.Fatalf("known status label = %q", got)
	}
	if got := trackingLabel(labels, "NEW_STATUS", "Status pembayaran diperbarui"); got != "Status pembayaran diperbarui" {
		t.Fatalf("unknown status label = %q", got)
	}
}

func TestProofReadFailureDistinguishesMissingObjectFromStorageOutage(t *testing.T) {
	missing := ToOrderError(proofReadFailure(model.ErrPrivateObjectNotFound))
	if missing.Status != 400 || missing.Code != "ORDB02003" {
		t.Fatalf("missing proof error = %d %s, want 400 ORDB02003", missing.Status, missing.Code)
	}
	outage := ToOrderError(proofReadFailure(errors.New("blob service unavailable")))
	if outage.Status != 503 || outage.Code != "ORDT04001" {
		t.Fatalf("storage outage error = %d %s, want 503 ORDT04001", outage.Status, outage.Code)
	}
	invalid := ToOrderError(proofReadFailure(model.ErrPrivateObjectInvalid))
	if invalid.Status != 400 || invalid.Code != "ORDB02003" {
		t.Fatalf("invalid proof error = %d %s, want 400 ORDB02003", invalid.Status, invalid.Code)
	}
}
