package nhn

import "testing"

func TestExtractLocation(t *testing.T) {
	got := extractLocation("근무 기간 : 2026년 10월 12일 ~ 2027년 1월 8일 근무 장소 : NHN 플레이뮤지엄 본사 (판교 삼평동 629)")
	if got != "NHN 플레이뮤지엄 본사 (판교 삼평동 629)" {
		t.Fatalf("extractLocation() = %q", got)
	}
}
