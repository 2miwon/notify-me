// Package allcon implements site.Adapter for 올콘 (all-con.co.kr), a
// Korean 공모전·대외활동 board. Its contest list page loads rows from an
// AJAX endpoint that takes the same filter form the page shows:
//
//	POST https://www.all-con.co.kr/page/ajax.contest_list.php
//	  t=1 (진행중) & page=N & ct[]=<분야> & tg[]=<응모대상> ...
//	  → {"totalPage", "rows": [{cl_srl, cl_title (an <a>), cl_host,
//	     cl_date "26.10.08~26.11.08", cl_status}]}
//	GET  https://www.all-con.co.kr/view/contest/<cl_srl>
//	  td.desc_* rows (주최/접수기간/분야/응모대상/시상내역) + div.board_body_txt
//
// Contests are stored with the same record shape as job postings (one
// pipeline, one app UI): Company = 주최, CareerLevel = 응모대상,
// EmploymentType = 시상내역, ApplicationStart/Deadline = 접수기간.
//
// Filter codes, from the page's own filter list:
//
//	분야 ct: 1 아이디어·창업·마케팅·네이밍  2 사진·영상  3 디자인·그림·웹툰
//	         4 문학·학술  5 과학·공학·학술  6 기타(예체능, e스포츠 등)
//	세부 sc (과학·공학·학술): 28 논문  29 과학  30 공학  31 개발  32 건축
//	         33 건설  34 인테리어  35 기타
//	대상 tg: 1 제한없음  2 일반인  3 대학생  4 대학원생  5 초등학생
//	         6 중학생  7 고등학생  8 주부 및 영유아  9 기타
package allcon

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL   = "https://www.all-con.co.kr"
	listURL   = baseURL + "/page/ajax.contest_list.php"
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	maxPages  = 30
)

var (
	kst         = time.FixedZone("KST", 9*60*60)
	tagPattern  = regexp.MustCompile(`<[^>]+>`)
	datePattern = regexp.MustCompile(`(\d{2})\.(\d{2})\.(\d{2})`)
	// The list title carries the site's "N 새글" new-post badge as text.
	newBadge       = regexp.MustCompile(`\s*(?:N\s*)?새글\s*$`)
	thousandsComma = regexp.MustCompile(`(\d),(\d)`)
)

// Adapter crawls all-con.co.kr's 진행중 contest list for the given filters.
type Adapter struct {
	// Categories are 분야 codes (ct[]), Subcategories 세부분야 codes (sc[]),
	// Targets 응모대상 codes (tg[]). Within each list the site ORs the
	// codes; empty means no restriction.
	Categories    []string
	Subcategories []string
	Targets       []string

	client *http.Client
}

func New(categories, subcategories, targets []string) *Adapter {
	return &Adapter{Categories: categories, Subcategories: subcategories, Targets: targets, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "allcon" }

type row struct {
	ID    string `json:"cl_srl"`
	Title string `json:"cl_title"`
	Host  string `json:"cl_host"`
	Date  string `json:"cl_date"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	var postings []job.Posting
	seen := map[string]bool{}
	for page := 1; page <= maxPages; page++ {
		rows, totalPages, err := a.fetchPage(page)
		if err != nil {
			return postings, fmt.Errorf("allcon: page %d: %w", page, err)
		}
		for _, r := range rows {
			if r.ID == "" || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			postings = append(postings, a.toPosting(r))
		}
		if page >= totalPages {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) fetchPage(page int) ([]row, int, error) {
	form := url.Values{
		"t":         {"1"},
		"page":      {fmt.Sprint(page)},
		"sortname":  {"cl_order"},
		"sortorder": {"asc"},
	}
	for _, c := range a.Categories {
		form.Add("ct[]", c)
	}
	for _, c := range a.Subcategories {
		form.Add("sc[]", c)
	}
	for _, t := range a.Targets {
		form.Add("tg[]", t)
	}
	req, err := http.NewRequest(http.MethodPost, listURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", baseURL+"/list/contest/1/1")
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var out struct {
		TotalPage json.Number `json:"totalPage"`
		Rows      []row       `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, err
	}
	total, _ := out.TotalPage.Int64()
	return out.Rows, int(total), nil
}

func (a *Adapter) toPosting(r row) job.Posting {
	p := job.Posting{
		Site:       a.Name(),
		ExternalID: r.ID,
		Title:      newBadge.ReplaceAllString(clean(r.Title), ""),
		Company:    clean(r.Host),
		URL:        fmt.Sprintf("%s/view/contest/%s", baseURL, r.ID),
		PostedAt:   time.Now().UTC(),
	}
	start, end := parsePeriod(r.Date)
	p.ApplicationStart, p.ApplicationDeadline = start, end

	if d, err := a.fetchDetail(p.URL); err == nil {
		if d.host != "" {
			p.Company = d.host
		}
		if s, e := parsePeriod(d.period); s != nil || e != nil {
			p.ApplicationStart, p.ApplicationDeadline = s, e
		}
		p.CareerLevel = selectSafe(d.target)
		p.EmploymentType = selectSafe(d.award)
		p.Description = d.summary()
	}
	return p
}

type detail struct {
	host, organizer, period, category, target, award, homepage, body string
}

// summary leads the description with the facts that don't fit a record
// field, so they're visible in the app's detail view.
func (d detail) summary() string {
	var lines []string
	for _, kv := range [][2]string{
		{"주최", d.host}, {"주관", d.organizer}, {"접수기간", d.period}, {"분야", d.category},
		{"응모대상", d.target}, {"시상내역", d.award}, {"접수처", d.homepage},
	} {
		if kv[1] != "" {
			lines = append(lines, kv[0]+": "+kv[1])
		}
	}
	if d.body != "" {
		lines = append(lines, "", d.body)
	}
	return strings.Join(lines, "\n")
}

func (a *Adapter) fetchDetail(pageURL string) (detail, error) {
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return detail{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.client.Do(req)
	if err != nil {
		return detail{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return detail{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return detail{}, err
	}
	text := func(sel string) string { return strings.Join(strings.Fields(doc.Find(sel).First().Text()), " ") }
	body, _ := doc.Find("div.board_body_txt").First().Html()
	homepage, _ := doc.Find("a.cl_web").First().Attr("href")
	return detail{
		host:      text("td.desc_host"),
		organizer: text("td.desc_organizer"),
		period:    text("td.desc_date"),
		category:  text("td.desc_cate"),
		target:    text("td.desc_target"),
		award:     text("td.desc_award"),
		homepage:  strings.TrimSpace(homepage),
		body:      htmlutil.StripToText(body),
	}, nil
}

// parsePeriod reads "26.10.21 ~ 26.11.15" (yy.mm.dd, KST). The deadline is
// the end of its day.
func parsePeriod(s string) (start, end *time.Time) {
	m := datePattern.FindAllStringSubmatch(s, 2)
	parse := func(g []string) *time.Time {
		t, err := time.ParseInLocation("06.01.02", g[1]+"."+g[2]+"."+g[3], kst)
		if err != nil {
			return nil
		}
		return &t
	}
	if len(m) >= 1 {
		start = parse(m[0])
	}
	if len(m) >= 2 {
		if t := parse(m[1]); t != nil {
			e := t.Add(24*time.Hour - time.Second)
			end = &e
		}
	}
	return start, end
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagPattern.ReplaceAllString(s, " "))), " ")
}

// selectSafe makes a value usable as a Notion select option: those can't
// contain commas and are capped at 100 characters.
func selectSafe(s string) string {
	// "3,700만원" → "3700만원"; list commas ("일반인, 대학생") → " ·".
	for thousandsComma.MatchString(s) {
		s = thousandsComma.ReplaceAllString(s, "$1$2")
	}
	s = strings.ReplaceAll(s, ",", " ·")
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 100 {
		s = string(r[:100])
	}
	return s
}
