package nhn

import "testing"

func TestDescriptionUsesStructuredSectionsWithoutDuplicates(t *testing.T) {
	got := description([]contentItem{
		{Title: "이런 일을 해요", Contents: []string{"<p>Java 백엔드 개발</p>", "Java 백엔드 개발", "<p>운영을 담당해요<br>장애를 대응해요</p>"}},
	})
	want := "이런 일을 해요\nJava 백엔드 개발\n운영을 담당해요\n장애를 대응해요"
	if got != want {
		t.Fatalf("description() = %q, want %q", got, want)
	}
}

func TestEmploymentAndDeadlineNormalization(t *testing.T) {
	if got := employmentType("정규"); got != "Full-time" {
		t.Fatalf("employmentType() = %q", got)
	}
	if deadline := parseDeadline("2999-12-31T23:59:00"); deadline != nil {
		t.Fatalf("permanent NHN deadline = %v, want nil", deadline)
	}
}
