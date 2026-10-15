// Package yello implements site.Adapter for job boards on Yello
// (recsolu.com) — Kearney's careers search (kearney.recsolu.com). The
// board's own "load more" calls a JSON endpoint that returns a page of
// rendered result rows:
//
//	GET https://<host>/job_boards/<board id>/search?page_number=N
//	    (X-Requested-With: XMLHttpRequest)
//	  {"html": "<li class=search-results__item>…<a class=search-results__req_title
//	    href=/jobs/<id>?job_board_id=…>Title</a><div><span>Full-time</span>
//	    <span>Region</span><span>City, City</span></div>…", "more_requisitions": bool}
//	GET https://<host>/jobs/<id>  → div.job-details__description
//
// The location filter's ids aren't stable to address directly, so this
// pages through the whole (small, ~200-role) board and filters by the
// city text each row carries.
package yello

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	maxPages  = 40
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
)

var (
	rowPattern  = regexp.MustCompile(`<a class="search-results__req_title"[^>]*href="([^"]*)">([^<]*)</a><div>((?:<span>[^<]*</span>)*)</div>`)
	spanPattern = regexp.MustCompile(`<span>([^<]*)</span>`)
)

// Adapter crawls one Yello job board.
type Adapter struct {
	SiteName string
	Company  string
	Host     string
	BoardID  string
	// Locations is a case-insensitive substring allow-list against each
	// row's city text ("Seoul", "Bangkok, Jakarta, Singapore"). Empty = all.
	Locations []string
	// DevOnly keeps engineer/developer/data-scientist/architect titles
	// (job.LooksDev) — Yello rows carry no department to go on.
	DevOnly bool

	client *http.Client
}

func New(siteName, company, host, boardID string, locations []string, devOnly bool) *Adapter {
	return &Adapter{
		SiteName: siteName, Company: company, Host: host, BoardID: boardID,
		Locations: locations, DevOnly: devOnly, client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.SiteName }

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	var postings []job.Posting
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			HTML             string `json:"html"`
			MoreRequisitions bool   `json:"more_requisitions"`
		}
		if err := a.getJSON(fmt.Sprintf("https://%s/job_boards/%s/search?page_number=%d", a.Host, a.BoardID, page), &resp); err != nil {
			return postings, fmt.Errorf("%s: page %d: %w", a.SiteName, page, err)
		}
		rows := rowPattern.FindAllStringSubmatch(resp.HTML, -1)
		for _, row := range rows {
			href, title := row[1], strings.TrimSpace(html.UnescapeString(row[2]))
			var spans []string
			for _, s := range spanPattern.FindAllStringSubmatch(row[3], -1) {
				spans = append(spans, html.UnescapeString(s[1]))
			}
			url := "https://" + a.Host + href
			if seen[url] {
				continue
			}
			seen[url] = true
			cities := ""
			if len(spans) > 0 {
				cities = spans[len(spans)-1]
			}
			if !matchesAny(cities, a.Locations) {
				continue
			}
			if a.DevOnly && !job.LooksDev(title) {
				continue
			}
			p := job.Posting{
				Site:       a.SiteName,
				ExternalID: externalID(href),
				Title:      title,
				Company:    a.Company,
				URL:        url,
				Location:   cities,
				PostedAt:   time.Now().UTC(),
			}
			if len(spans) > 0 {
				p.EmploymentType = job.CanonicalEmploymentType(strings.ToLower(spans[0]))
				if strings.Contains(strings.ToLower(spans[0]), "intern") {
					p.EmploymentType = "Internship"
				}
			}
			if et := job.EmploymentTypeFromTitle(title); et != "" {
				p.EmploymentType = et
			}
			if desc, err := a.fetchDescription(url); err == nil {
				p.Description = desc
			}
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}
		if !resp.MoreRequisitions || len(rows) == 0 {
			break
		}
	}
	return postings, nil
}

func externalID(href string) string {
	id := strings.TrimPrefix(href, "/jobs/")
	if i := strings.IndexByte(id, '?'); i >= 0 {
		id = id[:i]
	}
	return id
}

func matchesAny(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	lower := strings.ToLower(value)
	for _, c := range allowed {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" && strings.Contains(lower, c) {
			return true
		}
	}
	return false
}

func (a *Adapter) fetchDescription(url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}
	body, _ := doc.Find("div.job-details__description").First().Html()
	return htmlutil.StripToText(body), nil
}

func (a *Adapter) getJSON(url string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
