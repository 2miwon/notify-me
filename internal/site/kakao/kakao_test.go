package kakao

import "testing"

func TestCanonicalEmploymentType(t *testing.T) {
	if got := canonicalEmploymentType("정규직"); got != "Full-time" {
		t.Fatalf("got %q", got)
	}
}

func TestDescriptionForUnescapesBreaks(t *testing.T) {
	got := descriptionFor(listItem{Introduction: "첫 줄&lt;br/&gt;둘째 줄"})
	if got != "첫 줄 둘째 줄" {
		t.Fatalf("descriptionFor() = %q", got)
	}
}
