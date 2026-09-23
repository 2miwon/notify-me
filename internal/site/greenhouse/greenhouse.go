// Package greenhouse implements site.Adapter for companies whose careers
// page is backed by Greenhouse's public Job Board API — a plain public
// JSON endpoint, no auth needed, regardless of what the company's own
// careers UI is actually built on. Sendbird and Moloco both use it; any
// other "boards-api.greenhouse.io/v1/boards/<slug>" board should work
// the same way:
//
//	GET https://boards-api.greenhouse.io/v1/boards/<slug>/jobs/?content=true
package greenhouse

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

// Adapter crawls one Greenhouse job board.
type Adapter struct {
	CompanySlug string
	// Company is the display name stored on each posting.
	Company string
	// Locations is a substring allow-list (OR'd, case-insensitive)
	// against Greenhouse's own `location.name`. Empty means no
	// restriction.
	Locations []string
	// DevOnly, when true, drops postings whose title doesn't look like a
	// software/engineering role. Greenhouse `departments` names vary too
	// much between companies (and even within one — Moloco alone spreads
	// engineering work across "Engineering", "Ads Platform", "Ads
	// Quality", "MCM Quality", "Moloco Next") to allow-list reliably, so
	// this checks the title instead, in English and Korean.
	DevOnly bool

	client *http.Client
}

func New(companySlug, company string, locations []string, devOnly bool) *Adapter {
	return &Adapter{
		CompanySlug: companySlug,
		Company:     company,
		Locations:   locations,
		DevOnly:     devOnly,
		client:      &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.CompanySlug }

type jobsResponse struct {
	Jobs []posting `json:"jobs"`
}

type posting struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	AbsoluteURL    string `json:"absolute_url"`
	Content        string `json:"content"`
	FirstPublished string `json:"first_published"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.CompanySlug == "" {
		return nil, fmt.Errorf("greenhouse: no company slug configured")
	}

	url := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs/?content=true", a.CompanySlug)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; notify-me-crawler/1.0)")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var data jobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	var out []job.Posting
	for _, p := range data.Jobs {
		if !matchesLocation(p.Location.Name, a.Locations) {
			continue
		}
		title := strings.TrimSpace(p.Title)
		if a.DevOnly && !devRelated(title) {
			continue
		}

		// Greenhouse's own `content` field is HTML that's itself been
		// HTML-escaped once into the JSON string value (e.g. literal
		// "&lt;p&gt;" rather than "<p>") — unescape once before parsing
		// it as HTML, or the tags never get recognized as tags at all.
		description := htmlutil.StripToText(html.UnescapeString(p.Content))

		jp := job.Posting{
			Site:        a.Name(),
			ExternalID:  fmt.Sprintf("%d", p.ID),
			Title:       title,
			Company:     a.Company,
			URL:         p.AbsoluteURL,
			Location:    p.Location.Name,
			PostedAt:    time.Now().UTC(),
			Description: description,
		}
		if t, err := time.Parse(time.RFC3339, p.FirstPublished); err == nil {
			jp.ApplicationStart = &t
		}
		jp.MinYearsExperience = job.ExtractMinYearsExperience(jp.Title + "\n" + jp.Description)
		out = append(out, jp)
	}
	return out, nil
}

func matchesLocation(location string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	lower := strings.ToLower(location)
	for _, candidate := range allowed {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(candidate))) {
			return true
		}
	}
	return false
}

var devTitleKeywords = []string{
	"engineer", "developer", "swe", "software", "sre", "devops",
	"architect", "programmer", "scientist",
	"엔지니어", "개발자", "과학자",
}

func devRelated(title string) bool {
	lower := strings.ToLower(title)
	for _, kw := range devTitleKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
