package openai

import "testing"

func TestDepartmentAndEmploymentFilters(t *testing.T) {
	if !matches("Research", []string{"Research", "Scaling"}) {
		t.Fatal("selected department should match")
	}
	if matches("Legal", []string{"Research", "Scaling"}) {
		t.Fatal("unselected department should not match")
	}
	if got := employmentType("FullTime"); got != "Full-time" {
		t.Fatalf("employmentType() = %q", got)
	}
}
