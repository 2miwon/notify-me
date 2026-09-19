// Package wanted implements site.Adapter for wanted.co.kr.
//
// The listing page (https://www.wanted.co.kr/wdlist/<job_group_id>) is a
// Next.js app; the job cards are NOT present in the server-rendered HTML,
// they're fetched client-side. Rather than driving a headless browser, this
// adapter calls the same public JSON endpoint the page's frontend calls:
//
//	GET https://www.wanted.co.kr/api/chaos/navigation/v1/results
//	    ?job_group_id=<id>&country=all&job_sort=job.latest_order&limit=<n>&offset=<o>
//
// That endpoint needs no auth and no cookies, and returns plain JSON, so a
// normal net/http GET is sufficient — no goquery or chromedp needed for
// this particular site.
package wanted

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL   = "https://www.wanted.co.kr/api/chaos/navigation/v1/results"
	jobURLFmt = "https://www.wanted.co.kr/wd/%d"
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	pageSize  = 20
)

// Adapter crawls one Wanted job category (job_group_id), e.g. 518 for
// "개발" (Development). See https://www.wanted.co.kr/wdlist/<id> in a
// browser to find the ID for a different category.
type Adapter struct {
	JobGroupID int
	// MaxPages caps how many pages of `pageSize` results are fetched per
	// run, so a mis-set filter can't turn one crawl into an unbounded scrape.
	MaxPages int

	client *http.Client
}

func New(jobGroupID, maxPages int) *Adapter {
	return &Adapter{
		JobGroupID: jobGroupID,
		MaxPages:   maxPages,
		client:     &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *Adapter) Name() string { return "wanted" }

type listResponse struct {
	Data []struct {
		ID       int    `json:"id"`
		Position string `json:"position"`
		Company  struct {
			Name string `json:"name"`
		} `json:"company"`
		Address struct {
			Country  string `json:"country"`
			Location string `json:"location"`
		} `json:"address"`
	} `json:"data"`
	Links struct {
		Next *string `json:"next"`
	} `json:"links"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 15 * time.Second}
	}

	var postings []job.Posting
	offset := 0
	now := time.Now().UTC()

	for page := 0; a.MaxPages <= 0 || page < a.MaxPages; page++ {
		url := fmt.Sprintf("%s?job_group_id=%d&country=all&job_sort=job.latest_order&years=-1&limit=%d&offset=%d",
			baseURL, a.JobGroupID, pageSize, offset)

		resp, err := a.fetchPage(url)
		if err != nil {
			// Partial results are still useful: return what we have plus
			// the error, rather than discarding a successful earlier page.
			return postings, fmt.Errorf("wanted: page at offset %d: %w", offset, err)
		}

		if len(resp.Data) == 0 {
			break
		}

		for _, item := range resp.Data {
			loc := item.Address.Location
			if item.Address.Country != "" && loc != "" {
				loc = item.Address.Country + " " + loc
			} else if item.Address.Country != "" {
				loc = item.Address.Country
			}

			postings = append(postings, job.Posting{
				Site:       a.Name(),
				ExternalID: fmt.Sprintf("%d", item.ID),
				Title:      item.Position,
				Company:    item.Company.Name,
				URL:        fmt.Sprintf(jobURLFmt, item.ID),
				Location:   loc,
				PostedAt:   now,
			})
		}

		if resp.Links.Next == nil {
			break
		}
		offset += pageSize
	}

	return postings, nil
}

func (a *Adapter) fetchPage(url string) (*listResponse, error) {
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
