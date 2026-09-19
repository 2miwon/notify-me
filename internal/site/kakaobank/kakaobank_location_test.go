package kakaobank

import "testing"

func TestExtractLocation(t *testing.T) {
	got := extractLocation("근무 관련 정보\n· 근무지 : 카카오뱅크 판교오피스 (경기도 성남시 분당구)")
	if got != "카카오뱅크 판교오피스 (경기도 성남시 분당구)" {
		t.Fatalf("extractLocation() = %q", got)
	}
}
