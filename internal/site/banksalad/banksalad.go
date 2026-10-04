// Package banksalad implements site.Adapter for 뱅크샐러드's careers page.
// Its GreetingHR home (banksalad.career.greetinghr.com/ko/home) is
// switched off, but corp.banksalad.com/jobs still renders postings from a
// public JSON proxy over GreetingHR, and each posting's own GreetingHR
// page works:
//
//	GET https://www.banksalad.com/proxy/api/greeting/openings
//	  {"jobs": [{"department": "테크", "data": [{id, title, job,
//	    employmentType, careerInfo{from, type}, url}]}]}
//
// Descriptions come from the GreetingHR opening page (see
// greetinghr.FetchOpening).
package banksalad

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/job"
	"github.com/2miwon/notify-me/internal/site/greetinghr"
)

const listURL = "https://www.banksalad.com/proxy/api/greeting/openings"

// Adapter crawls corp.banksalad.com/jobs.
type Adapter struct {
	// DevOnly keeps department "테크" and dev-looking titles (job.LooksDev).
	DevOnly bool

	client *http.Client
}

func New(devOnly bool) *Adapter {
	return &Adapter{DevOnly: devOnly, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "banksalad" }

type opening struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	Job            string `json:"job"`
	EmploymentType string `json:"employmentType"`
	CareerInfo     *struct {
		From *int   `json:"from"`
		Type string `json:"type"`
	} `json:"careerInfo"`
	URL string `json:"url"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://corp.banksalad.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; notify-me-crawler/1.0)")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("banksalad: list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("banksalad: list: unexpected status %d", resp.StatusCode)
	}
	var body struct {
		Jobs []struct {
			Department string    `json:"department"`
			Data       []opening `json:"data"`
		} `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("banksalad: decode list: %w", err)
	}

	var postings []job.Posting
	for _, group := range body.Jobs {
		for _, o := range group.Data {
			if a.DevOnly && !job.LooksDev(o.Title, group.Department, o.Job) {
				continue
			}
			p := job.Posting{
				Site:           a.Name(),
				ExternalID:     fmt.Sprintf("%d", o.ID),
				Title:          strings.TrimSpace(o.Title),
				Company:        "뱅크샐러드",
				URL:            o.URL,
				EmploymentType: job.CanonicalKoreanEmploymentType(o.EmploymentType),
				PostedAt:       time.Now().UTC(),
			}
			if o.CareerInfo != nil {
				switch o.CareerInfo.Type {
				case "NOT_MATTER":
					p.CareerLevel = "무관"
				case "NEW_COMER":
					p.CareerLevel = "신입"
				case "EXPERIENCED":
					p.CareerLevel = "경력"
				}
			}
			opening, _ := greetinghr.FetchOpening(a.client, p.URL)
			p.Description = opening.Description
			p.MinYearsExperience = opening.YearsOrExtract(p.Title)
			postings = append(postings, p)
		}
	}
	return postings, nil
}
