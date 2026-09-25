package job

import "testing"

func TestIsDevTitle(t *testing.T) {
	for _, title := range []string{"Senior Backend Engineer", "Android Developer", "서버 개발자", "게임 보안 개발자 모집", "Applied Scientist II", "DBA (MySQL)", "Tech Lead (Frontend)", "Security Researcher (모바일보안)", "백엔드 개발 (3년 이상)", "ML Data Platform"} {
		if !IsDevTitle(title) {
			t.Errorf("IsDevTitle(%q) = false, want true", title)
		}
	}
	for _, title := range []string{"Account Executive", "브랜드 마케터", "사업개발 담당자", "Product Manager", "Business Development Manager", "HTML Publisher", "UX Researcher"} {
		if IsDevTitle(title) {
			t.Errorf("IsDevTitle(%q) = true, want false", title)
		}
	}
}

func TestIsDevCategory(t *testing.T) {
	for _, c := range []string{"Engineering", "개발", "기술/AI", "AI/ML", "Data Engineering", "Game Programming", "인프라", "Tech", "Tech Platform"} {
		if !IsDevCategory(c) {
			t.Errorf("IsDevCategory(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"Retail", "Marketing", "사업개발/운영", "Sales", "마케팅", ""} {
		if IsDevCategory(c) {
			t.Errorf("IsDevCategory(%q) = true, want false", c)
		}
	}
}

func TestLooksDev(t *testing.T) {
	cases := []struct {
		title    string
		category string
		want     bool
	}{
		{"Backend Engineer", "Marketing", true},
		{"보안기술 담당자 모집", "기술/AI", true},
		{"Security Engineer", "정보보안", true},
		{"정보보안·컴플라이언스 담당자", "정보보안", false},
		{"개인정보 관리체계 /보안 정책 담당자 모집", "기술/AI", false},
		{"Data Analyst (Azar)", "Engineering", false},
		{"개발 3본부 PM 담당 모집", "개발지원", false},
		{"[단기계약직] AI번역 영어 및 베트남어 담당자 모집", "Language AI Research", false},
		{"[AI Transformation Dept.] Sr. AI Transformation Specialist (FDE)", "", true},
		{"[AI Research Div.] '26, '27년 전문연구요원 신규편입", "", true},
		{"Global UX Researcher", "", false},
		{"서버 개발 (3년 이상)", "", true},
		{"[프로젝트 SC] Technical Artist 모집", "Technical Art", false},
		{"[단기계약직] AI 교육사업·국가 R&D 과제 운영 담당자 모집", "", false},
		{"Backend Developer", "Tech", true},
	}
	for _, c := range cases {
		if got := LooksDev(c.title, c.category); got != c.want {
			t.Errorf("LooksDev(%q, %q) = %v, want %v", c.title, c.category, got, c.want)
		}
	}
}

func TestTitleTags(t *testing.T) {
	cases := []struct{ title, emp, career string }{
		{"[AI Research Div.] Research Engineer - Foundation Models (2년 이상 / 계약직)", "Contract", "경력"},
		{"[AI Research Div.] Research Scientist Intern - 독자 AI 파운데이션 모델 (2년 이상 / 인턴)", "Internship", "경력"},
		{"[AI Research Div.] '26, '27년 전문연구요원 신규편입 (경력무관)", "", "무관"},
		{"[Infra Div.] IT Engineer (경력 무관 / 계약직)", "Contract", "무관"},
		{"[Finance Div.][Legal Dept.] Commercial Counsel (5~10년)", "", "경력"},
		{"토스뱅크 Product Designer (신입, 2년 이하) (~9/30)", "", "신입"},
		{"Server Developer (3년 이하)", "", ""},
		{"Senior Backend Engineer", "", ""},
		{"Internal Tools Engineer", "", ""},
		{"Smart Contracts Engineer, International", "", ""},
		{"QA Engineer (1-Year Contract)", "Contract", ""},
	}
	for _, c := range cases {
		if got := EmploymentTypeFromTitle(c.title); got != c.emp {
			t.Errorf("EmploymentTypeFromTitle(%q) = %q, want %q", c.title, got, c.emp)
		}
		if got := CareerLevelFromTitle(c.title); got != c.career {
			t.Errorf("CareerLevelFromTitle(%q) = %q, want %q", c.title, got, c.career)
		}
	}
}

func TestCanonicalKoreanEmploymentType(t *testing.T) {
	for raw, want := range map[string]string{
		"정규직": "Full-time", "무기계약직": "Full-time", "단기계약직": "Contract", "Regular": "Full-time",
		"Professional Contractor": "Contract", "Contractor": "Contract", "Internship": "Internship",
		"Permanent": "Full-time", "Full-time": "Full-time", "": "",
	} {
		if got := CanonicalKoreanEmploymentType(raw); got != want {
			t.Errorf("CanonicalKoreanEmploymentType(%q) = %q, want %q", raw, got, want)
		}
	}
}
