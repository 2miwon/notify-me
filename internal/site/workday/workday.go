// Package workday implements site.Adapter for any company on a Workday
// "myworkdayjobs.com" career site — a shared ATS whose public career
// pages are backed by the same unauthenticated JSON ("cxs") API for
// every tenant:
//
//	POST https://<host>/wday/cxs/<tenant>/<site>/jobs
//	  {"appliedFacets": {}, "limit": 20, "offset": N, "searchText": "..."}
//	  → {"total": N, "jobPostings": [{title, externalPath, locationsText,
//	     postedOn, bulletFields}]}
//	GET  https://<host>/wday/cxs/<tenant>/<site><externalPath>
//	  → {"jobPostingInfo": {title, jobDescription (HTML), location,
//	     timeType, startDate, endDate, externalUrl, ...}}
//
// host/tenant/site are all visible in the company's own career URL, e.g.
// https://yanolja.wd102.myworkdayjobs.com/ko-KR/External_Yanolja →
// host "yanolja.wd102.myworkdayjobs.com", tenant "yanolja" (the first
// host label), site "External_Yanolja".
package workday

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	pageSize = 20 // the cxs API rejects anything larger
	maxPages = 50
)

// Adapter crawls one Workday career site.
type Adapter struct {
	Host    string
	Tenant  string
	SiteID  string
	Company string
	// Name overrides Name() (default: Tenant).
	SiteName string
	// SearchText is passed through as the site's own keyword search —
	// useful to keep a huge tenant's crawl small. Empty lists everything.
	SearchText string
	// Locations is a case-insensitive substring allow-list against the
	// listing's locationsText ("Seoul, Korea", "2 Locations", ...). A
	// multi-location posting only says "N Locations" there, so those are
	// checked against the detail's location + additionalLocations instead.
	Locations []string
	// DevOnly keeps only dev-looking titles (job.IsDevTitle) — the list
	// API exposes no category field.
	DevOnly bool

	client *http.Client
}

func New(host, tenant, siteID, company, siteName, searchText string, locations []string, devOnly bool) *Adapter {
	return &Adapter{
		Host: host, Tenant: tenant, SiteID: siteID, Company: company, SiteName: siteName,
		SearchText: searchText, Locations: locations, DevOnly: devOnly,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string {
	if a.SiteName != "" {
		return a.SiteName
	}
	return a.Tenant
}

func (a *Adapter) apiBase() string {
	return fmt.Sprintf("https://%s/wday/cxs/%s/%s", a.Host, a.Tenant, a.SiteID)
}

type listing struct {
	Title         string `json:"title"`
	ExternalPath  string `json:"externalPath"`
	LocationsText string `json:"locationsText"`
}

type detail struct {
	JobPostingInfo struct {
		ID                  string   `json:"id"`
		Title               string   `json:"title"`
		JobDescription      string   `json:"jobDescription"`
		Location            string   `json:"location"`
		AdditionalLocations []string `json:"additionalLocations"`
		TimeType            string   `json:"timeType"`
		StartDate           string   `json:"startDate"`
		EndDate             string   `json:"endDate"`
		ExternalURL         string   `json:"externalUrl"`
		JobReqID            string   `json:"jobReqId"`
	} `json:"jobPostingInfo"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.Host == "" || a.Tenant == "" || a.SiteID == "" {
		return nil, fmt.Errorf("workday: host/tenant/site not configured")
	}

	listings, err := a.fetchListings()
	if err != nil {
		return nil, fmt.Errorf("workday(%s): list: %w", a.Name(), err)
	}

	var postings []job.Posting
	for _, l := range listings {
		if a.DevOnly && !job.IsDevTitle(l.Title) {
			continue
		}
		multi := strings.HasSuffix(strings.ToLower(l.LocationsText), "locations")
		if !multi && !matchesAny([]string{l.LocationsText}, a.Locations) {
			continue
		}

		var d detail
		detailErr := a.doJSON(http.MethodGet, a.apiBase()+l.ExternalPath, nil, &d)
		info := d.JobPostingInfo
		if multi && len(a.Locations) > 0 {
			// Can't tell where a multi-location posting is without its
			// detail — keep it rather than silently drop it if that failed.
			if detailErr == nil && !matchesAny(append([]string{info.Location}, info.AdditionalLocations...), a.Locations) {
				continue
			}
		}

		p := job.Posting{
			Site:       a.Name(),
			ExternalID: l.ExternalPath[strings.LastIndex(l.ExternalPath, "_")+1:],
			Title:      strings.TrimSpace(l.Title),
			Company:    a.Company,
			URL:        fmt.Sprintf("https://%s/%s%s", a.Host, a.SiteID, l.ExternalPath),
			Location:   l.LocationsText,
			PostedAt:   time.Now().UTC(),
		}
		if detailErr == nil {
			if info.JobReqID != "" {
				p.ExternalID = info.JobReqID
			}
			if info.ExternalURL != "" {
				p.URL = info.ExternalURL
			}
			if info.Location != "" {
				p.Location = strings.Join(append([]string{info.Location}, info.AdditionalLocations...), " / ")
			}
			p.EmploymentType = employmentType(info.TimeType, info.Title)
			p.Description = htmlutil.StripToText(info.JobDescription)
			if t, err := time.Parse("2006-01-02", info.StartDate); err == nil {
				p.ApplicationStart = &t
			}
			if t, err := time.Parse("2006-01-02", info.EndDate); err == nil {
				p.ApplicationDeadline = &t
			}
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

func (a *Adapter) fetchListings() ([]listing, error) {
	var all []listing
	for page := 0; page < maxPages; page++ {
		body := map[string]any{
			"appliedFacets": map[string]any{},
			"limit":         pageSize,
			"offset":        page * pageSize,
			"searchText":    a.SearchText,
		}
		var resp struct {
			Total       int       `json:"total"`
			JobPostings []listing `json:"jobPostings"`
		}
		if err := a.doJSON(http.MethodPost, a.apiBase()+"/jobs", body, &resp); err != nil {
			return all, err
		}
		all = append(all, resp.JobPostings...)
		if len(resp.JobPostings) < pageSize {
			break
		}
	}
	return all, nil
}

func (a *Adapter) doJSON(method, url string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
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

func employmentType(timeType, title string) string {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "intern"), strings.Contains(title, "인턴"):
		return "Internship"
	case strings.Contains(lower, "contract"), strings.Contains(title, "계약직"):
		return "Contract"
	case strings.Contains(strings.ToLower(timeType), "full"):
		return "Full-time"
	default:
		return strings.TrimSpace(timeType)
	}
}
