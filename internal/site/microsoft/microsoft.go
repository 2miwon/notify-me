// Package microsoft implements site.Adapter for
// apply.careers.microsoft.com (what careers.microsoft.com's search box
// actually submits to).
//
// Both the search and per-job detail are plain public JSON endpoints —
// no auth needed, just a Referer header matching the site's own origin
// (without it the API 429s; this isn't real security, just a check that
// happens to be trivially satisfiable):
//
//	GET /api/pcsx/search?domain=microsoft.com&query=<q>&location=<loc>&start=<n>
//	GET /api/pcsx/position_details?position_id=<id>&domain=microsoft.com&hl=en
//
// Location, not a text query, is what should actually scope this to
// Korea — a keyword search for e.g. "seoul" only matches postings whose
// title/location text literally contains that word, which silently
// misses real Korea postings located somewhere else (Busan's data-center
// build-out roles, for instance, say "Busan", never "Seoul").
package microsoft

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL   = "https://apply.careers.microsoft.com"
	referer   = "https://apply.careers.microsoft.com/careers?domain=microsoft.com"
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
)

// Adapter crawls apply.careers.microsoft.com for one location, optionally
// narrowed by a text query. Find values by searching on
// https://careers.microsoft.com and reading them back out of the URL.
type Adapter struct {
	Query    string
	Location string
	// DevOnly, when true, drops postings that don't look like software/
	// engineering roles — see devRelated. Without it, a broad location
	// search returns mostly non-dev roles (sales, CSM, facilities,
	// procurement all show up for a plain "Korea" search).
	DevOnly bool

	client *http.Client
}

func New(query, location string, devOnly bool) *Adapter {
	return &Adapter{Query: query, Location: location, DevOnly: devOnly, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "microsoft" }

type searchResponse struct {
	Data struct {
		Positions []position `json:"positions"`
		Count     int        `json:"count"`
	} `json:"data"`
}

type position struct {
	ID           int64    `json:"id"`
	DisplayJobID string   `json:"displayJobId"`
	Name         string   `json:"name"`
	Locations    []string `json:"locations"`
	Department   string   `json:"department"`
	PostedTs     int64    `json:"postedTs"`
	PositionURL  string   `json:"positionUrl"`
}

type detailResponse struct {
	Data struct {
		JobDescription             string   `json:"jobDescription"`
		PublicURL                  string   `json:"publicUrl"`
		EfcustomTextEmploymentType []string `json:"efcustomTextEmploymentType"`
	} `json:"data"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	start := 0

	for {
		resp, err := a.fetchSearchPage(start)
		if err != nil {
			return postings, fmt.Errorf("microsoft: search at start %d: %w", start, err)
		}
		if len(resp.Data.Positions) == 0 {
			break
		}

		for _, pos := range resp.Data.Positions {
			if a.DevOnly && !devRelated(pos.Department, pos.Name) {
				continue
			}

			p := job.Posting{
				Site:       a.Name(),
				ExternalID: strconv.FormatInt(pos.ID, 10),
				Title:      pos.Name,
				Company:    "Microsoft",
				URL:        baseURL + pos.PositionURL,
				Location:   strings.Join(pos.Locations, "; "),
				PostedAt:   time.Now().UTC(),
			}
			if pos.PostedTs > 0 {
				t := time.Unix(pos.PostedTs, 0).UTC()
				p.ApplicationStart = &t
			}

			// A failed detail fetch is a lesser failure than losing the
			// posting entirely — keep it with just the search fields.
			if detail, err := a.fetchDetail(pos.ID); err == nil {
				p.Description = htmlutil.StripToText(detail.Data.JobDescription)
				if detail.Data.PublicURL != "" {
					p.URL = detail.Data.PublicURL
				}
				if len(detail.Data.EfcustomTextEmploymentType) > 0 {
					p.EmploymentType = job.CanonicalEmploymentType(detail.Data.EfcustomTextEmploymentType[0])
				}
			}

			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}

		start += len(resp.Data.Positions)
		if start >= resp.Data.Count {
			break
		}
	}

	return postings, nil
}

func (a *Adapter) fetchSearchPage(start int) (*searchResponse, error) {
	query := url.Values{}
	query.Set("domain", "microsoft.com")
	query.Set("query", a.Query)
	query.Set("location", a.Location)
	query.Set("start", strconv.Itoa(start))

	reqURL := baseURL + "/api/pcsx/search?" + query.Encode()
	var out searchResponse
	if err := a.getJSON(reqURL, &out); err != nil {
		return nil, err
	}
	// TEMPORARY debug line — investigating why this returns 0 positions
	// from GitHub Actions' runner IPs but real results from elsewhere.
	// Remove once diagnosed.
	log.Printf("microsoft: debug: url=%s count=%d positions=%d", reqURL, out.Data.Count, len(out.Data.Positions))
	return &out, nil
}

func (a *Adapter) fetchDetail(positionID int64) (*detailResponse, error) {
	query := url.Values{}
	query.Set("position_id", strconv.FormatInt(positionID, 10))
	query.Set("domain", "microsoft.com")
	query.Set("hl", "en")

	var out detailResponse
	if err := a.getJSON(baseURL+"/api/pcsx/position_details?"+query.Encode(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// devRelated decides whether a posting looks like a software/engineering
// role. A plain "Korea" location search returns mostly non-dev postings
// (sales, customer success, procurement, data-center facilities all
// showed up ahead of the actual engineering roles in a live sample), and
// Microsoft's own `department` naming isn't consistent enough to allow-
// list cleanly, so this ORs a department hint with a title keyword check
// — the same recall-biased shape as internal/site/netflix's devRelated:
// either signal is enough to keep a posting, both must miss to drop one.
//
// The department hints are deliberately multi-word/specific rather than
// bare words like "engineering" or "software" — a data-center facilities
// role at Microsoft can be filed under "Electrical Engineering", which a
// bare "engineering" substring match would wrongly wave through.
var devDepartmentHints = []string{
	"software engineering", "solution engineering", "solution architecture",
	"applied science", "data science", "data & applied science",
}

var devTitleKeywords = []string{
	"engineer", "developer", "swe", "software", "sre", "devops",
	"architect", "programmer", "scientist",
}

func devRelated(department, title string) bool {
	lowerDept := strings.ToLower(department)
	for _, hint := range devDepartmentHints {
		if strings.Contains(lowerDept, hint) {
			return true
		}
	}
	lowerTitle := strings.ToLower(title)
	for _, kw := range devTitleKeywords {
		if strings.Contains(lowerTitle, kw) {
			return true
		}
	}
	return false
}

// getJSON retries on 429: fetching one detail page per posting with no
// pacing reliably triggers rate limiting partway through a run (measured
// ~50% of detail fetches failing this way on a live crawl), which for a
// "lesser failure than losing the posting" fallback silently means half
// the postings end up with no description/employment type at all rather
// than an actual fetch error. A few retries with backoff clears the
// large majority of these without needing to slow down every request.
func (a *Adapter) getJSON(url string, out interface{}) error {
	const maxAttempts = 4
	backoff := 500 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := a.getJSONOnce(url, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxAttempts {
			break
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return lastErr
}

func (a *Adapter) getJSONOnce(url string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", referer)

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("unexpected status %d", e.code) }

func isRetryable(err error) bool {
	var se *statusError
	if !errors.As(err, &se) {
		return false
	}
	return se.code == http.StatusTooManyRequests || se.code >= 500
}
