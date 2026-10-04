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

func TestExtractMinYearsExperiencePrefersLeadingRequirement(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"자격요건\n7년 이상의 백엔드 개발 경험 및 컴퓨터공학 전공\n1년 이상의 커머스 또는 결제·정산(핀테크) 도메인 경력이 있는 분", 7},
		{"13년 이상의 백엔드 개발 경험이 있는 분\n2년 이상의 소규모 엔지니어링 팀 매니징 경험이 있는 분", 13},
		{"3년 이상 10년 이하의 정보보안 또는 IT 인프라 업무 경험이 있는 분", 3},
	}
	for _, test := range tests {
		got := ExtractMinYearsExperience(test.text)
		if got == nil || *got != test.want {
			t.Errorf("ExtractMinYearsExperience(%q) = %v, want %d", test.text, got, test.want)
		}
	}
}

func TestExtractMinYearsExperienceCoupangPhrasing(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"소프트웨어 개발 경험 10년 이상\n매니저 관리를 포함한 관리 경험 2년 이상 (멘토, 테크 리드 경험 포함)", 10},
		{"10+ years of professional backend development experience\n7+ years of experience in Java/Kotlin/Spring-based service development", 10},
		{"대규모 엔터프라이즈 네트워크 설계, 기획, 구축 및 운영 관련 경력 최소 10년 이상 보유하신 분\n해당 직무 경력 15년 이상이신 분", 10},
		{"10–15+ years of experience in cybersecurity\n5+ years in a leadership role", 10},
	}
	for _, test := range tests {
		got := ExtractMinYearsExperience(test.text)
		if got == nil || *got != test.want {
			t.Errorf("ExtractMinYearsExperience(%q) = %v, want %d", test.text, got, test.want)
		}
	}
}
