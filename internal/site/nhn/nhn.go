// Package nhn implements site.Adapter for careers.nhn.com.
//
// NHN's React site exposes its own public JSON endpoints. Using those avoids
// browser rendering and provides the full Tech-group listing for expiry
// detection, while the per-posting endpoint supplies structured body sections.
package nhn

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	apiURL    = "https://careers.nhn.com/v1/job-postings"
	pageSize  = 100
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
)

type Adapter struct {
	JobGroupID string
	client     *http.Client
}

func New(jobGroupID string) *Adapter {
	return &Adapter{JobGroupID: jobGroupID, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "nhn" }

type responseHeader struct {
	IsSuccessful bool `json:"isSuccessful"`
}

type listResponse struct {
	Header responseHeader `json:"header"`
	Result []listing      `json:"result"`
}

type detailResponse struct {
	Header responseHeader `json:"header"`
	Result listing        `json:"result"`
}

type listing struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	FinishYn           string        `json:"finishYn"`
	PostingYn          string        `json:"postingYn"`
	PostingStaDatetime string        `json:"postingStaDatetime"`
	PostingEndDatetime string        `json:"postingEndDatetime"`
	Corporation        named         `json:"corporation"`
	CareerType         coded         `json:"careerType"`
	EmployeeType       coded         `json:"employeeType"`
	JobPostingContents []contentItem `json:"jobPostingContentsItems"`
}

type named struct {
	Name string `json:"name"`
}
type coded struct {
	Name string `json:"name"`
}

type contentItem struct {
	Title    string   `json:"title"`
	Contents []string `json:"contents"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if strings.TrimSpace(a.JobGroupID) == "" {
		return nil, fmt.Errorf("nhn: no job group ID configured")
	}
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	for page := 0; ; page++ {
		items, err := a.fetchListPage(page)
		if err != nil {
			return postings, fmt.Errorf("nhn: list page %d: %w", page, err)
		}
		for _, item := range items {
			// The endpoint normally only returns current postings, but retain
			// these checks so a temporarily cached closed record is not saved.
			if item.ID == "" || item.FinishYn == "Y" || item.PostingYn == "N" {
				continue
			}
			detail, err := a.fetchDetail(item.ID)
			if err == nil {
				item = detail
			}

			postedAt := parseTime(item.PostingStaDatetime)
			p := job.Posting{
				Site:           a.Name(),
				ExternalID:     item.ID,
				Title:          strings.TrimSpace(item.Name),
				Company:        strings.TrimSpace(item.Corporation.Name),
				URL:            "https://careers.nhn.com/recruits/" + item.ID,
				EmploymentType: employmentType(item.EmployeeType.Name),
				CareerLevel:    strings.TrimSpace(item.CareerType.Name),
				PostedAt:       postedAt,
				Description:    description(item.JobPostingContents),
			}
			if p.Company == "" {
				p.Company = "NHN"
			}
			if p.PostedAt.IsZero() {
				p.PostedAt = time.Now().UTC()
			} else {
				p.ApplicationStart = &postedAt
			}
			if deadline := parseDeadline(item.PostingEndDatetime); deadline != nil {
				p.ApplicationDeadline = deadline
			}
			p.Location = extractLocation(p.Description)
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}
		if len(items) < pageSize {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) fetchListPage(page int) ([]listing, error) {
	endpoint, err := url.Parse(apiURL)
	if err != nil {
		return nil, err
	}
	q := endpoint.Query()
	q.Set("jobGroupId", a.JobGroupID)
	q.Set("page", fmt.Sprint(page))
	q.Set("size", fmt.Sprint(pageSize))
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
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
	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Header.IsSuccessful {
		return nil, fmt.Errorf("API returned unsuccessful result")
	}
	return out.Result, nil
}

func (a *Adapter) fetchDetail(id string) (listing, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL+"/"+url.PathEscape(id), nil)
	if err != nil {
		return listing{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return listing{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return listing{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var out detailResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return listing{}, err
	}
	if !out.Header.IsSuccessful {
		return listing{}, fmt.Errorf("API returned unsuccessful result")
	}
	return out.Result, nil
}

func employmentType(raw string) string {
	switch strings.TrimSpace(raw) {
	case "정규", "정규직":
		return "Full-time"
	case "계약", "계약직":
		return "Contract"
	case "인턴", "인턴십":
		return "Internship"
	case "파견", "기간제":
		return "Temporary"
	default:
		return strings.TrimSpace(raw)
	}
}

// nhnDateLayout matches the API's own datetime strings (e.g.
// "2026-09-16T16:00:00") — no timezone offset, unlike time.RFC3339.
// Parsing these with RFC3339 always fails silently (parseTime discards
// the error), which meant PostedAt was never anything but "now" and
// ApplicationStart/ApplicationDeadline were never set at all. NHN's
// postings are all Korea-based (see internal/site/nhn's package doc),
// so treat the timestamp as KST.
const nhnDateLayout = "2006-01-02T15:04:05"

var kst = time.FixedZone("KST", 9*60*60)

func parseTime(value string) time.Time {
	t, err := time.ParseInLocation(nhnDateLayout, value, kst)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseDeadline(value string) *time.Time {
	t := parseTime(value)
	if t.IsZero() || t.Year() >= 2999 {
		return nil
	}
	return &t
}

func description(items []contentItem) string {
	var lines []string
	appendLine := func(value string) {
		value = htmlToText(value)
		for _, line := range strings.Split(value, "\n") {
			line = strings.Join(strings.Fields(line), " ")
			if line != "" && (len(lines) == 0 || lines[len(lines)-1] != line) {
				lines = append(lines, line)
			}
		}
	}
	for _, item := range items {
		appendLine(item.Title)
		for _, body := range item.Contents {
			appendLine(body)
		}
	}
	return strings.Join(lines, "\n")
}

func htmlToText(source string) string {
	if !strings.Contains(source, "<") {
		return source
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(source))
	if err != nil {
		return source
	}
	doc.Find("br").ReplaceWithHtml("\n")
	return doc.Text()
}

func extractLocation(body string) string {
	match := locationPattern.FindStringSubmatch(body)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

var locationPattern = regexp.MustCompile(`(?m)근무\s*(?:장소|지|지역)\s*[:：]\s*([^\r\n]+)`)
