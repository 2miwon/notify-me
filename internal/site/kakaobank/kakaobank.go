// Package kakaobank implements site.Adapter for recruit.kakaobank.com.
//
// The public jobs page is a Next.js app. Its cards are loaded from the same
// JSON endpoints the page calls:
//
//	POST https://recruit.kakaobank.com/api/recruits
//	GET  https://recruit.kakaobank.com/api/recruits/<notice ID>
//
// The list response includes the actual application start and end timestamps,
// so the app can show KakaoBank's precise deadline rather than guessing it
// from when the crawler first saw the posting.
package kakaobank

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL       = "https://recruit.kakaobank.com/api/recruits"
	detailURLFmt  = "https://recruit.kakaobank.com/api/recruits/%d"
	jobURLFmt     = "https://recruit.kakaobank.com/jobs/%d"
	userAgent     = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	pageSize      = 20
	koreaDateTime = "2006-01-02 15:04:05"
)

var koreaLocation = time.FixedZone("KST", 9*60*60)

// Adapter crawls the public KakaoBank jobs list. Class names and employee
// types use the exact labels found in the jobs-page URL; both filters are
// optional and are applied by KakaoBank's own API.
type Adapter struct {
	RecruitClassNames    []string
	RecruitEmployeeTypes []string

	client *http.Client
}

func New(recruitClassNames, recruitEmployeeTypes []string) *Adapter {
	return &Adapter{
		RecruitClassNames:    recruitClassNames,
		RecruitEmployeeTypes: recruitEmployeeTypes,
		client:               &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return "kakaobank" }

type listRequest struct {
	PageNumber           int      `json:"pageNumber"`
	PageSize             int      `json:"pageSize"`
	RecruitClassNames    []string `json:"recruitClassNames,omitempty"`
	RecruitEmployeeTypes []string `json:"recruitEmployeeTypes,omitempty"`
}

type listResponse struct {
	List   []listItem `json:"list"`
	Paging *struct {
		PageNumber int `json:"pageNumber"`
		TotalPages int `json:"totalPages"`
	} `json:"paging"`
}

type listItem struct {
	RecruitNoticeSn      int    `json:"recruitNoticeSn"`
	RecruitNoticeName    string `json:"recruitNoticeName"`
	RecruitClassName     string `json:"recruitClassName"`
	RecruitEmployeeType  string `json:"recruitEmployeeTypeName"`
	ReceiveStartDatetime string `json:"receiveStartDatetime"`
	ReceiveEndDatetime   string `json:"receiveEndDatetime"`
}

type detailResponse struct {
	Contents string `json:"contents"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	now := time.Now()
	for pageNumber := 1; ; pageNumber++ {
		response, err := a.fetchListPage(pageNumber)
		if err != nil {
			return postings, fmt.Errorf("kakaobank: list page %d: %w", pageNumber, err)
		}

		for _, item := range response.List {
			if item.RecruitNoticeSn == 0 || strings.TrimSpace(item.RecruitNoticeName) == "" {
				return postings, fmt.Errorf("kakaobank: invalid listing on page %d", pageNumber)
			}
			deadline, deadlineErr := parseKoreaTime(item.ReceiveEndDatetime)
			// The endpoint retains old notices as well as live ones. Treat the
			// source's own deadline as authoritative, so a closed notice drops
			// out of liveURLs and is deleted by the common expiry sweep.
			if deadlineErr == nil && !deadline.After(now) {
				continue
			}

			p := job.Posting{
				Site:           a.Name(),
				ExternalID:     strconv.Itoa(item.RecruitNoticeSn),
				Title:          strings.TrimSpace(item.RecruitNoticeName),
				Company:        "카카오뱅크",
				URL:            fmt.Sprintf(jobURLFmt, item.RecruitNoticeSn),
				EmploymentType: canonicalEmploymentType(item.RecruitEmployeeType, item.RecruitNoticeName),
				// recruitClassName is a job taxonomy (Engineering, Server,
				// Core Banking), not a seniority/career level. KakaoBank's
				// public list API does not expose a separate career field, so
				// leave CareerLevel empty instead of polluting that filter.
				PostedAt: time.Now().UTC(),
			}
			if t, err := parseKoreaTime(item.ReceiveStartDatetime); err == nil {
				p.ApplicationStart = &t
			}
			if deadlineErr == nil {
				p.ApplicationDeadline = &deadline
			}

			description, err := a.fetchDescription(item.RecruitNoticeSn)
			if err == nil {
				p.Description = description
				p.Location = extractLocation(description)
			}
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}

		if response.Paging.PageNumber >= response.Paging.TotalPages {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) fetchListPage(pageNumber int) (*listResponse, error) {
	payload, err := json.Marshal(listRequest{
		PageNumber:           pageNumber,
		PageSize:             pageSize,
		RecruitClassNames:    a.RecruitClassNames,
		RecruitEmployeeTypes: a.RecruitEmployeeTypes,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, listURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://recruit.kakaobank.com")
	req.Header.Set("Referer", "https://recruit.kakaobank.com/jobs")

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
	// A valid empty listing still has paging metadata. Reject an API error
	// body (for example {"result":[{"message":"..."}]}) so it cannot
	// be mistaken for "every posting closed" by the expiry sweep.
	if out.Paging == nil || out.Paging.PageNumber < 1 || out.Paging.TotalPages < out.Paging.PageNumber {
		return nil, fmt.Errorf("response has no valid paging metadata")
	}
	return &out, nil
}

func (a *Adapter) fetchDescription(noticeID int) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf(detailURLFmt, noticeID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var out detailResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(out.Contents))
	if err != nil {
		return "", fmt.Errorf("parse description: %w", err)
	}
	return descriptionText(doc.Selection), nil
}

var locationPattern = regexp.MustCompile(`(?m)근무\s*(?:지|장소|지역)\s*[:：]\s*([^\r\n]+)`)

func extractLocation(body string) string {
	match := locationPattern.FindStringSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func parseKoreaTime(raw string) (time.Time, error) {
	return time.ParseInLocation(koreaDateTime, raw, koreaLocation)
}

func canonicalEmploymentType(raw, title string) string {
	switch strings.TrimSpace(raw) {
	case "정규직":
		return "Full-time"
	case "계약직":
		return "Contract"
	case "인턴", "체험형 인턴", "채용연계형 인턴":
		return "Internship"
	}
	if strings.Contains(title, "인턴") {
		return "Internship"
	}
	if strings.Contains(title, "계약직") {
		return "Contract"
	}
	return strings.TrimSpace(raw)
}

func normalizeWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// descriptionText keeps the meaningful structural breaks in KakaoBank's
// CMS HTML. goquery's Text() concatenates <br>, paragraphs, and list items,
// which turned an entire notice into a single line in the saved description.
func descriptionText(selection *goquery.Selection) string {
	var lines []string
	var current strings.Builder
	flush := func() {
		if text := normalizeWhitespace(current.String()); text != "" {
			lines = append(lines, text)
		}
		current.Reset()
	}
	appendText := func(text string) {
		text = normalizeWhitespace(text)
		if text == "" {
			return
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(text)
	}

	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			appendText(node.Data)
			return
		}
		if node.Type != html.ElementNode && node.Type != html.DocumentNode {
			return
		}
		name := strings.ToLower(node.Data)
		if name == "script" || name == "style" || name == "template" {
			return
		}
		if name == "br" {
			flush()
			return
		}
		if isBlockElement(name) {
			flush()
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if isBlockElement(name) {
			flush()
		}
	}

	selection.Each(func(_ int, s *goquery.Selection) {
		for _, node := range s.Nodes {
			walk(node)
		}
	})
	flush()
	return strings.Join(lines, "\n")
}

func isBlockElement(name string) bool {
	switch name {
	case "address", "article", "aside", "blockquote", "div", "dl", "fieldset",
		"figcaption", "figure", "footer", "form", "header", "h1", "h2", "h3",
		"h4", "h5", "h6", "li", "main", "nav", "ol", "p", "pre", "section",
		"table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}
