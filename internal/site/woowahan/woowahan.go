// Package woowahan implements site.Adapter for career.woowahan.com, the
// public careers site for 우아한형제들 (Baemin).
//
// The Vue frontend fetches job cards from its public endpoint:
//
//	GET https://career.woowahan.com/w1/recruits
//
// It is paginated and returns a normal, successful empty response when there
// are no matching postings, so the adapter distinguishes that from an API
// failure to keep the shared expiry sweep safe.
package woowahan

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL   = "https://career.woowahan.com/w1/recruits"
	jobURLFmt = "https://career.woowahan.com/recruitment/%s/detail"
	pageSize  = 21
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
)

// Adapter crawls the selected Woowahan job groups and job codes. Both are
// optional allow-lists and map directly to the public careers URL filters.
type Adapter struct {
	JobGroupCodes []string
	JobCodes      []string

	client *http.Client
}

func New(jobGroupCodes, jobCodes []string) *Adapter {
	return &Adapter{
		JobGroupCodes: jobGroupCodes,
		JobCodes:      jobCodes,
		client:        &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return "woowahan" }

type response struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		PageNumber      int        `json:"pageNumber"`
		TotalPageNumber int        `json:"totalPageNumber"`
		TotalSize       int        `json:"totalSize"`
		List            []listItem `json:"list"`
	} `json:"data"`
}

type listItem struct {
	RecruitNumber      string `json:"recruitNumber"`
	RecruitName        string `json:"recruitName"`
	RecruitEndDate     string `json:"recruitEndDate"`
	IsUnlimitedEndDate bool   `json:"isUnlimitedEndDate"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	now := time.Now()
	for page := 0; ; page++ {
		response, err := a.fetchPage(page)
		if err != nil {
			return postings, fmt.Errorf("woowahan: list page %d: %w", page+1, err)
		}
		for _, item := range response.Data.List {
			if item.RecruitNumber == "" || strings.TrimSpace(item.RecruitName) == "" {
				return postings, fmt.Errorf("woowahan: invalid listing on page %d", page+1)
			}
			deadline, hasDeadline := parseDeadline(item.RecruitEndDate)
			if !item.IsUnlimitedEndDate && hasDeadline && !deadline.After(now) {
				continue
			}
			p := job.Posting{
				Site:       a.Name(),
				ExternalID: item.RecruitNumber,
				Title:      strings.TrimSpace(item.RecruitName),
				Company:    "우아한형제들",
				URL:        fmt.Sprintf(jobURLFmt, url.PathEscape(item.RecruitNumber)),
				PostedAt:   time.Now().UTC(),
			}
			if !item.IsUnlimitedEndDate && hasDeadline {
				p.ApplicationDeadline = &deadline
			}
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title)
			postings = append(postings, p)
		}
		// The API uses zero-based page requests but reports pageNumber as
		// one-based. A valid empty result has totalPageNumber == 0.
		if response.Data.TotalPageNumber == 0 || response.Data.PageNumber >= response.Data.TotalPageNumber {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) fetchPage(page int) (*response, error) {
	query := url.Values{
		"recruitCampaignSeq":  {"0"},
		"jobGroupCodes":       {strings.Join(a.JobGroupCodes, ",")},
		"jobCodes":            {strings.Join(a.JobCodes, ",")},
		"careerPeriod":        {""},
		"keyword":             {""},
		"employmentTypeCodes": {""},
		"page":                {fmt.Sprint(page)},
		"size":                {fmt.Sprint(pageSize)},
		"sort":                {"updateDate,desc"},
	}
	req, err := http.NewRequest(http.MethodGet, listURL+"?"+query.Encode(), nil)
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

	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if out.Code != "2000" {
		return nil, fmt.Errorf("API error %s: %s", out.Code, out.Message)
	}
	if out.Data.PageNumber < 1 || out.Data.TotalPageNumber < 0 || out.Data.TotalSize < 0 {
		return nil, fmt.Errorf("response has invalid paging metadata")
	}
	return &out, nil
}

func parseDeadline(raw string) (time.Time, bool) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", time.RFC3339} {
		if parsed, err := time.ParseInLocation(layout, raw, time.FixedZone("KST", 9*60*60)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
