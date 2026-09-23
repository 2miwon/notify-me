// Package lever implements site.Adapter for companies whose careers page
// is backed by Lever's public postings API — a plain public JSON
// endpoint, no auth needed, regardless of what the company's own careers
// UI is actually built on. Channel Talk (channel.io, Lever slug "zoyi")
// is one instance; any other "jobs.lever.co/<slug>" board should work
// the same way:
//
//	GET https://api.lever.co/v0/postings/<slug>?mode=json
package lever

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
)

// Adapter crawls one Lever postings board.
type Adapter struct {
	CompanySlug string
	// Company is the display name stored on each posting.
	Company string
	// Countries is an exact-match allow-list (OR'd) against Lever's own
	// `country` field (e.g. "KR"). Empty means no restriction.
	Countries []string
	// Teams is an exact-match allow-list (OR'd) against
	// categories.team. Empty means no restriction.
	Teams []string

	client *http.Client
}

func New(companySlug, company string, countries, teams []string) *Adapter {
	return &Adapter{
		CompanySlug: companySlug,
		Company:     company,
		Countries:   countries,
		Teams:       teams,
		client:      &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.CompanySlug }

type posting struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	HostedURL  string `json:"hostedUrl"`
	CreatedAt  int64  `json:"createdAt"`
	Country    string `json:"country"`
	Categories struct {
		Team       string `json:"team"`
		Department string `json:"department"`
		Location   string `json:"location"`
		Commitment string `json:"commitment"`
	} `json:"categories"`
	DescriptionPlain string `json:"descriptionPlain"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.CompanySlug == "" {
		return nil, fmt.Errorf("lever: no company slug configured")
	}

	url := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json", a.CompanySlug)
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
	var postings []posting
	if err := json.NewDecoder(resp.Body).Decode(&postings); err != nil {
		return nil, err
	}

	var out []job.Posting
	for _, p := range postings {
		if !matches(p.Country, a.Countries) || !matches(p.Categories.Team, a.Teams) {
			continue
		}
		postedAt := time.Now().UTC()
		var appStart *time.Time
		if p.CreatedAt > 0 {
			t := time.UnixMilli(p.CreatedAt).UTC()
			appStart = &t
		}
		jp := job.Posting{
			Site:             a.Name(),
			ExternalID:       p.ID,
			Title:            strings.TrimSpace(p.Text),
			Company:          a.Company,
			URL:              p.HostedURL,
			Location:         p.Categories.Location,
			PostedAt:         postedAt,
			ApplicationStart: appStart,
			Description:      normalizeText(p.DescriptionPlain),
		}
		jp.MinYearsExperience = job.ExtractMinYearsExperience(jp.Title + "\n" + jp.Description)
		out = append(out, jp)
	}
	return out, nil
}

// normalizeText collapses runs of blank lines in Lever's already-plain-text
// descriptionPlain field, matching the tidiness of every other adapter's
// description without needing an HTML stripper on non-HTML input.
func normalizeText(source string) string {
	var lines []string
	for _, raw := range strings.Split(source, "\n") {
		line := strings.Join(strings.Fields(raw), " ")
		if line != "" && (len(lines) == 0 || lines[len(lines)-1] != line) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func matches(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}
