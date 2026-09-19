// Package navercloud implements site.Adapter for NAVER Cloud Careers.
//
// NAVER Cloud uses the same family of recruitment software as NAVER, but it
// has its own domain and listing API. The API is public and returns the total
// number of live listings, which lets Fetch retrieve every page reliably.
package navercloud

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL    = "https://recruit.navercloudcorp.com/rcrt/loadJobList.do"
	detailURL  = "https://recruit.navercloudcorp.com/rcrt/view.do"
	userAgent  = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	pageSize   = 10
	dateLayout = "2006.01.02 15:04:05"
)

var employmentTypes = map[string]string{
	"정규": "Full-time",
	"계약": "Contract",
	"인턴": "Internship",
}

var workAreas = map[string]string{
	"0010": "분당",
	"0020": "서울",
	"0030": "춘천",
	"0040": "세종",
	"0050": "글로벌",
}

type Adapter struct {
	JobGroups []string
	client    *http.Client
}

func New(jobGroups []string) *Adapter {
	return &Adapter{JobGroups: jobGroups, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "navercloud" }

type listResponse struct {
	Result    string     `json:"result"`
	TotalSize int        `json:"totalSize"`
	List      []listItem `json:"list"`
}

type listItem struct {
	AnnoID         int    `json:"annoId"`
	AnnoSubject    string `json:"annoSubject"`
	SysCompanyCdNm string `json:"sysCompanyCdNm"`
	EntTypeCdNm    string `json:"entTypeCdNm"`
	EmpTypeCdNm    string `json:"empTypeCdNm"`
	ClassCdNm      string `json:"classCdNm"`
	WorkAreaCd     string `json:"workAreaCd"`
	StaYmdTime     string `json:"staYmdTime"`
	EndYmdTime     string `json:"endYmdTime"`
	JobDetailLink  string `json:"jobDetailLink"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	for firstIndex := 0; ; firstIndex += pageSize {
		page, err := a.fetchListPage(firstIndex)
		if err != nil {
			return postings, fmt.Errorf("navercloud: list page at firstIndex %d: %w", firstIndex, err)
		}
		if page.Result != "Y" {
			return postings, fmt.Errorf("navercloud: listing API returned result %q", page.Result)
		}
		if len(page.List) == 0 {
			break
		}

		for _, item := range page.List {
			if !matchesJobGroup(item.ClassCdNm, a.JobGroups) {
				continue
			}
			p := job.Posting{
				Site:           a.Name(),
				ExternalID:     strconv.Itoa(item.AnnoID),
				Title:          strings.TrimSpace(item.AnnoSubject),
				Company:        strings.TrimSpace(item.SysCompanyCdNm),
				URL:            detailLink(item),
				Location:       workAreas[item.WorkAreaCd],
				EmploymentType: canonicalEmploymentType(item.EmpTypeCdNm),
				CareerLevel:    strings.TrimSpace(item.EntTypeCdNm),
				PostedAt:       time.Now().UTC(),
			}
			if t, err := time.Parse(dateLayout, item.StaYmdTime); err == nil {
				p.ApplicationStart = &t
			}
			if t, err := time.Parse(dateLayout, item.EndYmdTime); err == nil {
				p.ApplicationDeadline = &t
			}
			if desc, err := a.fetchDescription(item.AnnoID); err == nil {
				p.Description = desc
			}
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}

		if firstIndex+len(page.List) >= page.TotalSize {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) fetchListPage(firstIndex int) (*listResponse, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s?firstIndex=%d", listURL, firstIndex), nil)
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
	return &out, nil
}

func (a *Adapter) fetchDescription(annoID int) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s?annoId=%d&lang=ko", detailURL, annoID), nil)
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
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	var lines []string
	doc.Find(".detail_wrap h1, .detail_wrap h2, .detail_wrap h3, .detail_wrap h4, .detail_wrap h5, .detail_wrap h6, .detail_wrap p, .detail_wrap li").Each(func(_ int, s *goquery.Selection) {
		s.Find("br").ReplaceWithHtml("\n")
		for _, line := range strings.Split(s.Text(), "\n") {
			if line = normalizeWhitespace(line); line != "" {
				lines = append(lines, line)
			}
		}
	})
	return strings.Join(lines, "\n"), nil
}

func canonicalEmploymentType(raw string) string {
	if canonical, ok := employmentTypes[strings.TrimSpace(raw)]; ok {
		return canonical
	}
	return strings.TrimSpace(raw)
}

func detailLink(item listItem) string {
	if item.JobDetailLink != "" {
		return item.JobDetailLink
	}
	return fmt.Sprintf("%s?annoId=%d&lang=ko", detailURL, item.AnnoID)
}

func normalizeWhitespace(s string) string {
	s = strings.ReplaceAll(s, string(rune(0xFEFF)), "")
	return strings.Join(strings.Fields(s), " ")
}

func matchesJobGroup(group string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(group), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}
