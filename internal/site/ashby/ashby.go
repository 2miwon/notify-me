// Package ashby implements site.Adapter for companies whose careers page
// is backed by Ashby's public job-board API — a plain public JSON
// endpoint, no auth needed, regardless of what the company's own careers
// UI is actually built on. OpenAI and Snowflake both use it; any other
// "jobs.ashbyhq.com/<slug>" board should work the same way:
//
//	GET https://api.ashbyhq.com/posting-api/job-board/<slug>?includeCompensation=false
//
// Snowflake's own careers site (careers.snowflake.com) is a much heavier
// Phenom People "widget" page whose visible results are embedded as a
// large inline JSON blob in the page HTML rather than served from a
// clean endpoint — Ashby (what its "Apply" button actually links to) is
// far simpler to depend on for the same data.
package ashby

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
)

// Adapter crawls one Ashby job board. Locations is a case-insensitive
// substring allow-list (OR'd) checked against Ashby's primary `location`
// and every `secondaryLocations` entry — a remote-friendly posting often
// lists "Remote - APAC" only as a secondary location. Empty means no
// restriction.
type Adapter struct {
	CompanySlug string
	// Company is the display name stored on each posting.
	Company   string
	Locations []string
	// DevOnly keeps only postings whose department/team or title looks
	// technical (job.IsDevCategory OR job.IsDevTitle).
	DevOnly bool

	client *http.Client
}

func New(companySlug, company string, locations []string, devOnly bool) *Adapter {
	return &Adapter{CompanySlug: companySlug, Company: company, Locations: locations, DevOnly: devOnly, client: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Adapter) Name() string { return a.CompanySlug }

type response struct {
	Jobs []listing `json:"jobs"`
}

type listing struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Department     string `json:"department"`
	Team           string `json:"team"`
	EmploymentType string `json:"employmentType"`
	Location       string `json:"location"`
	PublishedAt    string `json:"publishedAt"`
	IsListed       bool   `json:"isListed"`
	JobURL         string `json:"jobUrl"`
	Description    string `json:"descriptionPlain"`

	SecondaryLocations []struct {
		Location string `json:"location"`
	} `json:"secondaryLocations"`
}

func (l listing) locations() []string {
	out := []string{l.Location}
	for _, s := range l.SecondaryLocations {
		out = append(out, s.Location)
	}
	return out
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	if a.CompanySlug == "" {
		return nil, fmt.Errorf("ashby: no company slug configured")
	}

	url := fmt.Sprintf("https://api.ashbyhq.com/posting-api/job-board/%s?includeCompensation=false", a.CompanySlug)
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
	var data response
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	postings := make([]job.Posting, 0, len(data.Jobs))
	for _, item := range data.Jobs {
		if !item.IsListed || !matchesAny(item.locations(), a.Locations) {
			continue
		}
		if a.DevOnly && !job.LooksDev(item.Title, item.Department, item.Team) {
			continue
		}
		postedAt, _ := time.Parse(time.RFC3339Nano, item.PublishedAt)
		if postedAt.IsZero() {
			postedAt = time.Now().UTC()
		}
		p := job.Posting{
			Site: a.Name(), ExternalID: item.ID, Title: strings.TrimSpace(item.Title),
			Company: a.Company, URL: item.JobURL, Location: strings.TrimSpace(item.Location),
			EmploymentType: employmentType(item.EmploymentType), CareerLevel: careerLevel(item.Title),
			PostedAt: postedAt, ApplicationStart: &postedAt, Description: normalizeDescription(item.Description),
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

func matchesAny(values, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range values {
		lower := strings.ToLower(value)
		for _, candidate := range allowed {
			if c := strings.ToLower(strings.TrimSpace(candidate)); c != "" && strings.Contains(lower, c) {
				return true
			}
		}
	}
	return false
}

func employmentType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "fulltime", "full-time":
		return "Full-time"
	case "intern", "internship":
		return "Internship"
	case "contract", "contractor":
		return "Contract"
	case "temporary":
		return "Temporary"
	default:
		return strings.TrimSpace(raw)
	}
}

func careerLevel(title string) string {
	title = strings.ToLower(title)
	switch {
	case strings.Contains(title, "intern"), strings.Contains(title, "new grad"), strings.Contains(title, "university"):
		return "신입"
	case strings.Contains(title, "senior"), strings.Contains(title, "staff"), strings.Contains(title, "principal"), strings.Contains(title, "lead"):
		return "경력"
	default:
		return ""
	}
}

func normalizeDescription(source string) string {
	var lines []string
	for _, raw := range strings.Split(source, "\n") {
		line := strings.Join(strings.Fields(raw), " ")
		if line != "" && (len(lines) == 0 || lines[len(lines)-1] != line) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
