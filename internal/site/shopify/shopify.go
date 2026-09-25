// Package shopify implements site.Adapter for shopify.com/careers.
//
// Shopify runs its ATS on Ashby but has no public Ashby job board (the
// posting API 404s for it) — instead its own React Router site
// server-renders the whole listing into the page as turbo-stream loader
// data (see turbostream.go):
//
//	GET https://www.shopify.com/careers
//	  loaderData["($locale)/careers"] = {jobPostingsWithJobs: [{jobPosting:
//	    {id, title, teamName, departmentName, locationIds, employmentType,
//	    publishedDate, externalLink, ...}}], atsLocations: [{id, name}]}
//	GET https://www.shopify.com/careers/x_<jobPostingId>
//	  loaderData["($locale)/careers/$posting"].jobPosting.descriptionPlain
//
// Shopify is remote-first but hires per region; Locations filters on the
// posting's own location names ("APAC", "Global", "Singapore", ...).
package shopify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL = "https://www.shopify.com"
	// A plain browser UA — the site serves a bot-check page otherwise.
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36"
)

// Adapter crawls shopify.com/careers.
type Adapter struct {
	// Locations is a case-insensitive substring allow-list against the
	// posting's primary + secondary location names. Empty means all.
	Locations []string
	// DevOnly keeps team/department names job.IsDevCategory accepts
	// ("Engineering", "Data Insights") plus dev-looking titles.
	DevOnly bool

	client *http.Client
}

func New(locations []string, devOnly bool) *Adapter {
	return &Adapter{Locations: locations, DevOnly: devOnly, client: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Adapter) Name() string { return "shopify" }

type jobPosting struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	TeamName       string `json:"teamName"`
	DepartmentName string `json:"departmentName"`
	EmploymentType string `json:"employmentType"`
	WorkplaceType  string `json:"workplaceType"`
	PublishedDate  string `json:"publishedDate"`
	ExternalLink   string `json:"externalLink"`
	IsListed       bool   `json:"isListed"`
	LocationIDs    struct {
		Primary   string   `json:"primaryLocationId"`
		Secondary []string `json:"secondaryLocationIds"`
	} `json:"locationIds"`
	DescriptionPlain string `json:"descriptionPlain"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}

	var loader map[string]struct {
		JobPostingsWithJobs []struct {
			JobPosting jobPosting `json:"jobPosting"`
		} `json:"jobPostingsWithJobs"`
		ATSLocations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"atsLocations"`
	}
	if err := a.loaderData(baseURL+"/careers", &loader); err != nil {
		return nil, fmt.Errorf("shopify: list: %w", err)
	}
	careers, ok := loader["($locale)/careers"]
	if !ok || len(careers.JobPostingsWithJobs) == 0 {
		return nil, fmt.Errorf("shopify: no job postings in careers loader data")
	}
	locationNames := make(map[string]string, len(careers.ATSLocations))
	for _, l := range careers.ATSLocations {
		locationNames[l.ID] = strings.TrimSpace(l.Name)
	}

	var postings []job.Posting
	for _, entry := range careers.JobPostingsWithJobs {
		jp := entry.JobPosting
		if !jp.IsListed {
			continue
		}
		var locs []string
		for _, id := range append([]string{jp.LocationIDs.Primary}, jp.LocationIDs.Secondary...) {
			if name := locationNames[id]; name != "" {
				locs = append(locs, name)
			}
		}
		if !matchesAny(locs, a.Locations) {
			continue
		}
		if a.DevOnly && !job.LooksDev(jp.Title, jp.TeamName, jp.DepartmentName) {
			continue
		}

		location := strings.Join(locs, " / ")
		if jp.WorkplaceType != "" {
			location = jp.WorkplaceType + " - " + location
		}
		p := job.Posting{
			Site:           a.Name(),
			ExternalID:     jp.ID,
			Title:          strings.TrimSpace(jp.Title),
			Company:        "Shopify",
			URL:            jp.ExternalLink,
			Location:       location,
			EmploymentType: employmentType(jp.EmploymentType),
			PostedAt:       time.Now().UTC(),
		}
		if p.URL == "" {
			p.URL = baseURL + "/careers?ashby_jid=" + jp.ID
		}
		if t, err := time.Parse("2006-01-02", jp.PublishedDate); err == nil {
			p.ApplicationStart = &t
		}
		if desc, err := a.fetchDescription(jp.ID); err == nil {
			p.Description = desc
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

func (a *Adapter) fetchDescription(id string) (string, error) {
	var loader map[string]struct {
		JobPosting jobPosting `json:"jobPosting"`
	}
	// The slug part of the path is ignored; only the id after "_" matters.
	if err := a.loaderData(baseURL+"/careers/x_"+id, &loader); err != nil {
		return "", err
	}
	return normalize(loader["($locale)/careers/$posting"].JobPosting.DescriptionPlain), nil
}

func (a *Adapter) loaderData(url string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	raw, err := decodeLoaderData(string(body))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
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
	switch raw {
	case "FullTime":
		return "Full-time"
	case "Intern":
		return "Internship"
	case "Contract":
		return "Contract"
	case "Temporary":
		return "Temporary"
	default:
		return raw
	}
}

func normalize(source string) string {
	var lines []string
	for _, raw := range strings.Split(source, "\n") {
		line := strings.Join(strings.Fields(raw), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
