// Package kream implements site.Adapter for recruit.kreamcorp.com, KREAM
// (네이버 자회사, 한정판 거래 플랫폼)'s own custom (non-SaaS) career site —
// a JSP-rendered page whose listing and detail are plain public JSON/
// HTML endpoints the page's own jQuery calls:
//
//	GET /rcrt/loadJobList.do?firstIndex=N
//	  {"result", "list": [{annoId, sysCompanyCdNm (company sub-brand:
//	    "KREAM" vs "KREAM Pay"), annoSubject (title), classCdNm
//	    (department), subJobCdNm (team), entTypeCdNm (경력/신입),
//	    empTypeCdNm (정규/계약/인턴), staYmdTime, endYmdTime
//	    ("2999.12.31 ..." = always-open), jobDetailLink}], "totalSize"}
//	GET /rcrt/view.do?annoId=<id>
//	  <div class="detail_box"><h4 class="detail_title">부서 소개</h4>
//	   <p class="detail_text">...</p></div> (repeated per section)
package kream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL    = "https://recruit.kreamcorp.com"
	dateLayout = "2006.01.02 15:04:05"
	// pageSize is whatever the endpoint returns per call regardless of
	// any page-size param passed to it — paginate by firstIndex instead.
	maxPages = 20
)

var kst = time.FixedZone("KST", 9*60*60)

// Adapter crawls recruit.kreamcorp.com.
type Adapter struct {
	// DevOnly keeps department "Tech" (minus policy/PM-type titles) plus
	// dev-looking titles elsewhere — see job.LooksDev.
	DevOnly bool

	client *http.Client
}

func New(devOnly bool) *Adapter {
	return &Adapter{DevOnly: devOnly, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "kream" }

type listing struct {
	AnnoID         int64  `json:"annoId"`
	SysCompanyName string `json:"sysCompanyCdNm"`
	Subject        string `json:"annoSubject"`
	ClassName      string `json:"classCdNm"`
	SubJobName     string `json:"subJobCdNm"`
	EntTypeName    string `json:"entTypeCdNm"`
	EmpTypeName    string `json:"empTypeCdNm"`
	StartAt        string `json:"staYmdTime"`
	EndAt          string `json:"endYmdTime"`
	DetailLink     string `json:"jobDetailLink"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	fetched := 0 // index into the server's own unfiltered list
	for page := 0; page < maxPages; page++ {
		var resp struct {
			List      []listing `json:"list"`
			TotalSize int       `json:"totalSize"`
		}
		if err := a.getJSON(fmt.Sprintf("%s/rcrt/loadJobList.do?firstIndex=%d", baseURL, fetched), &resp); err != nil {
			return postings, fmt.Errorf("kream: list at %d: %w", fetched, err)
		}
		if len(resp.List) == 0 {
			break
		}
		for _, l := range resp.List {
			if a.DevOnly && !job.LooksDev(l.Subject, l.ClassName, l.SubJobName) {
				continue
			}
			postings = append(postings, a.toPosting(l))
		}
		fetched += len(resp.List)
		if fetched >= resp.TotalSize {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) toPosting(l listing) job.Posting {
	company := "KREAM"
	if l.SysCompanyName != "" {
		company = l.SysCompanyName
	}
	p := job.Posting{
		Site:           "kream",
		ExternalID:     fmt.Sprintf("%d", l.AnnoID),
		Title:          strings.TrimSpace(l.Subject),
		Company:        company,
		URL:            l.DetailLink,
		EmploymentType: job.CanonicalKoreanEmploymentType(l.EmpTypeName),
		CareerLevel:    strings.TrimSpace(l.EntTypeName),
		PostedAt:       time.Now().UTC(),
	}
	if p.URL == "" {
		p.URL = fmt.Sprintf("%s/rcrt/view.do?annoId=%d", baseURL, l.AnnoID)
	}
	if t, err := time.ParseInLocation(dateLayout, l.StartAt, kst); err == nil {
		p.ApplicationStart = &t
	}
	if !strings.HasPrefix(l.EndAt, "2999") {
		if t, err := time.ParseInLocation(dateLayout, l.EndAt, kst); err == nil {
			p.ApplicationDeadline = &t
		}
	}
	if desc, err := a.fetchDescription(l.AnnoID); err == nil {
		p.Description = desc
	}
	p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
	return p
}

func (a *Adapter) fetchDescription(annoID int64) (string, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/rcrt/view.do?annoId=%d", baseURL, annoID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; notify-me-crawler/1.0)")
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

	var sections []string
	doc.Find("div.detail_box").Each(func(_ int, box *goquery.Selection) {
		heading := strings.TrimSpace(box.Find("h4.detail_title").First().Text())
		// The body's own markup nests <p> inside <p class="detail_text">
		// (invalid HTML), so the HTML5 parser auto-closes that outer <p>
		// on the first nested one, leaving it empty and the real content
		// as its following siblings — both still inside div.detail_box.
		// Read the whole box instead of just the (now-empty) p.detail_text.
		clone := box.Clone()
		clone.Find("h4").Remove()
		bodyHTML, _ := clone.Html()
		body := htmlutil.StripToText(bodyHTML)
		if heading == "" && body == "" {
			return
		}
		sections = append(sections, strings.TrimSpace(heading+"\n"+body))
	})
	return strings.Join(sections, "\n\n"), nil
}

func (a *Adapter) getJSON(url string, out any) error {
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
	return json.NewDecoder(resp.Body).Decode(out)
}
