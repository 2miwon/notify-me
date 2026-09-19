// Package line implements site.Adapter for careers.linecorp.com.
//
// The listing page is a Gatsby (React static-site-generator) app. Gatsby
// bakes each page's GraphQL query result into a static JSON file at
// build time — for the jobs listing that's:
//
//	GET https://careers.linecorp.com/page-data/ko/jobs/page-data.json
//
// This file contains every job in one shot (381 as of writing) — the
// listing page's own ci/co/fi/ca query-string filters are applied
// purely client-side in the browser's JS after this loads, not sent to
// any server, so fetching it with no query string at all still returns
// the complete unfiltered set. This adapter does its own filtering
// (by job field, city, region) in Go instead.
//
// A posting's detail page is the same pattern, one JSON per job:
//
//	GET https://careers.linecorp.com/page-data/ko/jobs/<strapiId>/page-data.json
//
// and — unlike naver — its `content` field is already the full posting
// body as HTML, no separate HTML page needs scraping; this just strips
// tags with goquery.
package line

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL      = "https://careers.linecorp.com/page-data/ko/jobs/page-data.json"
	detailURLFmt = "https://careers.linecorp.com/page-data/ko/jobs/%d/page-data.json"
	jobURLFmt    = "https://careers.linecorp.com/ko/jobs/%d/"
	userAgent    = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	dateLayout   = "2006-01-02T15:04:05.000Z"
)

// Adapter crawls careers.linecorp.com, filtered by job field, city, and
// region — all three are OR'd within themselves and AND'd across each
// other (matches the site's own ci/co/fi filter semantics). An empty
// slice for any of the three means "don't filter on this dimension".
// Find valid values by ticking filters on
// https://careers.linecorp.com/ko/jobs and reading them back out of the
// URL's ci/co/fi params (spaces in a multi-word value, e.g. "East Asia",
// come through URL-decoded already).
type Adapter struct {
	JobFields []string
	Cities    []string
	Regions   []string

	client *http.Client
}

func New(jobFields, cities, regions []string) *Adapter {
	return &Adapter{
		JobFields: jobFields,
		Cities:    cities,
		Regions:   regions,
		client:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return "line" }

type namedItem struct {
	Name string `json:"name"`
}

type lineJob struct {
	Publish   bool   `json:"publish"`
	StrapiID  int    `json:"strapiId"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	// UntilFilled marks a rolling posting with no real deadline; end_date
	// is then a sentinel far-future date (seen: 2999-12-31), not a date
	// worth surfacing as a deadline.
	UntilFilled    bool        `json:"until_filled"`
	Title          string      `json:"title"`
	IsPublic       bool        `json:"is_public"`
	EmploymentType []namedItem `json:"employment_type"`
	JobFields      []namedItem `json:"job_fields"`
	Companies      []namedItem `json:"companies"`
	Cities         []namedItem `json:"cities"`
	Regions        []namedItem `json:"regions"`
}

type listResponse struct {
	Result struct {
		Data struct {
			AllStrapiJobs struct {
				TotalCount int `json:"totalCount"`
				Edges      []struct {
					Node lineJob `json:"node"`
				} `json:"edges"`
			} `json:"allStrapiJobs"`
		} `json:"data"`
	} `json:"result"`
}

type detailResponse struct {
	Result struct {
		Data struct {
			StrapiJobs struct {
				Content string `json:"content"`
			} `json:"strapiJobs"`
		} `json:"data"`
	} `json:"result"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	resp, err := a.fetchAll()
	if err != nil {
		return nil, fmt.Errorf("line: %w", err)
	}

	var postings []job.Posting
	for _, edge := range resp.Result.Data.AllStrapiJobs.Edges {
		j := edge.Node
		if !j.Publish || !j.IsPublic {
			continue
		}
		if !matchesAny(names(j.JobFields), a.JobFields) {
			continue
		}
		if !matchesAny(names(j.Cities), a.Cities) {
			continue
		}
		if !matchesAny(names(j.Regions), a.Regions) {
			continue
		}

		employmentType, careerLevel := canonicalEmploymentType(names(j.EmploymentType))
		p := job.Posting{
			Site:           a.Name(),
			ExternalID:     strconv.Itoa(j.StrapiID),
			Title:          strings.TrimSpace(j.Title),
			Company:        strings.Join(names(j.Companies), ", "),
			URL:            fmt.Sprintf(jobURLFmt, j.StrapiID),
			Location:       strings.Join(names(j.Cities), ", "),
			EmploymentType: employmentType,
			CareerLevel:    careerLevel,
			PostedAt:       time.Now().UTC(),
		}
		if t, err := time.Parse(dateLayout, j.StartDate); err == nil {
			p.ApplicationStart = &t
		}
		if !j.UntilFilled {
			if t, err := time.Parse(dateLayout, j.EndDate); err == nil {
				p.ApplicationDeadline = &t
			}
		}

		desc, err := a.fetchDescription(j.StrapiID)
		if err != nil {
			// A missing description is a lesser failure than losing the
			// posting entirely — keep it, just without the body.
			desc = ""
		}
		p.Description = desc
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)

		postings = append(postings, p)
	}

	return postings, nil
}

func (a *Adapter) fetchAll() (*listResponse, error) {
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

// fetchDescription reads a job's own page-data.json for its `content`
// field — already the full posting body as HTML, unlike naver where the
// detail page has to be scraped. goquery just strips the tags here.
func (a *Adapter) fetchDescription(strapiID int) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf(detailURLFmt, strapiID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var out detailResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	content := out.Result.Data.StrapiJobs.Content
	if content == "" {
		return "", nil
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("parse content html: %w", err)
	}

	return htmlTextLines(doc.Selection), nil
}

// htmlTextLines preserves text from every HTML container LINE uses (not just
// p/li). Some postings put their body in divs separated by br tags; the former
// selector silently skipped those sections. Walking text nodes also avoids
// duplicating text from nested div/p/li elements.
func htmlTextLines(selection *goquery.Selection) string {
	var lines []string
	var current strings.Builder
	flush := func() {
		if text := normalizeWhitespace(current.String()); text != "" {
			lines = append(lines, text)
		}
		current.Reset()
	}
	appendText := func(text string) {
		text = normalizeWhitespace(text)
		if text == "" {
			return
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(text)
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			appendText(node.Data)
			return
		}
		if node.Type != html.ElementNode && node.Type != html.DocumentNode {
			return
		}
		name := strings.ToLower(node.Data)
		if name == "script" || name == "style" || name == "template" {
			return
		}
		if name == "br" {
			flush()
			return
		}
		if isBlockElement(name) {
			flush()
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if isBlockElement(name) {
			flush()
		}
	}
	selection.Each(func(_ int, s *goquery.Selection) {
		for _, node := range s.Nodes {
			walk(node)
		}
	})
	flush()
	return strings.Join(lines, "\n")
}

func isBlockElement(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "div", "dl", "fieldset",
		"figcaption", "figure", "footer", "form", "header", "h1", "h2", "h3",
		"h4", "h5", "h6", "li", "main", "nav", "ol", "p", "pre", "section",
		"table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func names(items []namedItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Name
	}
	return out
}

// canonicalEmploymentType splits LINE's own employment_type labels into
// job.Posting's separate EmploymentType/CareerLevel fields. LINE's
// vocabulary ("Full-time", "Contract", "Temporary") is the project's
// canonical set for EmploymentType — see the matching comment in
// internal/site/naver/naver.go, which maps its own 정규/계약/인턴 onto
// these same three values instead of the other way around — so this
// mostly passes values through unchanged. The one thing worth unpacking:
// "Full-time (Entry level)" folds a career-level distinction into the
// employment-type label instead of reporting it separately the way naver
// does (모집 경력: 신입/경력/무관), so this pulls it out into CareerLevel
// using naver's own "신입" label rather than inventing an English one —
// CareerLevel wasn't part of the "Full-time" complaint, so no reason to
// fragment it too.
func canonicalEmploymentType(rawNames []string) (employmentType, careerLevel string) {
	if len(rawNames) == 0 {
		return "", ""
	}
	raw := rawNames[0]
	if strings.Contains(raw, "Entry level") {
		return "Full-time", "신입"
	}
	return raw, ""
}

// matchesAny reports whether any of have appears in allow, case
// insensitively. An empty allow list matches everything.
func matchesAny(have []string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, h := range have {
		for _, a := range allow {
			if strings.EqualFold(h, a) {
				return true
			}
		}
	}
	return false
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
