// Package apple implements site.Adapter for jobs.apple.com.
//
// The listing page is genuinely static server-rendered HTML — a plain
// net/http GET returns the full job list, no JS needed, so this scrapes
// it with goquery like the project's original plan assumed.
//
// The detail page is also static HTML, but its actual content isn't in
// the rendered markup at all: it's a JSON blob assigned to
// `window.__staticRouterHydrationData` via `JSON.parse("...")`, where the
// argument is itself a JSON-escaped string (so the page's JS can embed a
// JSON document inside a JS string literal without the browser trying to
// parse it twice on its own) — meaning the real data has to be JSON-
// decoded twice: once to un-escape the outer string, once to parse the
// JSON object that string actually contains. See fetchDescription.
package apple

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL    = "https://jobs.apple.com"
	userAgent  = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	dateLayout = "Jan 2, 2006"
)

// Adapter crawls jobs.apple.com for one location code. Find the code by
// picking a location filter on https://jobs.apple.com/en-us/search and
// reading it back out of the URL's `location` param (e.g.
// "south-korea-KOR").
type Adapter struct {
	LocationCode string
	// MaxPages caps how many 20-result pages are fetched per run, so a
	// mis-set filter can't turn one crawl into an unbounded scrape.
	MaxPages int

	client *http.Client
}

func New(locationCode string, maxPages int) *Adapter {
	return &Adapter{
		LocationCode: locationCode,
		MaxPages:     maxPages,
		client:       &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return "apple" }

type listing struct {
	externalID string
	title      string
	url        string
	team       string
	location   string
	postedAt   *time.Time
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.LocationCode == "" {
		return nil, fmt.Errorf("apple: no location code configured")
	}

	var postings []job.Posting

	for page := 1; a.MaxPages <= 0 || page <= a.MaxPages; page++ {
		listings, err := a.fetchListPage(page)
		if err != nil {
			return postings, fmt.Errorf("apple: list page %d: %w", page, err)
		}
		if len(listings) == 0 {
			break
		}

		for _, l := range listings {
			p := job.Posting{
				Site:             a.Name(),
				ExternalID:       l.externalID,
				Title:            l.title,
				Company:          "Apple",
				URL:              l.url,
				Location:         l.location,
				ApplicationStart: l.postedAt,
				PostedAt:         time.Now().UTC(),
			}

			// A failed detail fetch is a lesser failure than losing the
			// posting entirely — keep it with just the list-page fields.
			if data, err := a.fetchJobsData(l.url); err == nil {
				p.Description = combineDescription(data)
				if t, err := time.Parse(time.RFC3339, data.PostDateInGMT); err == nil {
					p.ApplicationStart = &t
				}
			}

			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}
	}

	return postings, nil
}

func (a *Adapter) fetchListPage(page int) ([]listing, error) {
	// Keep the supplied engineering sub-teams; using only the location made
	// Apple return unrelated sales, editorial, and business roles.
	team := "SFTWR-ISTECH+OPMFG-SE+SFTWR-CLD+SFTWR-COS+SFTWR-MCHLN+SFTWR-EPM+SFTWR-DSR+SFTWR-GPUG+SFTWR-AF+SFTWR-SEC+SFTWR-UEE+SFTWR-TS+SFTWR-SQAT+SFTWR-WSFT+SFTWR-CP+SFTWR-VMAT+SFTWR-SC"
	url := fmt.Sprintf("%s/en-us/search?location=%s&team=%s&page=%d", baseURL, a.LocationCode, team, page)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse list page: %w", err)
	}

	var listings []listing
	doc.Find("div.job-title.job-list-item").Each(func(_ int, s *goquery.Selection) {
		link := s.Find("h3 a").First()
		href, _ := link.Attr("href")
		if href == "" {
			return
		}

		title := strings.TrimSpace(link.Text())
		team := strings.TrimSpace(s.Find(".team-name").First().Text())
		postedText := strings.TrimSpace(s.Find(".job-posted-date").First().Text())

		// Location lives in a sibling element, not inside this .job-title
		// div itself.
		location := strings.TrimSpace(s.Parent().Find(".job-title-location span").Last().Text())

		l := listing{
			externalID: externalIDFromHref(href),
			title:      title,
			url:        baseURL + href,
			team:       team,
			location:   location,
		}
		if t, err := time.Parse(dateLayout, postedText); err == nil {
			l.postedAt = &t
		}
		listings = append(listings, l)
	})

	return listings, nil
}

// externalIDFromHref pulls the numeric-ish job ID out of a detail path
// like "/en-us/details/200682849-3631/business-affairs-...".
func externalIDFromHref(href string) string {
	parts := strings.Split(strings.Trim(href, "/"), "/")
	for i, part := range parts {
		if part == "details" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return href
}

type jobsData struct {
	JobNumber               string `json:"jobNumber"`
	PostingTitle            string `json:"postingTitle"`
	JobSummary              string `json:"jobSummary"`
	Description             string `json:"description"`
	Responsibilities        string `json:"responsibilities"`
	MinimumQualifications   string `json:"minimumQualifications"`
	PreferredQualifications string `json:"preferredQualifications"`
	PostDateInGMT           string `json:"postDateInGMT"`
}

func combineDescription(d *jobsData) string {
	var parts []string
	for _, s := range []string{d.JobSummary, d.Description, d.Responsibilities, d.MinimumQualifications, d.PreferredQualifications} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

// fetchJobsData fetches a posting's detail page and extracts its
// "jobsData" object out of the double-JSON-encoded
// window.__staticRouterHydrationData blob (see package doc).
func (a *Adapter) fetchJobsData(detailURL string) (*jobsData, error) {
	req, err := http.NewRequest(http.MethodGet, detailURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	html := string(body)

	const marker = "window.__staticRouterHydrationData = JSON.parse("
	idx := strings.Index(html, marker)
	if idx == -1 {
		return nil, fmt.Errorf("hydration data not found")
	}
	quoteStart := idx + len(marker)
	quoted, err := extractQuotedJSString(html, quoteStart)
	if err != nil {
		return nil, fmt.Errorf("extract quoted string: %w", err)
	}

	var innerJSON string
	if err := json.Unmarshal([]byte(quoted), &innerJSON); err != nil {
		return nil, fmt.Errorf("unescape outer string: %w", err)
	}

	objStart, ok := findJSONFieldObjectStart(innerJSON, "jobsData")
	if !ok {
		return nil, fmt.Errorf("jobsData field not found")
	}
	objJSON, err := extractBalancedJSON(innerJSON, objStart)
	if err != nil {
		return nil, fmt.Errorf("extract jobsData object: %w", err)
	}

	var data jobsData
	if err := json.Unmarshal([]byte(objJSON), &data); err != nil {
		return nil, fmt.Errorf("decode jobsData: %w", err)
	}
	return &data, nil
}

// extractQuotedJSString returns the JSON string literal starting at
// startIdx (which must point at the opening '"'), including both quotes,
// found by scanning for the first unescaped closing quote.
func extractQuotedJSString(s string, startIdx int) (string, error) {
	if startIdx >= len(s) || s[startIdx] != '"' {
		return "", fmt.Errorf("expected opening quote at %d", startIdx)
	}
	for i := startIdx + 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++ // skip the escaped character
		case '"':
			return s[startIdx : i+1], nil
		}
	}
	return "", fmt.Errorf("unterminated string")
}

// findJSONFieldObjectStart finds `"<key>":` in s and returns the index of
// the '{' that follows it (skipping whitespace).
func findJSONFieldObjectStart(s, key string) (int, bool) {
	marker := strconv.Quote(key) + ":"
	idx := strings.Index(s, marker)
	if idx == -1 {
		return 0, false
	}
	i := idx + len(marker)
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return 0, false
	}
	return i, true
}

// extractBalancedJSON returns the JSON object starting at start (which
// must point at '{'), respecting string literals so a brace inside a
// quoted value doesn't throw off the depth count.
func extractBalancedJSON(s string, start int) (string, error) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced JSON starting at %d", start)
}
