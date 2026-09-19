// Package netflix implements site.Adapter for explore.jobs.netflix.net.
//
// Netflix's careers site runs on Eightfold.ai's ATS. The listing page
// embeds its own results as JSON server-side, and that same data is
// available from a plain public JSON API — the page's own "Show More"
// button calls it directly, no auth, no bot-blocking:
//
//	GET https://explore.jobs.netflix.net/api/apply/v2/jobs
//	    ?domain=netflix.com&start=<n>&num=10&location=<loc>&sort_by=relevance
//
// `num` above 10 is silently ignored server-side — the site's own UI never
// asks for more than a page at a time, so pagination always advances
// `start` by 10 regardless of what's requested. Each posting's full
// description isn't in the listing response; it's fetched per-ID from:
//
//	GET https://explore.jobs.netflix.net/api/apply/v2/jobs/<id>?domain=netflix.com
package netflix

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	apiBase   = "https://explore.jobs.netflix.net/api/apply/v2/jobs"
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	pageSize  = 10
)

// Adapter crawls explore.jobs.netflix.net for the given locations (OR'd
// across each other — one request per location). Find a location's exact
// string from https://explore.jobs.netflix.net's own location filter.
type Adapter struct {
	Locations []string
	// DevOnly, when true, drops postings that don't look like engineering
	// roles — see devRelated for how and why. When false, every posting
	// for Locations is kept as-is.
	DevOnly bool

	client *http.Client
}

func New(locations []string, devOnly bool) *Adapter {
	return &Adapter{
		Locations: locations,
		DevOnly:   devOnly,
		client:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return "netflix" }

type searchResponse struct {
	Count     int        `json:"count"`
	Positions []position `json:"positions"`
}

type position struct {
	ID                   int64  `json:"id"`
	Name                 string `json:"name"`
	Location             string `json:"location"`
	Department           string `json:"department"`
	TCreate              int64  `json:"t_create"`
	CanonicalPositionURL string `json:"canonicalPositionUrl"`
}

type detailResponse struct {
	JobDescription string `json:"job_description"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	locations := a.Locations
	if len(locations) == 0 {
		locations = []string{""}
	}

	// The site's own pagination can hand back a posting already seen on an
	// earlier page when relevance-sort reorders results mid-crawl —
	// dedupe by ID rather than trust offsets to be a clean partition.
	seen := make(map[int64]bool)
	var postings []job.Posting

	for _, loc := range locations {
		for start := 0; ; start += pageSize {
			resp, err := a.fetchPage(loc, start)
			if err != nil {
				return postings, fmt.Errorf("netflix: page at start %d: %w", start, err)
			}
			if len(resp.Positions) == 0 {
				break
			}

			for _, pos := range resp.Positions {
				if seen[pos.ID] {
					continue
				}
				seen[pos.ID] = true

				if a.DevOnly && !devRelated(pos.Department, pos.Name) {
					continue
				}

				p := job.Posting{
					Site:       a.Name(),
					ExternalID: fmt.Sprintf("%d", pos.ID),
					Title:      pos.Name,
					Company:    "Netflix",
					URL:        pos.CanonicalPositionURL,
					Location:   pos.Location,
					PostedAt:   time.Now().UTC(),
				}
				if pos.TCreate > 0 {
					t := time.Unix(pos.TCreate, 0).UTC()
					p.ApplicationStart = &t
				}

				// A failed detail fetch is a lesser failure than losing the
				// posting entirely — keep it with just the list fields.
				if desc, err := a.fetchDescription(pos.ID); err == nil {
					p.Description = desc
				}
				p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)

				postings = append(postings, p)
			}

			if start+pageSize >= resp.Count {
				break
			}
		}
	}

	return postings, nil
}

func (a *Adapter) fetchPage(location string, start int) (*searchResponse, error) {
	query := url.Values{}
	query.Set("domain", "netflix.com")
	query.Set("start", fmt.Sprintf("%d", start))
	query.Set("num", fmt.Sprintf("%d", pageSize))
	query.Set("sort_by", "relevance")
	if location != "" {
		query.Set("location", location)
	}

	req, err := http.NewRequest(http.MethodGet, apiBase+"?"+query.Encode(), nil)
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

	var out searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

func (a *Adapter) fetchDescription(id int64) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/%d?domain=netflix.com", apiBase, id), nil)
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
	return htmlutil.StripToText(out.JobDescription), nil
}

// devRelated decides whether a posting looks like an engineering role.
//
// Netflix's own `department` field is not a clean engineering/non-
// engineering split: real dev roles ("Senior Data Engineer", "AI
// Engineer", "Data Visualization Engineer") get filed under "Data &
// Insights" next to non-dev researcher/analyst roles, and "Engineering
// Operations" mixes Technical Program Manager with plain Program Manager
// and Capacity Analyst — confirmed by sampling both departments' live
// postings. Since missing a real dev posting is worse than showing an
// extra non-dev one, this ORs two independent signals instead of trusting
// either alone:
//
//  1. department is one Netflix files real engineering work under, even
//     though it also contains non-dev roles (a deliberately broadened
//     allow-list, not just the literal "Engineering" bucket)
//  2. the title itself contains an engineering-role keyword, checked
//     regardless of department — a safety net for a dev posting that
//     lands somewhere unexpected, since (1) can't be exhaustive
//
// Either signal is enough to keep a posting; both must miss for one to be
// dropped. This still isn't a guarantee (a role could be both mistitled
// and mis-departmented), just the best recall-biased approximation from
// the fields Netflix actually exposes.
var devDepartments = map[string]bool{
	"Engineering":            true,
	"Engineering Operations": true,
	"Data & Insights":        true,
	"Games":                  true,
	"Netflix Games Studio":   true,
	"Next Games":             true,
}

var devTitleKeywords = []string{
	"engineer", "engineering", "developer", "swe", "software",
	"sre", "devops", "architect", "programmer",
}

func devRelated(department, title string) bool {
	if devDepartments[department] {
		return true
	}
	lower := strings.ToLower(title)
	for _, kw := range devTitleKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
