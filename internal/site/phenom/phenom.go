// Package phenom implements site.Adapter for career sites built on Phenom
// People (BCG: careers.bcg.com). The search page posts to the site's own
// /widgets endpoint, and each job page embeds its full record as JSON:
//
//	POST https://<host>/widgets
//	  {"ddoKey": "refineSearch", "lang": "en_global", "from": N, "size": 50,
//	   "selected_fields": {"country": ["Korea – Republic of Korea"]}, ...}
//	  → {"refineSearch": {"totalHits", "data": {"jobs": [{jobId, title,
//	     category, subCategory, type, location, postedDate}]}}}
//	GET  https://<host>/<lang path>/job/<jobId>
//	  <script>... phApp.ddo = {"jobDetail": {"data": {"job":
//	    {"description": "<html>", ...}}}}; ...</script>
//
// The country facet value must match the site's own spelling exactly —
// read it off the facet list on the search page.
package phenom

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	pageSize  = 50
	maxPages  = 20
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36"
)

// Adapter crawls one Phenom career site, filtered to one country facet.
type Adapter struct {
	SiteName string
	Company  string
	Host     string
	// Lang is the site's locale key, e.g. "en_global"; LangPath its URL
	// prefix, e.g. "global/en".
	Lang     string
	LangPath string
	Country  string
	// DevOnly keeps category/subCategory/title that look technical
	// (job.LooksDev).
	DevOnly bool

	client *http.Client
}

func New(siteName, company, host, lang, langPath, country string, devOnly bool) *Adapter {
	return &Adapter{
		SiteName: siteName, Company: company, Host: host, Lang: lang, LangPath: langPath,
		Country: country, DevOnly: devOnly, client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.SiteName }

type listing struct {
	JobID       string `json:"jobId"`
	Title       string `json:"title"`
	Category    string `json:"category"`
	SubCategory string `json:"subCategory"`
	Type        string `json:"type"`
	Location    string `json:"location"`
	PostedDate  string `json:"postedDate"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	var postings []job.Posting
	from := 0
	for page := 0; page < maxPages; page++ {
		jobs, total, err := a.search(from)
		if err != nil {
			return postings, fmt.Errorf("%s: search at %d: %w", a.SiteName, from, err)
		}
		for _, l := range jobs {
			if a.DevOnly && !job.LooksDev(l.Title, l.Category, l.SubCategory) {
				continue
			}
			postings = append(postings, a.toPosting(l))
		}
		from += len(jobs)
		if len(jobs) == 0 || from >= total {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) search(from int) ([]listing, int, error) {
	body, _ := json.Marshal(map[string]any{
		"lang": a.Lang, "deviceType": "desktop", "country": "global",
		"pageName": "search-results", "ddoKey": "refineSearch", "sortBy": "",
		"subsearch": "", "from": from, "jobs": true, "counts": false,
		"all_fields": []string{"country"}, "size": pageSize, "clearAll": false,
		"jdsource": "facets", "isSliderEnable": false, "pageId": "page20",
		"siteType": "external", "keywords": "", "global": true,
		"selected_fields": map[string][]string{"country": {a.Country}},
	})
	req, err := http.NewRequest(http.MethodPost, "https://"+a.Host+"/widgets", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
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
		RefineSearch struct {
			TotalHits int `json:"totalHits"`
			Data      struct {
				Jobs []listing `json:"jobs"`
			} `json:"data"`
		} `json:"refineSearch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, err
	}
	return out.RefineSearch.Data.Jobs, out.RefineSearch.TotalHits, nil
}

func (a *Adapter) toPosting(l listing) job.Posting {
	p := job.Posting{
		Site:           a.SiteName,
		ExternalID:     l.JobID,
		Title:          strings.TrimSpace(l.Title),
		Company:        a.Company,
		URL:            fmt.Sprintf("https://%s/%s/job/%s", a.Host, a.LangPath, l.JobID),
		Location:       l.Location,
		EmploymentType: job.CanonicalEmploymentType(strings.ToLower(strings.TrimSpace(l.Type))),
		PostedAt:       time.Now().UTC(),
	}
	if strings.Contains(strings.ToLower(l.Title), "intern") {
		p.EmploymentType = "Internship"
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000-0700", l.PostedDate); err == nil {
		p.ApplicationStart = &t
	}
	if desc, err := a.fetchDescription(p.URL); err == nil {
		p.Description = desc
	}
	p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
	return p
}

func (a *Adapter) fetchDescription(pageURL string) (string, error) {
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
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	const marker = "phApp.ddo = "
	i := bytes.Index(page, []byte(marker))
	if i < 0 {
		return "", fmt.Errorf("job data not found")
	}
	var ddo struct {
		JobDetail struct {
			Data struct {
				Job struct {
					Description string `json:"description"`
				} `json:"job"`
			} `json:"data"`
		} `json:"jobDetail"`
	}
	// The object is followed by more script; Decoder stops after it.
	if err := json.NewDecoder(bytes.NewReader(page[i+len(marker):])).Decode(&ddo); err != nil {
		return "", err
	}
	return htmlutil.StripToText(ddo.JobDetail.Data.Job.Description), nil
}
