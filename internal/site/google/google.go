// Package google implements site.Adapter for the public Google Careers search.
package google

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
	"github.com/PuerkitoBio/goquery"
)

const listURL = "https://www.google.com/about/careers/applications/jobs/results?location=South%20Korea&target_level=EARLY&target_level=INTERN_AND_APPRENTICE&degree=BACHELORS"

type Adapter struct{ client *http.Client }

func New() *Adapter             { return &Adapter{client: &http.Client{Timeout: 30 * time.Second}} }
func (a *Adapter) Name() string { return "google" }

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	doc, err := a.get(listURL)
	if err != nil {
		return nil, err
	}
	var postings []job.Posting
	doc.Find("ul.spHGqe > li.lLd3Je").Each(func(_ int, card *goquery.Selection) {
		title := clean(card.Find("h3.QJPWVe").First().Text())
		href, ok := card.Find("a[href*='jobs/results/']").Last().Attr("href")
		if !ok || title == "" {
			return
		}
		u, err := url.Parse(href)
		if err != nil {
			return
		}
		if !u.IsAbs() {
			u, _ = url.Parse("https://www.google.com/about/careers/applications/" + strings.TrimPrefix(href, "./"))
		}
		location := clean(card.Find(".r0wTof").First().Text())
		// The list card's own .Xsxa1e only ever holds "Minimum
		// qualifications" — "About the job", "Responsibilities" and
		// "Preferred qualifications" only exist on the detail page, so
		// using the card's snippet as the full description silently
		// truncated every posting to just its minimum-quals bullets.
		description := clean(card.Find(".Xsxa1e").Text())
		externalID := card.AttrOr("ssk", "")
		if _, id, ok := strings.Cut(externalID, ":"); ok {
			externalID = id
		}

		p := job.Posting{Site: a.Name(), ExternalID: externalID, Title: title, Company: "Google", URL: u.String(), Location: location, CareerLevel: "신입", PostedAt: time.Now().UTC(), Description: description}
		// A failed detail fetch is a lesser failure than losing the
		// posting entirely — keep it with just the list card's snippet.
		if full, err := a.fetchDescription(u.String()); err == nil && full != "" {
			p.Description = full
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	})
	if len(postings) == 0 {
		return nil, fmt.Errorf("google: no job cards found")
	}
	if total := matchedCount(doc); total > len(postings) {
		return nil, fmt.Errorf("google: fetched only %d of %d matched jobs; refusing an incomplete refresh", len(postings), total)
	}
	return postings, nil
}

// fetchDescription reads the full posting body off the detail page —
// qualifications (both tiers), "About the job", and "Responsibilities" —
// none of which the list page's own card exposes in full.
func (a *Adapter) fetchDescription(detailURL string) (string, error) {
	doc, err := a.get(detailURL)
	if err != nil {
		return "", err
	}

	var parts []string
	doc.Find("h3:contains('qualifications')").Each(func(_ int, h3 *goquery.Selection) {
		heading := clean(h3.Text())
		list := clean(h3.NextFiltered("ul").Text())
		if heading != "" || list != "" {
			parts = append(parts, strings.TrimSpace(heading+"\n"+list))
		}
	})
	appendSection := func(sel *goquery.Selection) {
		heading := clean(sel.Find("h3").First().Text())
		body := clean(sel.Clone().Find("h3").Remove().End().Text())
		if heading != "" || body != "" {
			parts = append(parts, strings.TrimSpace(heading+"\n"+body))
		}
	}
	appendSection(doc.Find("div.aG5W3").First())
	appendSection(doc.Find("div.BDNOWe").First())
	return strings.Join(parts, "\n\n"), nil
}

func (a *Adapter) get(target string) (*goquery.Document, error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; notify-me-crawler/1.0)")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return goquery.NewDocumentFromReader(resp.Body)
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func matchedCount(doc *goquery.Document) int {
	text := clean(doc.Find(".SWhIm").First().Text())
	n, err := strconv.Atoi(strings.ReplaceAll(text, ",", ""))
	if err != nil {
		return 0
	}
	return n
}
