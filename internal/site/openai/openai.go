// Package openai implements site.Adapter for OpenAI's public Ashby job board.
// The careers UI is Cloudflare-protected, while Ashby's posting API supplies
// the same listed jobs with stable department, team, and description fields.
package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
)

const jobsURL = "https://api.ashbyhq.com/posting-api/job-board/openai?includeCompensation=false"

type Adapter struct {
	Departments []string
	Locations   []string
	client      *http.Client
}

func New(departments, locations []string) *Adapter {
	return &Adapter{Departments: departments, Locations: locations, client: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Adapter) Name() string { return "openai" }

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
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, jobsURL, nil)
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
		if !item.IsListed || !matches(item.Department, a.Departments) || !matches(item.Location, a.Locations) {
			continue
		}
		postedAt, _ := time.Parse(time.RFC3339Nano, item.PublishedAt)
		if postedAt.IsZero() {
			postedAt = time.Now().UTC()
		}
		p := job.Posting{
			Site: a.Name(), ExternalID: item.ID, Title: strings.TrimSpace(item.Title),
			Company: "OpenAI", URL: item.JobURL, Location: strings.TrimSpace(item.Location),
			EmploymentType: employmentType(item.EmploymentType), CareerLevel: careerLevel(item.Title),
			PostedAt: postedAt, ApplicationStart: &postedAt, Description: normalizeDescription(item.Description),
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
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
