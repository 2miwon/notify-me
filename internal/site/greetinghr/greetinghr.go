// Package greetinghr implements site.Adapter for companies hosted on
// GreetingHR (그리팅) — a shared Korean ATS SaaS platform. Kakao Mobility
// and Kakao Pay's career sites are both instances of it; any other
// "<company>.career.greetinghr.com" site should work the same way.
//
// The site is a Next.js app whose listing page is server-rendered with
// the full result already embedded, so there's no need to reverse the
// separate `/_next/data/<buildId>/...` route (its buildId changes on
// every platform redeploy — fragile to depend on). Instead this parses
// the `__NEXT_DATA__` JSON blob straight out of the page's own HTML:
//
//	<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":
//	  {"dehydratedState":{"queries":[..., {"queryKey":["openings"], "state":
//	  {"data":[ {opening summary, including its location/career/employment
//	  fields}, ... ]}}]}}}}</script>
//
// The listing's query filter is baked into the URL path+query itself
// (each company's page builder names its own filter param differently —
// "jobs" vs "occupations" seen so far), so ListPath is passed through
// verbatim rather than reconstructed from parts; copy it from the
// company's own career site after applying whatever filter you want.
//
// Each opening's full description isn't in the listing payload, so it's
// fetched per-posting from /ko/o/<openingId>, whose own __NEXT_DATA__ has
// a `["career","getOpeningById",...]` query holding the HTML detail body.
package greetinghr

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"

// Adapter crawls one GreetingHR-hosted company's career site.
type Adapter struct {
	// Subdomain is the company's GreetingHR subdomain, e.g.
	// "kakaomobility" for kakaomobility.career.greetinghr.com. Also used
	// as this adapter's site identity (Name()), even when BaseURL
	// overrides where requests actually go.
	Subdomain string
	// BaseURL overrides the default "https://<subdomain>.career.
	// greetinghr.com" origin — some companies (Upstage, for one) put
	// their GreetingHR site behind their own custom domain instead
	// (careers.upstage.ai). Leave empty to use the default origin.
	BaseURL string
	// ListPath is the filtered listing page's path+query, exactly as
	// copied from the site after applying the desired filter, e.g.
	// "/ko/guide?jobs=%EA%B0%9C%EB%B0%9C".
	ListPath string
	// Company is the display name stored on each posting.
	Company string

	client *http.Client
}

func New(subdomain, baseURL, listPath, company string) *Adapter {
	return &Adapter{
		Subdomain: subdomain,
		BaseURL:   baseURL,
		ListPath:  listPath,
		Company:   company,
		client:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.Subdomain }

func (a *Adapter) baseURL() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return "https://" + a.Subdomain + ".career.greetinghr.com"
}

type openingSummary struct {
	OpeningID          int64   `json:"openingId"`
	Title              string  `json:"title"`
	OpenDate           string  `json:"openDate"`
	DueDate            *string `json:"dueDate"`
	OpeningJobPosition struct {
		OpeningJobPositions []struct {
			WorkspacePlace struct {
				Place string `json:"place"`
			} `json:"workspacePlace"`
			JobPositionCareer struct {
				CareerType string `json:"careerType"`
			} `json:"jobPositionCareer"`
			JobPositionEmployment struct {
				EmploymentType string `json:"employmentType"`
			} `json:"jobPositionEmployment"`
		} `json:"openingJobPositions"`
	} `json:"openingJobPosition"`
}

type openingDetail struct {
	Data struct {
		OpeningsInfo struct {
			Detail string `json:"detail"`
		} `json:"openingsInfo"`
	} `json:"data"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.Subdomain == "" {
		return nil, fmt.Errorf("greetinghr: no subdomain configured")
	}

	openings, err := a.fetchOpenings()
	if err != nil {
		return nil, fmt.Errorf("greetinghr(%s): list: %w", a.Subdomain, err)
	}

	var postings []job.Posting
	for _, o := range openings {
		p := job.Posting{
			Site:       a.Name(),
			ExternalID: fmt.Sprintf("%d", o.OpeningID),
			Title:      strings.TrimSpace(o.Title),
			Company:    a.Company,
			URL:        fmt.Sprintf("%s/ko/o/%d", a.baseURL(), o.OpeningID),
			PostedAt:   time.Now().UTC(),
		}
		if positions := o.OpeningJobPosition.OpeningJobPositions; len(positions) > 0 {
			pos := positions[0]
			p.Location = strings.TrimSpace(pos.WorkspacePlace.Place)
			p.EmploymentType = canonicalEmploymentType(pos.JobPositionEmployment.EmploymentType)
			p.CareerLevel = canonicalCareerType(pos.JobPositionCareer.CareerType)
		}
		if t, err := time.Parse(time.RFC3339, o.OpenDate); err == nil {
			p.ApplicationStart = &t
		}
		if o.DueDate != nil {
			if t, err := time.Parse(time.RFC3339, *o.DueDate); err == nil {
				p.ApplicationDeadline = &t
			}
		}

		// A failed detail fetch is a lesser failure than losing the
		// posting entirely — keep it with just the list fields.
		if desc, err := a.fetchDescription(o.OpeningID); err == nil {
			p.Description = desc
		}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)

		postings = append(postings, p)
	}

	return postings, nil
}

func (a *Adapter) fetchOpenings() ([]openingSummary, error) {
	html, err := a.getHTML(a.baseURL() + a.ListPath)
	if err != nil {
		return nil, err
	}

	raw, ok := extractNextDataQuery(html, "openings")
	if !ok {
		return nil, fmt.Errorf("openings query not found in __NEXT_DATA__")
	}
	var openings []openingSummary
	if err := json.Unmarshal(raw, &openings); err != nil {
		return nil, fmt.Errorf("decode openings: %w", err)
	}
	return openings, nil
}

func (a *Adapter) fetchDescription(openingID int64) (string, error) {
	html, err := a.getHTML(fmt.Sprintf("%s/ko/o/%d", a.baseURL(), openingID))
	if err != nil {
		return "", err
	}

	raw, ok := extractNextDataQuery(html, "career")
	if !ok {
		return "", fmt.Errorf("getOpeningById query not found in __NEXT_DATA__")
	}
	var detail openingDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return "", fmt.Errorf("decode opening detail: %w", err)
	}
	return htmlutil.StripToText(detail.Data.OpeningsInfo.Detail), nil
}

func (a *Adapter) getHTML(url string) (string, error) {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	return string(body), nil
}

// extractNextDataQuery finds the react-query cache entry inside the
// page's `__NEXT_DATA__` script whose queryKey's first element equals
// firstKey, and returns its raw `state.data` JSON.
func extractNextDataQuery(html, firstKey string) (json.RawMessage, bool) {
	const marker = `<script id="__NEXT_DATA__" type="application/json">`
	idx := strings.Index(html, marker)
	if idx == -1 {
		return nil, false
	}
	start := idx + len(marker)
	end := strings.Index(html[start:], "</script>")
	if end == -1 {
		return nil, false
	}
	blob := html[start : start+end]

	var envelope struct {
		Props struct {
			PageProps struct {
				DehydratedState struct {
					Queries []struct {
						QueryKey []json.RawMessage `json:"queryKey"`
						State    struct {
							Data json.RawMessage `json:"data"`
						} `json:"state"`
					} `json:"queries"`
				} `json:"dehydratedState"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal([]byte(blob), &envelope); err != nil {
		return nil, false
	}

	for _, q := range envelope.Props.PageProps.DehydratedState.Queries {
		if len(q.QueryKey) == 0 {
			continue
		}
		var key string
		if err := json.Unmarshal(q.QueryKey[0], &key); err != nil {
			continue
		}
		if key == firstKey {
			return q.State.Data, true
		}
	}
	return nil, false
}

func canonicalEmploymentType(raw string) string {
	switch raw {
	case "FULL_TIME_WORKER":
		return "Full-time"
	case "CONTRACT_WORKER":
		return "Contract"
	case "INTERN_WORKER", "INTERNSHIP_WORKER":
		return "Internship"
	case "TEMPORARY_WORKER":
		return "Temporary"
	case "MILITARY_SERVICE_EXCEPTION":
		// 전문연구요원 — a distinct Korean employment category (alternative
		// mandatory military service served as an R&D researcher), not a
		// normal full-time hire despite functioning like one day to day.
		return "전문연구요원"
	default:
		return raw
	}
}

func canonicalCareerType(raw string) string {
	switch raw {
	case "NOT_MATTER":
		return "무관"
	case "NEW_COMER":
		return "신입"
	case "EXPERIENCED":
		return "경력"
	default:
		return raw
	}
}
