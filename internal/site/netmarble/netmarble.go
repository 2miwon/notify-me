// Package netmarble implements site.Adapter for career.netmarble.com,
// which lists every Netmarble group company (넷마블, 넷마블네오, 넷마블에프앤씨,
// ...) from one public JSON API:
//
//	GET https://career.netmarble.com/api/v1/apply/announces?page=1&size=1000
//	  {"content": [{carAnnoId, companyNm, annoSubject, carJobGroupNm,
//	    carWorkGroupNm, entTypeNm, reqTypeNm, staDate, endDate, ...}], ...}
//	GET https://career.netmarble.com/api/v1/apply/announces/<id>/view
//	  {..., "annoContents": "<full standalone HTML document>"}
//
// endDate "2999-12-31 ..." (isUnlimitedEndDate) means 상시채용 — no deadline.
package netmarble

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL    = "https://career.netmarble.com"
	dateLayout = "2006-01-02 15:04:05"
)

var kst = time.FixedZone("KST", 9*60*60)

// Adapter crawls career.netmarble.com.
type Adapter struct {
	// DevOnly keeps job group "기술/AI" (minus policy/PM-type titles) plus
	// any title that reads like a dev role — see job.LooksDev. "개발지원"
	// is 개발PM/QA-style support work; it only passes on a dev title.
	DevOnly bool

	client *http.Client
}

func New(devOnly bool) *Adapter {
	return &Adapter{DevOnly: devOnly, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "netmarble" }

type announce struct {
	ID                 int64  `json:"carAnnoId"`
	CompanyName        string `json:"companyNm"`
	Subject            string `json:"annoSubject"`
	JobGroup           string `json:"carJobGroupNm"`
	WorkGroup          string `json:"carWorkGroupNm"`
	EmploymentType     string `json:"entTypeNm"`
	CareerType         string `json:"reqTypeNm"`
	StartDate          string `json:"staDate"`
	EndDate            string `json:"endDate"`
	IsUnlimitedEndDate bool   `json:"isUnlimitedEndDate"`
	Contents           string `json:"annoContents"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var list struct {
		Content []announce `json:"content"`
	}
	if err := a.getJSON(baseURL+"/api/v1/apply/announces?page=1&size=1000", &list); err != nil {
		return nil, fmt.Errorf("netmarble: list: %w", err)
	}

	var postings []job.Posting
	for _, an := range list.Content {
		if a.DevOnly && !job.LooksDev(an.Subject, an.JobGroup, an.WorkGroup) {
			continue
		}
		company := "넷마블"
		if an.CompanyName != "" {
			company = an.CompanyName
		}
		p := job.Posting{
			Site:           a.Name(),
			ExternalID:     fmt.Sprintf("%d", an.ID),
			Title:          strings.TrimSpace(an.Subject),
			Company:        company,
			URL:            fmt.Sprintf("%s/announce/view?anno_id=%d", baseURL, an.ID),
			EmploymentType: canonicalEmploymentType(an.EmploymentType),
			CareerLevel:    canonicalCareerType(an.CareerType),
			PostedAt:       time.Now().UTC(),
		}
		if t, err := time.ParseInLocation(dateLayout, an.StartDate, kst); err == nil {
			p.ApplicationStart = &t
		}
		if !an.IsUnlimitedEndDate && !strings.HasPrefix(an.EndDate, "2999") {
			if t, err := time.ParseInLocation(dateLayout, an.EndDate, kst); err == nil {
				p.ApplicationDeadline = &t
			}
		}

		// A failed detail fetch shouldn't cost the posting itself.
		var detail announce
		if err := a.getJSON(fmt.Sprintf("%s/api/v1/apply/announces/%d/view", baseURL, an.ID), &detail); err == nil {
			p.Description = describe(detail.Contents)
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

func (a *Adapter) getJSON(url string, v any) error {
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
	return json.NewDecoder(resp.Body).Decode(v)
}

func canonicalEmploymentType(raw string) string {
	switch strings.TrimSpace(raw) {
	case "정규직":
		return "Full-time"
	case "계약직":
		return "Contract"
	case "인턴":
		return "Internship"
	default:
		return strings.TrimSpace(raw)
	}
}

func canonicalCareerType(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "무관") {
		return "무관"
	}
	return raw
}

var hrefPattern = regexp.MustCompile(`href="(https?://[^"]+)"`)

// describe converts annoContents to text. Some postings (넷마블엔투's) are
// nothing but a banner image linking to an external Notion page — for
// those, keep the link so the posting's detail view isn't just empty.
func describe(contents string) string {
	text := htmlutil.StripToText(contents)
	if len([]rune(text)) >= 100 {
		return text
	}
	var links []string
	for _, m := range hrefPattern.FindAllStringSubmatch(contents, -1) {
		links = append(links, "상세 공고: "+m[1])
	}
	return strings.TrimSpace(strings.Join(append([]string{text}, links...), "\n"))
}
