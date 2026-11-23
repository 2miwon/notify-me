// Package jibe implements site.Adapter for career sites built on Jibe
// (iCIMS's candidate-experience product) — AMD's careers.amd.com is one.
// The site's own search page calls a plain public JSON endpoint:
//
//	GET https://<host>/api/jobs?location=<loc>&page=<n>&limit=100
//
// Each result already carries the full description, so there's no
// per-posting detail request. The location param is a free-text match
// that also hits street addresses, so results are re-checked against the
// posting's own location text.
package jibe

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	pageSize  = 100
)

// Adapter crawls one Jibe career site for one location.
type Adapter struct {
	SiteName string
	Company  string
	// Host is the career site's hostname, e.g. "careers.amd.com".
	Host     string
	Location string
	// DevOnly keeps postings whose category or title looks technical
	// (job.LooksDev).
	DevOnly bool

	client *http.Client
}

func New(siteName, company, host, location string, devOnly bool) *Adapter {
	return &Adapter{
		SiteName: siteName, Company: company, Host: host, Location: location, DevOnly: devOnly,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.SiteName }

type jobsResponse struct {
	TotalCount int `json:"totalCount"`
	Jobs       []struct {
		Data struct {
			Slug           string   `json:"slug"`
			ReqID          string   `json:"req_id"`
			Title          string   `json:"title"`
			Description    string   `json:"description"`
			FullLocation   string   `json:"full_location"`
			Country        string   `json:"country"`
			Category       []string `json:"category"`
			EmploymentType string   `json:"employment_type"`
			PostedDate     string   `json:"posted_date"`
			Language       string   `json:"language"`
		} `json:"data"`
	} `json:"jobs"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	if a.Host == "" {
		return nil, fmt.Errorf("jibe(%s): no host configured", a.SiteName)
	}

	var postings []job.Posting
	seen := 0
	for page := 1; ; page++ {
		resp, err := a.fetchPage(page)
		if err != nil {
			return postings, fmt.Errorf("jibe(%s): page %d: %w", a.SiteName, page, err)
		}
		for _, j := range resp.Jobs {
			d := j.Data
			if !a.inLocation(d.FullLocation + " " + d.Country) {
				continue
			}
			title := strings.TrimSpace(d.Title)
			categories := make([]string, len(d.Category))
			for i, c := range d.Category {
				categories[i] = strings.TrimSpace(c)
			}
			if a.DevOnly && !job.LooksDev(title, categories...) {
				continue
			}

			p := job.Posting{
				Site:           a.Name(),
				ExternalID:     d.ReqID,
				Title:          title,
				Company:        a.Company,
				URL:            a.postingURL(d.Slug, d.Language),
				Location:       d.FullLocation,
				PostedAt:       time.Now().UTC(),
				Description:    htmlutil.StripToText(d.Description),
				EmploymentType: employmentType(d.EmploymentType),
			}
			if t, err := time.Parse("2006-01-02T15:04:05-0700", d.PostedDate); err == nil {
				p.ApplicationStart = &t
			}
			if p.EmploymentType == "" {
				p.EmploymentType = job.EmploymentTypeFromTitle(title)
			}
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}
		seen += len(resp.Jobs)
		if len(resp.Jobs) < pageSize || seen >= resp.TotalCount {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) inLocation(text string) bool {
	return a.Location == "" || strings.Contains(strings.ToLower(text), strings.ToLower(a.Location))
}

func (a *Adapter) postingURL(slug, language string) string {
	u := fmt.Sprintf("https://%s/careers-home/jobs/%s", a.Host, url.PathEscape(slug))
	if language != "" {
		u += "?lang=" + url.QueryEscape(language)
	}
	return u
}

func (a *Adapter) fetchPage(page int) (*jobsResponse, error) {
	query := url.Values{}
	query.Set("location", a.Location)
	query.Set("page", strconv.Itoa(page))
	query.Set("limit", strconv.Itoa(pageSize))

	req, err := http.NewRequest(http.MethodGet, "https://"+a.Host+"/api/jobs?"+query.Encode(), nil)
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
	var out jobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &out, nil
}

// employmentType maps Jibe's schema.org-style values ("FULL_TIME").
func employmentType(raw string) string {
	switch raw {
	case "FULL_TIME":
		return "Full-time"
	case "PART_TIME":
		return "Part-time"
	case "CONTRACTOR":
		return "Contract"
	case "INTERN":
		return "Internship"
	case "TEMPORARY":
		return "Temporary"
	default:
		return ""
	}
}
