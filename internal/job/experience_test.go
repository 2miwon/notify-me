package job

import "testing"

func TestExtractMinYearsExperiencePrefersCareerRequirement(t *testing.T) {
	text := `하나의 서비스를 1년 이상 개발/운영해 본 분
경력이 5년 이상인 분`
	got := ExtractMinYearsExperience(text)
	if got == nil || *got != 5 {
		t.Fatalf("ExtractMinYearsExperience() = %v, want 5", got)
	}
}

func TestExtractMinYearsExperienceFallsBackToGenericRequirement(t *testing.T) {
	got := ExtractMinYearsExperience("백엔드 시스템을 3년 이상 개발해 본 분")
	if got == nil || *got != 3 {
		t.Fatalf("ExtractMinYearsExperience() = %v, want 3", got)
	}
}

func TestExtractMinYearsExperienceHandlesStructuredKoreanRange(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"경력 : 2년 ~ 10년", 2},
		{"관련 경력：5년 이상", 5},
	}
	for _, test := range tests {
		got := ExtractMinYearsExperience(test.text)
		if got == nil || *got != test.want {
			t.Errorf("ExtractMinYearsExperience(%q) = %v, want %d", test.text, got, test.want)
		}
	}
}

func TestExtractMinYearsExperienceEnglishVariants(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"At least 3 years of relevant experience", 3},
		{"3–5 years of experience building distributed systems", 3},
		{"5 years' professional experience", 5},
		{"Experience of 4 years in software engineering", 4},
	}
	for _, test := range tests {
		got := ExtractMinYearsExperience(test.text)
		if got == nil || *got != test.want {
			t.Errorf("ExtractMinYearsExperience(%q) = %v, want %d", test.text, got, test.want)
		}
	}
}
