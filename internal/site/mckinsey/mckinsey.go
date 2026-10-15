// Package mckinsey implements site.Adapter for McKinsey & Company's
// careers search (mckinsey.com/careers/search-jobs), which reads a public
// JSON API through McKinsey's own gateway:
//
//	GET https://gateway.mckinsey.com/apigw-x0cceuow60/v1/api/jobs/search
//	    ?pageSize=100&start=1&lang=en&cities=Seoul
//	  {"numFound", "docs": [{jobID, title, friendlyURL, interest,
//	    functions, cities, whatYouWillDo, whoYouWillWorkWith,
//	    yourBackground}]}
//
// `cities` takes the names from the API's own /jobs/masterdata list
// ("Seoul" for South Korea). Global programs (Associate, Business
// Analyst, ...) are one posting listing 100+ cities, Seoul among them.
package mckinsey

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	searchURL = "https://gateway.mckinsey.com/apigw-x0cceuow60/v1/api/jobs/search"
	jobURL    = "https://www.mckinsey.com/careers/search-jobs/jobs/"
	pageSize  = 100
	maxPages  = 20
)

// Adapter crawls McKinsey's job search for one city.
type Adapter struct {
	City string
	// DevOnly keeps titles/functions that look technical (job.LooksDev) —
	// QuantumBlack data roles, Tech & AI architects — rather than the
	// generalist consulting programs.
	DevOnly bool

	client *http.Client
}

func New(city string, devOnly bool) *Adapter {
	return &Adapter{City: city, DevOnly: devOnly, client: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Adapter) Name() string { return "mckinsey" }

type doc struct {
	JobID              string   `json:"jobID"`
	Title              string   `json:"title"`
	FriendlyURL        string   `json:"friendlyURL"`
	Interest           string   `json:"interest"`
	Functions          []string `json:"functions"`
	Cities             []string `json:"cities"`
	WhatYouWillDo      string   `json:"whatYouWillDo"`
	WhoYouWillWorkWith string   `json:"whoYouWillWorkWith"`
	YourBackground     string   `json:"yourBackground"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	var postings []job.Posting
	start := 1
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("pageSize", fmt.Sprint(pageSize))
		q.Set("start", fmt.Sprint(start))
		q.Set("lang", "en")
		q.Set("cities", a.City)
		var resp struct {
			NumFound int   `json:"numFound"`
			Docs     []doc `json:"docs"`
		}
		if err := a.getJSON(searchURL+"?"+q.Encode(), &resp); err != nil {
			return postings, fmt.Errorf("mckinsey: search at %d: %w", start, err)
		}
		for _, d := range resp.Docs {
			categories := append([]string{d.Interest}, d.Functions...)
			if a.DevOnly && !job.LooksDev(d.Title, categories...) {
				continue
			}
			postings = append(postings, a.toPosting(d))
		}
		start += len(resp.Docs)
		if len(resp.Docs) == 0 || start > resp.NumFound {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) toPosting(d doc) job.Posting {
	location := a.City
	if len(d.Cities) > 1 {
		location = fmt.Sprintf("%s (+%d cities)", a.City, len(d.Cities)-1)
	}
	var sections []string
	for _, s := range []struct{ heading, html string }{
		{"Who you'll work with", d.WhoYouWillWorkWith},
		{"What you'll do", d.WhatYouWillDo},
		{"Your background", d.YourBackground},
	} {
		if text := htmlutil.StripToText(s.html); text != "" {
			sections = append(sections, s.heading+"\n"+text)
		}
	}
	p := job.Posting{
		Site:        a.Name(),
		ExternalID:  d.JobID,
		Title:       strings.TrimSpace(d.Title),
		Company:     "McKinsey & Company",
		URL:         jobURL + d.FriendlyURL,
		Location:    location,
		PostedAt:    time.Now().UTC(),
		Description: strings.Join(sections, "\n\n"),
	}
	if strings.Contains(strings.ToLower(d.Title), "intern") {
		p.EmploymentType = "Internship"
	}
	p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
	return p
}

func (a *Adapter) getJSON(target string, out any) error {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://www.mckinsey.com")
	req.Header.Set("Referer", "https://www.mckinsey.com/careers/search-jobs")
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
