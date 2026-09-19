// Package ninehire implements site.Adapter for companies hosted on
// NineHire (나인하이어) — a Korean ATS SaaS platform. Kakao Pay
// Securities' career site (kakaopaysec.ninehire.site) is one instance;
// any other "<company>.ninehire.site" site should work the same way.
//
// The listing is a plain public JSON endpoint (no auth, matches what the
// site's own homepage calls):
//
//	GET /_backend/identity-access/homepage/recruitments
//	    ?companyId=<uuid>&page=<n>&countPerPage=20&order=created_at_desc
//
// CompanyID is that UUID — find it by opening the company's own career
// site and reading it out of this same request in the network tab.
//
// Each posting's full description isn't in that listing payload, so it's
// fetched per-posting from the Next.js detail page's own server-rendered
// `__NEXT_DATA__` blob (same technique as internal/site/greetinghr, but a
// different schema — NineHire's detail page nests the description HTML
// under `props.pageProps.jobPosting.content`, not the recruitment object
// itself):
//
//	GET /job_posting/<addressKey>
package ninehire

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	userAgent    = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	countPerPage = 20
)

// Adapter crawls one NineHire-hosted company's career site.
type Adapter struct {
	// Subdomain is the company's NineHire subdomain, e.g. "kakaopaysec"
	// for kakaopaysec.ninehire.site.
	Subdomain string
	// CompanyID is the UUID the site's own homepage passes as
	// `companyId` — copy it from a network request on the company's site.
	CompanyID string
	// Company is the display name stored on each posting.
	Company string
	// DevOnly, when true, drops postings that don't look like software/
	// engineering roles — see devRelated. An unfiltered listing is mostly
	// non-dev (purchasing, HR, customer support, marketing, finance, ...
	// outnumber the actual engineering job groups).
	DevOnly bool

	client *http.Client
}

func New(subdomain, companyID, company string, devOnly bool) *Adapter {
	return &Adapter{
		Subdomain: subdomain,
		CompanyID: companyID,
		Company:   company,
		DevOnly:   devOnly,
		client:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.Subdomain }

func (a *Adapter) baseURL() string {
	return "https://" + a.Subdomain + ".ninehire.site"
}

type recruitmentsResponse struct {
	Count   int           `json:"count"`
	Results []recruitment `json:"results"`
}

type recruitment struct {
	AddressKey     string   `json:"addressKey"`
	Title          string   `json:"title"`
	ExternalTitle  string   `json:"externalTitle"`
	DeadlineType   string   `json:"deadlineType"`
	DeadlineValue  *string  `json:"deadlineValue"`
	EmploymentType []string `json:"employmentType"`
	Career         struct {
		Type  string `json:"type"`
		Range struct {
			Over  int `json:"over"`
			Below int `json:"below"`
		} `json:"range"`
	} `json:"career"`
	JobLocations []struct {
		PlaceName   string `json:"placeName"`
		AddressName string `json:"addressName"`
	} `json:"jobLocations"`
	JobGroup struct {
		Title string `json:"title"`
	} `json:"jobGroup"`
	CreatedAt string `json:"createdAt"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.Subdomain == "" || a.CompanyID == "" {
		return nil, fmt.Errorf("ninehire: subdomain and companyID must both be set")
	}

	var postings []job.Posting
	for page := 1; ; page++ {
		resp, err := a.fetchPage(page)
		if err != nil {
			return postings, fmt.Errorf("ninehire(%s): page %d: %w", a.Subdomain, page, err)
		}
		if len(resp.Results) == 0 {
			break
		}

		for _, r := range resp.Results {
			title := r.ExternalTitle
			if title == "" {
				title = r.Title
			}
			if a.DevOnly && !devRelated(r.JobGroup.Title, title) {
				continue
			}

			p := job.Posting{
				Site:       a.Name(),
				ExternalID: r.AddressKey,
				Title:      strings.TrimSpace(title),
				Company:    a.Company,
				URL:        fmt.Sprintf("%s/job_posting/%s", a.baseURL(), r.AddressKey),
				PostedAt:   time.Now().UTC(),
			}
			if len(r.JobLocations) > 0 {
				loc := r.JobLocations[0]
				if loc.AddressName != "" {
					p.Location = loc.AddressName
				} else {
					p.Location = loc.PlaceName
				}
			}
			if len(r.EmploymentType) > 0 {
				p.EmploymentType = canonicalEmploymentType(r.EmploymentType[0])
			}
			p.CareerLevel = canonicalCareerType(r.Career.Type)
			if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
				p.ApplicationStart = &t
			}
			if r.DeadlineType != "until_filled" && r.DeadlineValue != nil {
				if t, err := time.Parse(time.RFC3339, *r.DeadlineValue); err == nil {
					p.ApplicationDeadline = &t
				}
			}

			// A failed detail fetch is a lesser failure than losing the
			// posting entirely — keep it with just the list fields.
			if desc, err := a.fetchDescription(r.AddressKey); err == nil {
				p.Description = desc
			}

			// The listing already reports a minimum years-of-experience
			// as structured data (unlike every other adapter, which has
			// to guess from free text) — prefer it, falling back to the
			// usual best-effort extraction only when it's absent (0).
			if r.Career.Range.Over > 0 {
				years := r.Career.Range.Over
				p.MinYearsExperience = &years
			} else {
				p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			}

			postings = append(postings, p)
		}

		if page*countPerPage >= resp.Count {
			break
		}
	}

	return postings, nil
}

func (a *Adapter) fetchPage(page int) (*recruitmentsResponse, error) {
	url := fmt.Sprintf("%s/_backend/identity-access/homepage/recruitments?companyId=%s&page=%d&countPerPage=%d&order=created_at_desc",
		a.baseURL(), a.CompanyID, page, countPerPage)

	req, err := http.NewRequest(http.MethodGet, url, nil)
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
	var out recruitmentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

func (a *Adapter) fetchDescription(addressKey string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/job_posting/%s", a.baseURL(), addressKey), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	const marker = `<script id="__NEXT_DATA__" type="application/json">`
	html := string(body)
	idx := strings.Index(html, marker)
	if idx == -1 {
		return "", fmt.Errorf("__NEXT_DATA__ not found")
	}
	start := idx + len(marker)
	end := strings.Index(html[start:], "</script>")
	if end == -1 {
		return "", fmt.Errorf("__NEXT_DATA__ unterminated")
	}

	var envelope struct {
		Props struct {
			PageProps struct {
				JobPosting struct {
					Content string `json:"content"`
				} `json:"jobPosting"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal([]byte(html[start:start+end]), &envelope); err != nil {
		return "", fmt.Errorf("decode __NEXT_DATA__: %w", err)
	}
	return htmlutil.StripToText(envelope.Props.PageProps.JobPosting.Content), nil
}

func canonicalEmploymentType(raw string) string {
	switch raw {
	case "full_time":
		return "Full-time"
	case "part_time":
		return "Part-time"
	case "contract":
		return "Contract"
	case "intern", "internship":
		return "Internship"
	default:
		return raw
	}
}

// devRelated decides whether a posting looks like a software/engineering
// role. NineHire's own job-group taxonomy is specific enough to allow-
// list directly (unlike Netflix's or Nvidia's, it doesn't visibly mix
// dev and non-dev titles within one group on inspection), but a title
// keyword check is ORed in too as the same safety net every other
// adapter's devRelated uses — either signal is enough to keep a posting.
var devJobGroups = map[string]bool{
	"백엔드 개발":       true,
	"프론트엔드 개발":     true,
	"DevOps":       true,
	"AI 엔지니어링":     true,
	"데이터 엔지니어링":    true,
	"데이터베이스 엔지니어링": true,
	"프로덕트 엔지니어링":   true,
}

var devTitleKeywords = []string{
	"개발자", "엔지니어", "engineer", "developer", "devops", "sre", "swe",
}

func devRelated(jobGroup, title string) bool {
	if devJobGroups[jobGroup] {
		return true
	}
	lower := strings.ToLower(title)
	for _, kw := range devTitleKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

func canonicalCareerType(raw string) string {
	switch raw {
	case "experienced":
		return "경력"
	case "new":
		return "신입"
	case "irrelevant", "not_matter":
		return "무관"
	default:
		return raw
	}
}
