// Package pcsx implements site.Adapter for any company on Eightfold's
// "PCSX" career-site product — the same platform internal/site/nvidia and
// internal/site/microsoft were written against, parametrized by host and
// domain so another company (Qualcomm: careers.qualcomm.com) needs only
// config:
//
//	GET https://<host>/api/pcsx/search?domain=<domain>&query=<q>&location=<loc>&start=<n>
//	GET https://<host>/api/pcsx/position_details?position_id=<id>&domain=<domain>&hl=en
//
// The API 403s ("Not authorized for PCSX") without a Referer from the
// site's own origin.
package pcsx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"

// Adapter crawls one PCSX career site for one search query/location pair.
// Find values by searching on the company's own careers page and reading
// them back out of the request it sends.
type Adapter struct {
	SiteName string
	Company  string
	// Host is the career site's hostname, e.g. "careers.qualcomm.com".
	Host string
	// Domain is the `domain` query param the site's own search sends.
	Domain   string
	Query    string
	Location string
	// DevOnly keeps postings whose department or title looks technical
	// (job.LooksDev).
	DevOnly bool

	client *http.Client
}

func New(siteName, company, host, domain, query, location string, devOnly bool) *Adapter {
	return &Adapter{
		SiteName: siteName, Company: company, Host: host, Domain: domain,
		Query: query, Location: location, DevOnly: devOnly,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.SiteName }

func (a *Adapter) baseURL() string { return "https://" + a.Host }

func (a *Adapter) referer() string {
	return a.baseURL() + "/careers?domain=" + a.Domain
}

type searchResponse struct {
	Data struct {
		Positions []position `json:"positions"`
		Count     int        `json:"count"`
	} `json:"data"`
}

type position struct {
	ID           int64    `json:"id"`
	DisplayJobID string   `json:"displayJobId"`
	Name         string   `json:"name"`
	Locations    []string `json:"locations"`
	Department   string   `json:"department"`
	PostedTs     int64    `json:"postedTs"`
	PositionURL  string   `json:"positionUrl"`
}

type detailResponse struct {
	Data struct {
		JobDescription             string   `json:"jobDescription"`
		PublicURL                  string   `json:"publicUrl"`
		EfcustomTextEmploymentType []string `json:"efcustomTextEmploymentType"`
	} `json:"data"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	var postings []job.Posting
	start := 0

	for {
		resp, err := a.fetchSearchPage(start)
		if err != nil {
			return postings, fmt.Errorf("%s: search at start %d: %w", a.SiteName, start, err)
		}
		if len(resp.Data.Positions) == 0 {
			break
		}

		for _, pos := range resp.Data.Positions {
			if a.DevOnly && !job.LooksDev(pos.Name, pos.Department) {
				continue
			}
			p := job.Posting{
				Site:       a.Name(),
				ExternalID: strconv.FormatInt(pos.ID, 10),
				Title:      pos.Name,
				Company:    a.Company,
				URL:        a.baseURL() + pos.PositionURL,
				Location:   strings.Join(pos.Locations, "; "),
				PostedAt:   time.Now().UTC(),
			}
			if pos.PostedTs > 0 {
				t := time.Unix(pos.PostedTs, 0).UTC()
				p.ApplicationStart = &t
			}

			// A failed detail fetch is a lesser failure than losing the
			// posting entirely — keep it with just the search fields.
			if detail, err := a.fetchDetail(pos.ID); err == nil {
				p.Description = htmlutil.StripToText(detail.Data.JobDescription)
				if detail.Data.PublicURL != "" {
					p.URL = detail.Data.PublicURL
				}
				if len(detail.Data.EfcustomTextEmploymentType) > 0 {
					p.EmploymentType = job.CanonicalEmploymentType(detail.Data.EfcustomTextEmploymentType[0])
				}
			}

			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}

		start += len(resp.Data.Positions)
		if start >= resp.Data.Count {
			break
		}
	}

	return postings, nil
}

func (a *Adapter) fetchSearchPage(start int) (*searchResponse, error) {
	query := url.Values{}
	query.Set("domain", a.Domain)
	query.Set("query", a.Query)
	query.Set("location", a.Location)
	query.Set("start", strconv.Itoa(start))

	var out searchResponse
	if err := a.getJSON(a.baseURL()+"/api/pcsx/search?"+query.Encode(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (a *Adapter) fetchDetail(positionID int64) (*detailResponse, error) {
	query := url.Values{}
	query.Set("position_id", strconv.FormatInt(positionID, 10))
	query.Set("domain", a.Domain)
	query.Set("hl", "en")

	var out detailResponse
	if err := a.getJSON(a.baseURL()+"/api/pcsx/position_details?"+query.Encode(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// getJSON retries on 429 — see internal/site/microsoft's getJSON doc:
// same platform, same rate-limit-under-load risk when fetching one
// detail page per posting with no pacing.
func (a *Adapter) getJSON(url string, out interface{}) error {
	const maxAttempts = 4
	backoff := 500 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := a.getJSONOnce(url, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxAttempts {
			break
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	return lastErr
}

type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("unexpected status %d", e.code) }

func isRetryable(err error) bool {
	var se *statusError
	if !errors.As(err, &se) {
		return false
	}
	return se.code == http.StatusTooManyRequests || se.code >= 500
}

func (a *Adapter) getJSONOnce(url string, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", a.referer())

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
