// Package roundhr implements site.Adapter for companies hosted on RoundHR
// (라운드HR), a Korean ATS whose "<company>.recruit.roundhr.com" career
// sites are a Next.js shell over a public JSON API:
//
//	GET https://api-prod.roundhr.com/api/site/jobs?code=<company>&per=50&page=N
//	  {"results": [{title, code, position_group{title}, position{title},
//	    application_form{code, employment_type, career_kind, career_start,
//	    career_end, end_at, intro_content, main_task_content,
//	    requirement_content, preferred_point_content, benefit_content}}],
//	   "page": {"total", "pages", "current"}}
//
// A posting's page is https://<company>.recruit.roundhr.com/c/<application_form.code>.
// The company code is the subdomain (ridi.recruit.roundhr.com → "ridi").
package roundhr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	apiBase  = "https://api-prod.roundhr.com/api/site/jobs"
	perPage  = 50
	maxPages = 20
)

// Adapter crawls one RoundHR career site.
type Adapter struct {
	Code    string
	Company string
	// DevOnly keeps postings whose position group / position / title looks
	// technical (job.LooksDev).
	DevOnly bool

	client *http.Client
}

func New(code, company string, devOnly bool) *Adapter {
	return &Adapter{Code: code, Company: company, DevOnly: devOnly, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return a.Code }

type title struct {
	Title string `json:"title"`
}

type jobEntry struct {
	Title         string `json:"title"`
	PositionGroup title  `json:"position_group"`
	Position      title  `json:"position"`
	Form          struct {
		Code                  string  `json:"code"`
		EmploymentType        string  `json:"employment_type"`
		CareerKind            string  `json:"career_kind"`
		CareerStart           *int    `json:"career_start"`
		EndAt                 *string `json:"end_at"`
		IntroContent          string  `json:"intro_content"`
		MainTaskContent       string  `json:"main_task_content"`
		RequirementContent    string  `json:"requirement_content"`
		PreferredPointContent string  `json:"preferred_point_content"`
		BenefitContent        string  `json:"benefit_content"`
		Status                string  `json:"status"`
	} `json:"application_form"`
	CreatedAt string `json:"created_at"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.Code == "" {
		return nil, fmt.Errorf("roundhr: no company code configured")
	}

	var postings []job.Posting
	for page := 1; page <= maxPages; page++ {
		var resp struct {
			Results []jobEntry `json:"results"`
			Page    struct {
				Pages int `json:"pages"`
			} `json:"page"`
		}
		if err := a.getJSON(fmt.Sprintf("%s?code=%s&per=%d&page=%d", apiBase, a.Code, perPage, page), &resp); err != nil {
			return postings, fmt.Errorf("roundhr(%s): page %d: %w", a.Code, page, err)
		}
		for _, e := range resp.Results {
			if a.DevOnly && !job.LooksDev(e.Title, e.PositionGroup.Title, e.Position.Title) {
				continue
			}
			postings = append(postings, a.toPosting(e))
		}
		if page >= resp.Page.Pages {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) toPosting(e jobEntry) job.Posting {
	f := e.Form
	p := job.Posting{
		Site:           a.Name(),
		ExternalID:     f.Code,
		Title:          strings.TrimSpace(e.Title),
		Company:        a.Company,
		URL:            fmt.Sprintf("https://%s.recruit.roundhr.com/c/%s", a.Code, f.Code),
		EmploymentType: employmentType(f.EmploymentType),
		CareerLevel:    careerLevel(f.CareerKind),
		PostedAt:       time.Now().UTC(),
	}
	if t, err := time.Parse(time.RFC3339, e.CreatedAt); err == nil {
		p.ApplicationStart = &t
	}
	if f.EndAt != nil {
		if t, err := time.Parse(time.RFC3339, *f.EndAt); err == nil {
			p.ApplicationDeadline = &t
		}
	}
	var sections []string
	for _, html := range []string{f.IntroContent, f.MainTaskContent, f.RequirementContent, f.PreferredPointContent, f.BenefitContent} {
		if text := htmlutil.StripToText(html); text != "" {
			sections = append(sections, text)
		}
	}
	p.Description = strings.Join(sections, "\n")
	p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
	if p.MinYearsExperience == nil && f.CareerKind == "experienced" && f.CareerStart != nil && *f.CareerStart > 0 {
		years := *f.CareerStart
		p.MinYearsExperience = &years
	}
	return p
}

func (a *Adapter) getJSON(url string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; notify-me-crawler/1.0)")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func employmentType(raw string) string {
	switch raw {
	case "full_time":
		return "Full-time"
	case "contract":
		return "Contract"
	case "intern", "internship":
		return "Internship"
	case "part_time":
		return "Part-time"
	default:
		return ""
	}
}

func careerLevel(kind string) string {
	switch kind {
	case "experienced":
		return "경력"
	case "new", "newcomer", "new_comer":
		return "신입"
	case "irrelevant", "any", "not_matter":
		return "무관"
	default:
		return ""
	}
}
