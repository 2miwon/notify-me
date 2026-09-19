package kakaobank

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestParseKoreaTime(t *testing.T) {
	got, err := parseKoreaTime("2026-09-18 23:59:59")
	if err != nil {
		t.Fatal(err)
	}
	if got.Location() != koreaLocation || got.Hour() != 23 || got.Day() != 18 {
		t.Fatalf("parseKoreaTime() = %s (%s)", got, got.Location())
	}
	if got.UTC().Hour() != 14 {
		t.Fatalf("UTC hour = %d, want 14", got.UTC().Hour())
	}
}

func TestDescriptionTextPreservesLineBreaks(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
        <div><h2>담당 업무</h2><p>첫 번째 업무<br>두 번째 업무</p>
        <ul><li>자격 요건 A</li><li>자격 요건 B</li></ul></div>`))
	if err != nil {
		t.Fatal(err)
	}
	want := "담당 업무\n첫 번째 업무\n두 번째 업무\n자격 요건 A\n자격 요건 B"
	if got := descriptionText(doc.Selection); got != want {
		t.Errorf("descriptionText() = %q, want %q", got, want)
	}
}

func TestCanonicalEmploymentType(t *testing.T) {
	tests := []struct{ raw, title, want string }{
		{"정규직", "백엔드 개발자", "Full-time"},
		{"", "AI 검색 엔지니어 (계약직)", "Contract"},
		{"", "체험형 인턴", "Internship"},
	}
	for _, test := range tests {
		if got := canonicalEmploymentType(test.raw, test.title); got != test.want {
			t.Errorf("canonicalEmploymentType(%q, %q) = %q, want %q", test.raw, test.title, got, test.want)
		}
	}
}
