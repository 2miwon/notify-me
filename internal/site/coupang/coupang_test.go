package coupang

import (
	"testing"

	"github.com/2miwon/notify-me/internal/job"
)

func TestHTMLToTextPreservesPostingLines(t *testing.T) {
	got := htmlToText("&lt;h2&gt;자격 요건&lt;/h2&gt;&lt;p&gt;학사 이상&lt;br&gt;3년 이상&lt;/p&gt;&lt;ul&gt;&lt;li&gt;Go 경험&lt;/li&gt;&lt;/ul&gt;")
	want := "자격 요건\n학사 이상\n3년 이상\nGo 경험"
	if got != want {
		t.Fatalf("htmlToText() = %q, want %q", got, want)
	}
}

func TestMatchesLocation(t *testing.T) {
	if !matchesLocation("Seoul, South Korea", []string{"Seoul, South Korea"}) {
		t.Fatal("Seoul must match")
	}
	if matchesLocation("Taipei, Taiwan", []string{"Seoul, South Korea"}) {
		t.Fatal("Taipei must not match the Seoul-only configuration")
	}
}

func TestTechDepartmentDoesNotAdmitNonEngineeringTitles(t *testing.T) {
	// Coupang files PMs, designers and analysts under "<Product> Tech".
	if job.LooksDev("Senior Product Manager (Rocket Growth)", "Rocket Growth Tech") {
		t.Fatal("PM under a Tech department must not count as a dev role")
	}
	if !job.LooksDev("Staff Back-end Engineer (Eats Customer)", "Eats Tech") {
		t.Fatal("engineer under a Tech department must count as a dev role")
	}
}
