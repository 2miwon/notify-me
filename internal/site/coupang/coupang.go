// Package coupang implements site.Adapter for Coupang Careers. The web site
// sits behind Cloudflare, but its underlying Greenhouse public board API is
// intended for job-board integrations and returns the complete live listing.
package coupang

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL   = "https://boards-api.greenhouse.io/v1/boards/coupang/jobs?content=true"
	jobURLFmt = "https://www.coupang.jobs/kr/jobs/%d/?gh_jid=%d"
)

type Adapter struct {
	Locations   []string
	Departments []string
	client      *http.Client
}

func New(locations, departments []string) *Adapter {
	return &Adapter{Locations: locations, Departments: departments, client: &http.Client{Timeout: 60 * time.Second}}
}

func (a *Adapter) Name() string { return "coupang" }

type response struct {
	Jobs []listing `json:"jobs"`
}
type listing struct {
	ID                  int     `json:"id"`
	Title               string  `json:"title"`
	CompanyName         string  `json:"company_name"`
	Content             string  `json:"content"`
	FirstPublished      string  `json:"first_published"`
	UpdatedAt           string  `json:"updated_at"`
	ApplicationDeadline *string `json:"application_deadline"`
	Location            struct {
		Name string `json:"name"`
	} `json:"location"`
	Departments []struct {
		Name string `json:"name"`
	} `json:"departments"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var body response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if body.Jobs == nil {
		return nil, fmt.Errorf("response has no jobs array")
	}

	postings := make([]job.Posting, 0)
	for _, item := range body.Jobs {
		if item.ID == 0 || strings.TrimSpace(item.Title) == "" || !matchesLocation(item.Location.Name, a.Locations) || !matchesDepartment(item.Departments, a.Departments) {
			continue
		}
		description := htmlToText(item.Content)
		postedAt := parseTime(item.FirstPublished)
		if postedAt.IsZero() {
			postedAt = parseTime(item.UpdatedAt)
		}
		if postedAt.IsZero() {
			postedAt = time.Now().UTC()
		}
		p := job.Posting{
			Site: a.Name(), ExternalID: fmt.Sprint(item.ID), Title: strings.TrimSpace(item.Title),
			Company: companyName(item.CompanyName), URL: fmt.Sprintf(jobURLFmt, item.ID, item.ID),
			Location: strings.TrimSpace(item.Location.Name), PostedAt: postedAt, Description: description,
			EmploymentType: employmentType(item.Title + "\n" + description),
		}
		if item.ApplicationDeadline != nil {
			if deadline := parseTime(*item.ApplicationDeadline); !deadline.IsZero() {
				p.ApplicationDeadline = &deadline
			}
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

func matchesLocation(location string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(location), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func matchesDepartment(departments []struct {
	Name string `json:"name"`
}, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, department := range departments {
		for _, candidate := range allowed {
			if strings.EqualFold(strings.TrimSpace(department.Name), strings.TrimSpace(candidate)) {
				return true
			}
		}
	}
	return false
}

func companyName(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "Coupang") || strings.TrimSpace(raw) == "" {
		return "쿠팡"
	}
	return strings.TrimSpace(raw)
}

func employmentType(text string) string {
	switch {
	case strings.Contains(text, "인턴"):
		return "Internship"
	case strings.Contains(text, "계약직"):
		return "Contract"
	case strings.Contains(text, "정규직"):
		return "Full-time"
	default:
		return ""
	}
}

func parseTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func htmlToText(source string) string {
	source = html.UnescapeString(source)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(source))
	if err != nil {
		return strings.Join(strings.Fields(source), " ")
	}
	var lines []string
	doc.Find("h1, h2, h3, h4, h5, h6, p, li").Each(func(_ int, s *goquery.Selection) {
		s.Find("br").ReplaceWithHtml("\n")
		for _, line := range strings.Split(s.Text(), "\n") {
			if line = strings.Join(strings.Fields(line), " "); line != "" {
				lines = append(lines, line)
			}
		}
	})
	return strings.Join(lines, "\n")
}
