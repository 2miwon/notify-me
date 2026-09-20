// Package naver implements site.Adapter for recruit.navercorp.com.
//
// The listing page (https://recruit.navercorp.com/rcrt/list.do?...) is
// server-rendered, but the site also exposes the exact JSON endpoint its
// own "load more" pagination calls:
//
//	GET https://recruit.navercorp.com/rcrt/loadJobList.do
//	    ?subJobCdArr=<comma-separated category codes>&firstIndex=<offset>
//
// That returns clean structured fields (title, company, employment type,
// career level, real application dates) with no auth needed, so this
// adapter hits it directly with net/http rather than scraping the list
// HTML. The category codes come from selecting filters on the list page
// and reading them back out of the resulting URL's subJobCdArr param.
//
// The per-posting detail page (rcrt/view.do?annoId=...) is genuinely
// static HTML with no equivalent JSON API, so its free-text body is
// scraped with goquery — this is the one part of this adapter matching
// the project's original "static pages: net/http + goquery" assumption.
package naver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL    = "https://recruit.navercorp.com/rcrt/loadJobList.do"
	detailURL  = "https://recruit.navercorp.com/rcrt/view.do"
	userAgent  = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	pageSize   = 10
	dateLayout = "2006.01.02 15:04:05"
)

// workAreaNames maps the site's work-area codes to their display names.
// Read off the "근무지" filter checkboxes on the list page; there are
// only five and they're effectively static (Naver's own campuses).
// sisterCompanyHosts maps a Naver-group affiliate's sysCompanyCdNm to its
// own separate careers domain. recruit.navercorp.com's Tech search
// cross-lists these affiliates' postings, but the jobDetailLink it
// returns for them still points at navercorp.com's own
// rcrt/view.do?annoId=..., which just falls back to a generic listing
// instead of the actual posting — confirmed live by visiting one. Each
// affiliate turns out to run the exact same view.do?annoId=... path on
// its own domain, so swapping just the host is enough; NAVER Cloud
// resolves to the same domain internal/site/navercloud already crawls
// directly, which is harmless (Store dedupes by URL either way).
var sisterCompanyHosts = map[string]string{
	"NAVER LABS":    "recruit.naverlabs.com",
	"NAVER WEBTOON": "recruit.webtoonscorp.com",
	"NAVER Cloud":   "recruit.navercloudcorp.com",
}

// sisterCompanyDetailLink rewrites link's host to the affiliate's own
// domain when companyName names one, leaving genuine NAVER links (and
// anything unrecognized) untouched.
func sisterCompanyDetailLink(link, companyName string) string {
	host, ok := sisterCompanyHosts[companyName]
	if !ok {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	u.Host = host
	return u.String()
}

var workAreaNames = map[string]string{
	"0010": "분당",
	"0020": "서울",
	"0030": "춘천",
	"0040": "세종",
	"0050": "글로벌",
}

// employmentTypeNames maps Naver's own employment-type labels onto the
// vocabulary internal/site/line already uses ("Full-time", "Contract",
// "Temporary") — LINE's, not Naver's, is the project's canonical set here,
// since without this, filtering "full-time only" across sites meant
// listing both "정규" and "Full-time" in config/keywords.yaml's
// employment_types. "Internship" doesn't otherwise appear on line, but
// naver genuinely distinguishes it from a temp/contract role, so it's
// kept as its own value rather than folded into "Temporary".
var employmentTypeNames = map[string]string{
	"정규": "Full-time",
	"계약": "Contract",
	"인턴": "Internship",
}

// canonicalEmploymentType looks up raw in employmentTypeNames, or passes
// it through unchanged if Naver ever reports a value not in that table —
// keeping unrecognized data visible (and still filterable on its own
// literal value) beats silently dropping it.
func canonicalEmploymentType(raw string) string {
	if mapped, ok := employmentTypeNames[raw]; ok {
		return mapped
	}
	return raw
}

// Adapter crawls recruit.navercorp.com, filtered to a set of sub-job
// category codes. To find codes for a different filter: open
// https://recruit.navercorp.com/rcrt/list.do, tick the categories you
// want, and read them back out of the URL's subJobCdArr query param.
type Adapter struct {
	SubJobCodes []string

	client *http.Client
}

func New(subJobCodes []string) *Adapter {
	return &Adapter{
		SubJobCodes: subJobCodes,
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *Adapter) Name() string { return "naver" }

type listResponse struct {
	Result    string     `json:"result"`
	TotalSize int        `json:"totalSize"`
	List      []listItem `json:"list"`
}

type listItem struct {
	AnnoID         int    `json:"annoId"`
	AnnoSubject    string `json:"annoSubject"`
	SysCompanyCdNm string `json:"sysCompanyCdNm"`
	EntTypeCdNm    string `json:"entTypeCdNm"` // career level: 신입/경력/무관
	EmpTypeCdNm    string `json:"empTypeCdNm"` // employment type: 정규/인턴/계약
	WorkAreaCd     string `json:"workAreaCd"`
	StaYmdTime     string `json:"staYmdTime"`
	EndYmdTime     string `json:"endYmdTime"`
	JobDetailLink  string `json:"jobDetailLink"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 15 * time.Second}
	}
	if len(a.SubJobCodes) == 0 {
		return nil, fmt.Errorf("naver: no sub job codes configured")
	}

	var postings []job.Posting
	firstIndex := 0

	for {
		resp, err := a.fetchListPage(firstIndex)
		if err != nil {
			return postings, fmt.Errorf("naver: list page at firstIndex %d: %w", firstIndex, err)
		}
		if len(resp.List) == 0 {
			break
		}

		for _, item := range resp.List {
			p := job.Posting{
				Site:           a.Name(),
				ExternalID:     strconv.Itoa(item.AnnoID),
				Title:          item.AnnoSubject,
				Company:        item.SysCompanyCdNm,
				URL:            sisterCompanyDetailLink(item.JobDetailLink, item.SysCompanyCdNm),
				Location:       workAreaNames[item.WorkAreaCd],
				EmploymentType: canonicalEmploymentType(item.EmpTypeCdNm),
				CareerLevel:    item.EntTypeCdNm,
				PostedAt:       time.Now().UTC(),
			}
			if t, err := time.Parse(dateLayout, item.StaYmdTime); err == nil {
				p.ApplicationStart = &t
			}
			if t, err := time.Parse(dateLayout, item.EndYmdTime); err == nil {
				p.ApplicationDeadline = &t
			}

			desc, err := a.fetchDescription(item.AnnoID)
			if err != nil {
				// A missing description is a lesser failure than losing
				// the posting entirely — keep it, just without the body.
				desc = ""
			}
			p.Description = desc
			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)

			postings = append(postings, p)
		}

		firstIndex += pageSize
		if firstIndex >= resp.TotalSize {
			break
		}
	}

	return postings, nil
}

func (a *Adapter) fetchListPage(firstIndex int) (*listResponse, error) {
	url := fmt.Sprintf("%s?subJobCdArr=%s&firstIndex=%d", listURL, strings.Join(a.SubJobCodes, ","), firstIndex)

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

	var out listResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &out, nil
}

// boilerplateLineMarkers: any line containing one of these gets dropped
// from the description. These are the recurring hiring-process/legal/HR
// notices — "전형절차: 서류 ▶ 1차 인터뷰 ▶ ... ▶ 최종합격", military
// service eligibility, veteran hiring preference, OFAC sanctions notices,
// "review results are sent by email" — that show up near-verbatim on
// almost every Naver posting regardless of team or job. They aren't
// specific to the job, just clutter.
//
// This is a line-level filter rather than "cut everything after the
// first heading that says X", because postings aren't consistently
// authored: the closing section's own heading text varies ("전형절차 및
// 기타사항" vs "전형절차 및 일정" vs no heading at all, just a bullet
// list run directly into the previous paragraph) enough that matching a
// specific heading string missed real cases in testing. Matching the
// boilerplate content itself is more robust than trying to find its
// section boundary.
var boilerplateLineMarkers = []string{
	"전형절차", "전형 절차",
	"레퍼런스체크", "레퍼런스 체크",
	"최종합격",
	"처우협의", "처우 협의",
	"국가유공자",
	"병역", "군필자", "면제자",
	"허위 기재", "허위사실",
	"OFAC", "Sanction", "제재",
	"1:1 문의", "FAQ",
	"조기 마감",
	"합격이 취소",
	"지원현황", "지원 철회",
	"기업문화 적합도 검사", "기업문화적합도 검사",
	"중복지원",
	"해외 출장에 결격",
	"수습 기간",
	"전형별 결과",
	"수신 오류",
	"스팸함",
}

// fetchDescription scrapes the detail page's free-text body, one line
// per heading/paragraph/list item (not one line per ".detail_box" — a
// box can itself contain several headings and paragraphs, and collapsing
// all of it with .Text() runs them together with no separators at all).
//
// Scoped to ".detail_wrap h1..h6, p, li" rather than ".detail_text"
// directly: Naver's CMS content puts another <p> right inside that
// <p class="detail_text">, and HTML5's parsing rules auto-close a <p> as
// soon as another <p> starts — so the real text ends up in anonymous
// sibling elements, not inside .detail_text itself, and a selector for
// .detail_text alone silently returns empty strings. Selecting p/li
// anywhere under .detail_wrap picks it up regardless of where the broken
// nesting put it.
func (a *Adapter) fetchDescription(annoID int) (string, error) {
	url := fmt.Sprintf("%s?annoId=%d&lang=ko", detailURL, annoID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
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
		return "", fmt.Errorf("parse detail page: %w", err)
	}

	var lines []string
	doc.Find(".detail_wrap h1, .detail_wrap h2, .detail_wrap h3, .detail_wrap h4, " +
		".detail_wrap h5, .detail_wrap h6, .detail_wrap p, .detail_wrap li").
		Each(func(_ int, s *goquery.Selection) {
			// Checked on the element's whole text, before elementLines
			// below splits it on <br>: some postings hard-wrap a single
			// boilerplate sentence across several manual <br>s (copied
			// from Word/HWP), which would otherwise split e.g. "기업문화
			// 적합도" and "검사" onto separate fragments and dodge the
			// marker match entirely. Matching per-element keeps a
			// boilerplate sentence recognizable regardless of how it's
			// line-wrapped, and drops it as a whole rather than leaving
			// stray fragments behind.
			whole := normalizeWhitespace(s.Text())
			if whole == "" || containsAny(whole, boilerplateLineMarkers) {
				return
			}
			lines = append(lines, elementLines(s)...)
		})

	return strings.Join(lines, "\n"), nil
}

// elementLines flattens one element (already confirmed not boilerplate)
// to plain-text lines. Some postings are authored with several bullet
// points typed inside one <p>, separated by manual <br> tags instead of
// separate <li>s — a plain .Text() call ignores <br> entirely and runs
// them all into one line, so this converts each <br> into a real line
// break first.
func elementLines(s *goquery.Selection) []string {
	s.Find("br").ReplaceWithHtml("\n")

	var lines []string
	for _, line := range strings.Split(s.Text(), "\n") {
		line = normalizeWhitespace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func containsAny(s string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// byteOrderMark is Unicode code point 0xFEFF, written as a bare rune
// constant (not a literal character in source) so no editor or tool can
// mangle it into a raw byte sequence.
const byteOrderMark = rune(0xFEFF)

func normalizeWhitespace(s string) string {
	// The BOM shows up throughout the CMS's WYSIWYG markup as a leftover
	// editor artifact, not meaningful content.
	s = strings.Map(func(r rune) rune {
		if r == byteOrderMark {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
