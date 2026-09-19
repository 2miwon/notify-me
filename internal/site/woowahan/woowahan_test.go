package woowahan

import "testing"

func TestParseDeadline(t *testing.T) {
	got, ok := parseDeadline("2026-09-30")
	if !ok || got.Year() != 2026 || got.Month() != 9 || got.Day() != 30 {
		t.Fatalf("parseDeadline() = %v, %v", got, ok)
	}
}
