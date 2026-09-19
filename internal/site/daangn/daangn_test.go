package daangn

import "testing"

func TestMatchesDepartment(t *testing.T) {
	allowed := []string{"network", "software-engineer-backend"}
	if !matchesDepartment("software-engineer software-engineer-backend", allowed) {
		t.Fatal("selected backend department should match")
	}
	if matchesDepartment("software-engineer-ios", allowed) {
		t.Fatal("unselected iOS department should not match")
	}
}

func TestJobMetadata(t *testing.T) {
	if got := employmentType("intern"); got != "Internship" {
		t.Fatalf("employmentType() = %q", got)
	}
	if got := careerLevel("Software Engineer, Backend (경력)"); got != "경력" {
		t.Fatalf("careerLevel() = %q", got)
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText("<h3>이런 분을 찾아요</h3><ul><li>학사 이상</li><li>Go 경험</li></ul>")
	want := "이런 분을 찾아요\n학사 이상\nGo 경험"
	if got != want {
		t.Fatalf("htmlToText() = %q, want %q", got, want)
	}
}

func TestHTMLToTextDropsAdjacentDuplicateCMSBlocks(t *testing.T) {
	got := htmlToText("<h3>이런 일을 해요</h3><p>보안 이벤트를 분석하고 대응해요</p><li>보안 이벤트를 분석하고 대응해요</li><p>1\\. 서류 전형</p>")
	want := "이런 일을 해요\n보안 이벤트를 분석하고 대응해요\n1. 서류 전형"
	if got != want {
		t.Fatalf("htmlToText() = %q, want %q", got, want)
	}
}
