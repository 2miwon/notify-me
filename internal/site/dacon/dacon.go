// Package dacon implements site.Adapter for DACON (dacon.io), a Korean
// AI/data-science competition platform. Its competition list page reads
// a public JSON API:
//
//	GET https://app.dacon.io/api/v1/competition/list?offset=N&range=100
//	  {"data": [{cpt_id, name, keyword ("알고리즘 | 정형 | ..."), prize_info,
//	    sponsor, period_start, period_end ("2026-11-02 10:00:00" KST),
//	    participants, user_count}]}
//
// The list includes finished competitions (newest first), so only ones
// whose period_end is still ahead are kept. Stored like job postings (see
// internal/site/allcon): Company = sponsor (or DACON), EmploymentType =
// 상금, ApplicationStart/Deadline = 대회 기간.
package dacon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL    = "https://app.dacon.io/api/v1/competition/list?offset=%d&range=%d"
	pageURL    = "https://dacon.io/competitions/official/%d/overview/"
	pageSize   = 100
	maxPages   = 5
	dateLayout = "2006-01-02 15:04:05"
)

var kst = time.FixedZone("KST", 9*60*60)

// Adapter crawls DACON's open competitions.
type Adapter struct {
	client *http.Client
	now    func() time.Time
}

func New() *Adapter {
	return &Adapter{client: &http.Client{Timeout: 20 * time.Second}, now: time.Now}
}

func (a *Adapter) Name() string { return "dacon" }

type competition struct {
	ID          int64  `json:"cpt_id"`
	Name        string `json:"name"`
	Keyword     string `json:"keyword"`
	PrizeInfo   string `json:"prize_info"`
	Sponsor     string `json:"sponsor"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
	UserCount   int    `json:"user_count"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.now == nil {
		a.now = time.Now
	}
	now := a.now()
	var postings []job.Posting
	for page := 0; page < maxPages; page++ {
		comps, err := a.fetchPage(page * pageSize)
		if err != nil {
			return postings, fmt.Errorf("dacon: offset %d: %w", page*pageSize, err)
		}
		live := 0
		for _, c := range comps {
			end, err := time.ParseInLocation(dateLayout, c.PeriodEnd, kst)
			if err != nil || !end.After(now) {
				continue
			}
			live++
			postings = append(postings, toPosting(c, end))
		}
		// Newest first: a full page with nothing still open means the
		// rest of the history is closed too.
		if len(comps) < pageSize || live == 0 {
			break
		}
	}
	return postings, nil
}

func toPosting(c competition, end time.Time) job.Posting {
	sponsor := strings.TrimSpace(c.Sponsor)
	if sponsor == "" {
		sponsor = "DACON"
	}
	p := job.Posting{
		Site:                "dacon",
		ExternalID:          fmt.Sprint(c.ID),
		Title:               strings.TrimSpace(c.Name),
		Company:             sponsor,
		URL:                 fmt.Sprintf(pageURL, c.ID),
		EmploymentType:      selectSafe(c.PrizeInfo),
		PostedAt:            time.Now().UTC(),
		ApplicationDeadline: &end,
	}
	if start, err := time.ParseInLocation(dateLayout, c.PeriodStart, kst); err == nil {
		p.ApplicationStart = &start
	}
	var lines []string
	for _, kv := range [][2]string{
		{"분야", c.Keyword}, {"상금", c.PrizeInfo},
		{"기간", c.PeriodStart + " ~ " + c.PeriodEnd},
		{"참가자", fmt.Sprintf("%d명", c.UserCount)},
	} {
		if strings.TrimSpace(kv[1]) != "" {
			lines = append(lines, kv[0]+": "+kv[1])
		}
	}
	p.Description = strings.Join(lines, "\n")
	return p
}

func (a *Adapter) fetchPage(offset int) ([]competition, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf(listURL, offset, pageSize), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; notify-me-crawler/1.0)")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	var out struct {
		Data []competition `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// selectSafe makes a value usable as a Notion select option (no commas,
// at most 100 characters): "4,200만 원" → "4200만 원".
func selectSafe(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if r := []rune(s); len(r) > 100 {
		s = string(r[:100])
	}
	if s == "-" {
		return ""
	}
	return s
}
