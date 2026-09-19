package config

import "testing"

func TestMatchesLocationIsFailClosedWhenConfigured(t *testing.T) {
	cfg := Config{Locations: []string{"서울", "Seoul"}}
	for _, location := range []string{"서울특별시", "Seoul, South Korea"} {
		if !cfg.MatchesLocation(location) {
			t.Errorf("MatchesLocation(%q) = false, want true", location)
		}
	}
	for _, location := range []string{"", "Pangyo, South Korea", "San Francisco, CA"} {
		if cfg.MatchesLocation(location) {
			t.Errorf("MatchesLocation(%q) = true, want false", location)
		}
	}
}
