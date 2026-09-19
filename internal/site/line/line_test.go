package line

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestHTMLTextLinesKeepsDivAndBreakContent(t *testing.T) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(`
		<div>소개<br>첫 번째 문장</div>
		<section><p>지원 자격 <strong>3년 이상</strong></p><ul><li>Go</li><li>Swift</li></ul></section>`))
	if err != nil {
		t.Fatal(err)
	}

	got := htmlTextLines(doc.Selection)
	want := "소개\n첫 번째 문장\n지원 자격 3년 이상\nGo\nSwift"
	if got != want {
		t.Fatalf("htmlTextLines() = %q, want %q", got, want)
	}
}
