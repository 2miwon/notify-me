// Package bucketplace implements site.Adapter for 오늘의집 (Bucketplace)'s
// careers page, bucketplace.com/careers. The site is a Gatsby build whose
// listing ships as static page data:
//
//	GET https://www.bucketplace.com/page-data/careers/page-data.json
//	  {"result": {"data": {"position": {"nodes": [{"frontmatter": {name,
//	    teamName, recruitUrl, region, hiringType, isPool, slug}}]}}}}
//
// Each recruitUrl is a GreetingHR opening page, but the GreetingHR
// company home itself 404s — so the list comes from Gatsby and only the
// description from GreetingHR (see greetinghr.FetchDescription).
package bucketplace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
	"github.com/2miwon/notify-me/internal/site/greetinghr"
)

const listURL = "https://www.bucketplace.com/page-data/careers/page-data.json"

// Adapter crawls bucketplace.com/careers.
type Adapter struct {
	// DevOnly keeps team "Engineering"/"정보보안" (and any other team that
	// job.IsDevCategory accepts) plus dev-looking titles in other teams.
	DevOnly bool

	client *http.Client
}

func New(devOnly bool) *Adapter {
	return &Adapter{DevOnly: devOnly, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "bucketplace" }

type frontmatter struct {
	Name       string `json:"name"`
	TeamName   string `json:"teamName"`
	RecruitURL string `json:"recruitUrl"`
	Region     string `json:"region"`
	HiringType string `json:"hiringType"`
	IsPool     bool   `json:"isPool"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; notify-me-crawler/1.0)")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bucketplace: list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bucketplace: list: unexpected status %d", resp.StatusCode)
	}
	var page struct {
		Result struct {
			Data struct {
				Position struct {
					Nodes []struct {
						Frontmatter frontmatter `json:"frontmatter"`
					} `json:"nodes"`
				} `json:"position"`
			} `json:"data"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("bucketplace: decode list: %w", err)
	}

	var postings []job.Posting
	for _, node := range page.Result.Data.Position.Nodes {
		f := node.Frontmatter
		if f.RecruitURL == "" {
			continue
		}
		if a.DevOnly && !job.LooksDev(f.Name, f.TeamName) {
			continue
		}
		p := job.Posting{
			Site:           a.Name(),
			ExternalID:     f.RecruitURL[strings.LastIndex(f.RecruitURL, "/")+1:],
			Title:          strings.TrimSpace(f.Name),
			Company:        "오늘의집",
			URL:            f.RecruitURL,
			Location:       f.Region,
			EmploymentType: employmentType(f.HiringType, f.Name),
			PostedAt:       time.Now().UTC(),
		}
		if desc, err := greetinghr.FetchDescription(a.client, f.RecruitURL); err == nil {
			p.Description = desc
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

// employmentType maps hiringType; the site files 계약직 roles as
// "parttime" even though they're full-hours fixed-term contracts.
func employmentType(hiringType, title string) string {
	switch hiringType {
	case "fulltime":
		return "Full-time"
	case "intern":
		return "Internship"
	case "parttime":
		if strings.Contains(title, "계약직") {
			return "Contract"
		}
		return "Temporary"
	default:
		return hiringType
	}
}
