package coupang

import "testing"

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

func TestMatchesDepartment(t *testing.T) {
	departments := []struct {
		Name string `json:"name"`
	}{{Name: "Search and Discovery"}}
	if !matchesDepartment(departments, []string{"Cloud Platform", "Search and Discovery"}) {
		t.Fatal("selected department must match")
	}
	if matchesDepartment(departments, []string{"Corporate IT"}) {
		t.Fatal("unselected department must not match")
	}
}
