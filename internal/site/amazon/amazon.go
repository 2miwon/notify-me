// Package amazon implements site.Adapter for amazon.jobs.
//
// The listing page's data comes from a plain public JSON endpoint the
// page's own filters call — no auth, no bot-blocking:
//
//	GET https://www.amazon.jobs/en/search.json
//	    ?category[]=<code>&location[]=<code>&offset=<n>&result_limit=<n>
//
// Each returned job already includes its full description,
// qualifications, and posted date — no separate detail-page fetch
// needed, unlike naver/line/apple.
package amazon

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
	searchURL  = "https://www.amazon.jobs/en/search.json"
	jobURLBase = "https://www.amazon.jobs"
	userAgent  = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	pageSize   = 100
	dateLayout = "January 2, 2006"
)

// Adapter crawls amazon.jobs, filtered by category and location — both
// OR'd within themselves, AND'd across each other, matching the site's
// own filter semantics. An empty slice for either means "don't filter on
// this dimension". Find values by ticking filters on
// https://www.amazon.jobs/en/search and reading them back out of the
// URL's category[]/location[] params.
type Adapter struct {
	Categories []string
	Locations  []string

	client *http.Client
}

func New(categories, locations []string) *Adapter {
	return &Adapter{
		Categories: categories,
		Locations:  locations,
		client:     &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return "amazon" }

type searchResponse struct {
	Hits int         `json:"hits"`
	Jobs []amazonJob `json:"jobs"`
}

type amazonJob struct {
	ID                      string `json:"id"`
	Title                   string `json:"title"`
	CompanyName             string `json:"company_name"`
	JobPath                 string `json:"job_path"`
	Location                string `json:"location"`
	JobScheduleType         string `json:"job_schedule_type"`
	PostedDate              string `json:"posted_date"`
	Description             string `json:"description"`
	BasicQualifications     string `json:"basic_qualifications"`
	PreferredQualifications string `json:"preferred_qualifications"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	offset := 0

	for {
		resp, err := a.fetchPage(offset)
		if err != nil {
			return postings, fmt.Errorf("amazon: page at offset %d: %w", offset, err)
		}
		if len(resp.Jobs) == 0 {
			break
		}

		for _, j := range resp.Jobs {
			p := job.Posting{
				Site:           a.Name(),
				ExternalID:     j.ID,
				Title:          j.Title,
				Company:        j.CompanyName,
				URL:            jobURLBase + j.JobPath,
				Location:       j.Location,
				EmploymentType: job.CanonicalEmploymentType(j.JobScheduleType),
				PostedAt:       time.Now().UTC(),
				Description:    combineDescription(j),
			}
			if t, err := time.Parse(dateLayout, j.PostedDate); err == nil {
				p.ApplicationStart = &t
			}
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)

			postings = append(postings, p)
		}

		offset += pageSize
		if offset >= resp.Hits {
			break
		}
	}

	return postings, nil
}

func (a *Adapter) fetchPage(offset int) (*searchResponse, error) {
	query := url.Values{}
	for _, c := range a.Categories {
		query.Add("category[]", c)
	}
	for _, l := range a.Locations {
		query.Add("location[]", l)
	}
	query.Set("offset", fmt.Sprintf("%d", offset))
	query.Set("result_limit", fmt.Sprintf("%d", pageSize))
	query.Set("sort", "recent")

	req, err := http.NewRequest(http.MethodGet, searchURL+"?"+query.Encode(), nil)
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

// combineDescription joins the three free-text fields Amazon splits a
// posting into. Each is itself an HTML fragment (mainly "<br/>" between
// paragraphs) rather than plain text, so this strips tags the same way
// as every other adapter's description handling, via goquery.
func combineDescription(j amazonJob) string {
	var parts []string
	for _, section := range []string{j.Description, j.BasicQualifications, j.PreferredQualifications} {
		if text := htmlutil.StripToText(section); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}
