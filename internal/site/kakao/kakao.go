// Package kakao implements site.Adapter for careers.kakao.com.
//
// The public jobs page is client-rendered, but its own GET endpoint returns
// the full text and metadata for every card in one response:
//
//	GET https://careers.kakao.com/public/api/job-list
//
// This adapter follows all API pages and filters closed notices using the
// source's closeFlag/statusCode (and endDate when one is present).
package kakao

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL   = "https://careers.kakao.com/public/api/job-list"
	jobURLFmt = "https://careers.kakao.com/jobs/%s"
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
)

// Adapter crawls one Kakao careers search. The defaults match the Kakao
// corporation Technology listing; empty filter fields are sent as empty query
// parameters, which means "all" on the source website.
type Adapter struct {
	Company      string
	Part         string
	SkillSet     string
	EmployeeType string

	client *http.Client
}

func New(company, part, skillSet, employeeType string) *Adapter {
	if company == "" {
		company = "KAKAO"
	}
	if part == "" {
		part = "TECHNOLOGY"
	}
	return &Adapter{
		Company: company, Part: part, SkillSet: skillSet, EmployeeType: employeeType,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return "kakao" }

type listResponse struct {
	JobList       []listItem `json:"jobList"`
	TotalJobCount int        `json:"totalJobCount"`
	TotalPage     int        `json:"totalPage"`
}

type listItem struct {
	RealID              string  `json:"realId"`
	JobOfferID          int     `json:"jobOfferId"`
	JobOfferTitle       string  `json:"jobOfferTitle"`
	CompanyName         string  `json:"companyName"`
	LocationName        string  `json:"locationName"`
	EmployeeTypeName    string  `json:"employeeTypeName"`
	CloseFlag           bool    `json:"closeFlag"`
	UseFlag             bool    `json:"useFlag"`
	StatusCode          string  `json:"statusCode"`
	EndDate             *string `json:"endDate"`
	Introduction        string  `json:"introduction"`
	WorkContentDesc     string  `json:"workContentDesc"`
	Qualification       string  `json:"qualification"`
	JobOfferProcessDesc string  `json:"jobOfferProcessDesc"`
	KrewComment         string  `json:"krewComment"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	now := time.Now()
	for page := 1; ; page++ {
		response, err := a.fetchPage(page)
		if err != nil {
			return postings, fmt.Errorf("kakao: list page %d: %w", page, err)
		}
		for _, item := range response.JobList {
			if item.RealID == "" || item.JobOfferID == 0 || strings.TrimSpace(item.JobOfferTitle) == "" {
				return postings, fmt.Errorf("kakao: invalid listing on page %d", page)
			}
			deadline, hasDeadline := parseDate(item.EndDate)
			if item.CloseFlag || !item.UseFlag || item.StatusCode != "PROGRESS" || (hasDeadline && !deadline.After(now)) {
				continue
			}

			description := descriptionFor(item)
			p := job.Posting{
				Site:           a.Name(),
				ExternalID:     strconv.Itoa(item.JobOfferID),
				Title:          strings.TrimSpace(item.JobOfferTitle),
				Company:        strings.TrimSpace(item.CompanyName),
				URL:            fmt.Sprintf(jobURLFmt, url.PathEscape(item.RealID)),
				Location:       strings.TrimSpace(item.LocationName),
				EmploymentType: canonicalEmploymentType(item.EmployeeTypeName),
				PostedAt:       time.Now().UTC(),
				Description:    description,
			}
			if hasDeadline {
				p.ApplicationDeadline = &deadline
			}
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}

		if response.TotalPage < 1 || page >= response.TotalPage {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) fetchPage(page int) (*listResponse, error) {
	query := url.Values{
		"skillSet":     {a.SkillSet},
		"part":         {a.Part},
		"company":      {a.Company},
		"keyword":      {""},
		"employeeType": {a.EmployeeType},
		"page":         {strconv.Itoa(page)},
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

	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if out.TotalPage < 1 || out.TotalJobCount < 0 {
		return nil, fmt.Errorf("response has no valid paging metadata")
	}
	return &out, nil
}

func parseDate(raw *string) (time.Time, bool) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, *raw, time.FixedZone("KST", 9*60*60)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func canonicalEmploymentType(raw string) string {
	switch strings.TrimSpace(raw) {
	case "정규직":
		return "Full-time"
	case "계약직":
		return "Contract"
	case "인턴", "체험형 인턴", "채용연계형 인턴":
		return "Internship"
	default:
		return strings.TrimSpace(raw)
	}
}

func descriptionFor(item listItem) string {
	sections := []string{item.Introduction, item.WorkContentDesc, item.Qualification, item.JobOfferProcessDesc, item.KrewComment}
	var lines []string
	for _, section := range sections {
		if text := htmlToText(section); text != "" {
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n\n")
}

func htmlToText(source string) string {
	if source == "" {
		return ""
	}
	// The API mixes real HTML with escaped entities. Unescape first so
	// goquery sees both <br> and &lt;br&gt; as line boundaries.
	source = html.UnescapeString(source)
	source = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n").Replace(source)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(source))
	if err != nil {
		return strings.Join(strings.Fields(source), " ")
	}
	return strings.Join(strings.Fields(doc.Text()), " ")
}
