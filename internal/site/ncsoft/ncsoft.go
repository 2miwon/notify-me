// Package ncsoft implements site.Adapter for careers.ncsoft.com (NC and
// its subsidiaries). The listing is a jQuery page that POSTs to its own
// JSON endpoint, guarded by a Spring Security session + CSRF token — so
// Fetch first GETs the list page to pick up both, then replays the page's
// own request:
//
//	GET  https://careers.ncsoft.com/apply/list
//	  Set-Cookie: nextrct-web-session=...
//	  <meta name="_csrf" content="<token>" />
//	POST https://careers.ncsoft.com/interface/apply/list
//	  X-CSRF-TOKEN: <token>
//	  order_type=ORDER_D&order_direction=asc&page=1&pagesize=...
//	  {"result": {"data": {"record_count": N, "record": [{jopenId,
//	    regOpId, jopenNm, jobTypeName, jobName, channelNm, empTypeCd,
//	    startDt, endDt, ...}]}}}
//
// order_type must be ORDER_D (deadline) — ORDER_R 404s. The detail page
// (/apply/view/?companyId=<regOpId>&jopenId=<id>&regOpId=<regOpId>) is plain server-rendered
// HTML with the body in <article class="contents">.
package ncsoft

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL    = "https://careers.ncsoft.com"
	userAgent  = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	dateLayout = "2006.01.02"
	pageSize   = 200
)

var (
	kst       = time.FixedZone("KST", 9*60*60)
	csrfMatch = regexp.MustCompile(`name="_csrf"\s+content=\s*"([^"]+)"`)
	// devJobTypes are NC job types that are engineering outright. "AI R&D"
	// and "System Administration" also hold translators, data-labeling
	// assistants and IT planners, so those go through job.LooksDev with
	// the finer-grained jobName ("AI Engineering", "Vision AI Research")
	// as the category instead.
	devJobTypes = map[string]bool{
		"Game Programming": true,
	}
)

// Adapter crawls careers.ncsoft.com.
type Adapter struct {
	DevOnly bool

	client *http.Client
}

func New(devOnly bool) *Adapter {
	return &Adapter{DevOnly: devOnly}
}

func (a *Adapter) Name() string { return "ncsoft" }

type record struct {
	JopenID     int64  `json:"jopenId"`
	RegOpID     string `json:"regOpId"`
	Title       string `json:"jopenNm"`
	JobTypeName string `json:"jobTypeName"`
	JobName     string `json:"jobName"`
	ChannelName string `json:"channelNm"`
	EmpTypeCd   string `json:"empTypeCd"`
	CompanyName string `json:"companyNm"`
	StartDt     string `json:"startDt"`
	EndDt       string `json:"endDt"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	jar, _ := cookiejar.New(nil)
	a.client = &http.Client{Timeout: 20 * time.Second, Jar: jar}

	listPage, err := a.get(baseURL + "/apply/list")
	if err != nil {
		return nil, fmt.Errorf("ncsoft: list page: %w", err)
	}
	m := csrfMatch.FindStringSubmatch(listPage)
	if m == nil {
		return nil, fmt.Errorf("ncsoft: csrf token not found on list page")
	}
	records, err := a.fetchRecords(m[1])
	if err != nil {
		return nil, fmt.Errorf("ncsoft: list: %w", err)
	}

	var postings []job.Posting
	for _, r := range records {
		title := strings.Join(strings.Fields(r.Title), " ")
		if a.DevOnly && !devJobTypes[r.JobTypeName] && !job.LooksDev(title, r.JobName) {
			continue
		}
		company := "엔씨소프트"
		if r.CompanyName != "" && r.CompanyName != "NC" {
			company = r.CompanyName
		}
		p := job.Posting{
			Site:       a.Name(),
			ExternalID: fmt.Sprintf("%d", r.JopenID),
			Title:      title,
			Company:    company,
			// regOpId is required for subsidiary postings (NCAI, FSG, ...);
			// without it the page only works for NC itself (NCH).
			URL:            fmt.Sprintf("%s/apply/view/?companyId=%[2]s&jopenId=%[3]d&regOpId=%[2]s", baseURL, url.QueryEscape(r.RegOpID), r.JopenID),
			EmploymentType: employmentType(r.EmpTypeCd),
			CareerLevel:    careerLevel(r.ChannelName),
			PostedAt:       time.Now().UTC(),
		}
		if t, err := time.ParseInLocation(dateLayout, r.StartDt, kst); err == nil {
			p.ApplicationStart = &t
		}
		if t, err := time.ParseInLocation(dateLayout, r.EndDt, kst); err == nil {
			p.ApplicationDeadline = &t
		}
		if desc, err := a.fetchDescription(p.URL); err == nil {
			p.Description = desc
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

func (a *Adapter) fetchRecords(csrf string) ([]record, error) {
	form := url.Values{
		"order_type":      {"ORDER_D"},
		"order_direction": {"asc"},
		"pagesize":        {fmt.Sprintf("%d", pageSize)},
		"channelCds":      {""},
		"keywords":        {""},
		"job_group_cd":    {""},
		"search_text":     {""},
		"job_type_cd":     {""},
		"companyIds":      {""},
	}
	var all []record
	for page := 1; page <= 20; page++ {
		form.Set("page", fmt.Sprintf("%d", page))
		req, err := http.NewRequest(http.MethodPost, baseURL+"/interface/apply/list", strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("X-CSRF-TOKEN", csrf)
		req.Header.Set("Referer", baseURL+"/apply/list")

		resp, err := a.client.Do(req)
		if err != nil {
			return nil, err
		}
		var body struct {
			Result struct {
				Data struct {
					RecordCount int      `json:"record_count"`
					Record      []record `json:"record"`
				} `json:"data"`
			} `json:"result"`
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}
		all = append(all, body.Result.Data.Record...)
		if len(body.Result.Data.Record) < pageSize || len(all) >= body.Result.Data.RecordCount {
			break
		}
	}
	return all, nil
}

func (a *Adapter) fetchDescription(pageURL string) (string, error) {
	page, err := a.get(pageURL)
	if err != nil {
		return "", err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		return "", err
	}
	html, err := doc.Find("article.contents").First().Html()
	if err != nil {
		return "", err
	}
	return htmlutil.StripToText(html), nil
}

func (a *Adapter) get(pageURL string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
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
	return string(body), err
}

// employmentType maps NC's empTypeCd: 20 정규직, 30 계약직, 40 단기계약직.
func employmentType(code string) string {
	switch code {
	case "20":
		return "Full-time"
	case "30", "40":
		return "Contract"
	default:
		return ""
	}
}

// careerLevel maps channelNm ("신입"/"경력"/"단기" — 단기 is a contract
// channel, not a career level, so it's left blank).
func careerLevel(channel string) string {
	switch strings.TrimSpace(channel) {
	case "신입", "경력":
		return strings.TrimSpace(channel)
	default:
		return ""
	}
}
