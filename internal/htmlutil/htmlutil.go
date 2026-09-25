// Package htmlutil holds small HTML-to-text helpers shared by site
// adapters that get their description fields back as HTML fragments
// rather than plain text. Kept separate from internal/job so that
// package can stay dependency-free.
package htmlutil

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// StripToText converts an HTML fragment (as several sites return their
// description fields — mainly plain text with "<br/>" between
// paragraphs, sometimes a stray link) to plain text, turning each <br>
// into a real line break first so paragraphs don't get glued into one
// run-on line the way a plain .Text() call would leave them.
func StripToText(htmlFragment string) string {
	if htmlFragment == "" {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlFragment))
	if err != nil {
		return ""
	}
	// Some sites (Netmarble) return a whole standalone HTML document,
	// whose <style>/<title> text would otherwise leak into the result.
	doc.Find("style, script, title").Remove()
	doc.Find("br").ReplaceWithHtml("\n")

	var lines []string
	for _, line := range strings.Split(doc.Text(), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
