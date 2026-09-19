package yonsei

import (
	"os"
	"testing"
)

// This deliberately runs only when explicitly requested. It validates the
// real SSO session flow without ever printing credentials or posting data.
func TestFetchRecommendedRecruitments(t *testing.T) {
	if os.Getenv("RUN_YONSEI_INTEGRATION") != "1" {
		t.Skip("set RUN_YONSEI_INTEGRATION=1 to run against Career Yonsei")
	}
	postings, err := New().Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(postings) == 0 {
		t.Fatal("no recommended-recruitment postings returned")
	}
}
