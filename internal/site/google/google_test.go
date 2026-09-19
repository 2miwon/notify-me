package google

import "testing"

func TestClean(t *testing.T) {
	if got := clean(" A\n B "); got != "A B" {
		t.Fatal(got)
	}
}
