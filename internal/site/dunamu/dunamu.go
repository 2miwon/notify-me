// Package dunamu implements site.Adapter for Dunamu's Engineering careers.
package dunamu

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const listURL = "https://www.dunamu.com/careers/jobs?category=engineering"

type Adapter struct{ client *http.Client }

func New() *Adapter             { return &Adapter{client: &http.Client{Timeout: 30 * time.Second}} }
func (a *Adapter) Name() string { return "dunamu" }

type listPage struct {
	Props struct {
		PageProps struct {
			Articles struct {
				Content []struct {
					ID           int    `json:"id"`
					CategoryKind string `json:"categoryKind"`
					Title        string `json:"title"`
					Summary      string `json:"summary"`
					CreatedAt    string `json:"createdAt"`
				} `json:"content"`
			} `json:"articles"`
		} `json:"pageProps"`
	} `json:"props"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	page, err := a.get(listURL)
	if err != nil {
		return nil, err
	}
	script := page.Find("#__NEXT_DATA__").Text()
	var data listPage
	if script == "" || json.Unmarshal([]byte(script), &data) != nil {
		return nil, fmt.Errorf("list data not found")
	}
	var postings []job.Posting
	for _, article := range data.Props.PageProps.Articles.Content {
		if article.CategoryKind != "LINK" || strings.TrimSpace(article.Summary) == "" {
			continue
		}
		detail, err := a.detail(article.Summary)
		if err != nil {
			return postings, fmt.Errorf("detail %d: %w", article.ID, err)
		}
		postedAt, _ := time.Parse("2006-01-02T15:04:05", article.CreatedAt)
		if postedAt.IsZero() {
			postedAt = time.Now().UTC()
		}
		p := job.Posting{Site: a.Name(), ExternalID: fmt.Sprint(article.ID), Title: strings.TrimSpace(article.Title), Company: "두나무", URL: article.Summary, PostedAt: postedAt, Description: detail.description, EmploymentType: detail.employmentType, CareerLevel: detail.careerLevel, Location: detail.location}
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

type detailInfo struct{ description, employmentType, careerLevel, location string }

func (a *Adapter) detail(url string) (detailInfo, error) {
	doc, err := a.get(url)
	if err != nil {
		return detailInfo{}, err
	}
	section := doc.Find(".detailView_information").First()
	if section.Length() == 0 {
		return detailInfo{}, fmt.Errorf("description not found")
	}
	var lines []string
	section.Find("h1, h2, h3, h4, p, li").Each(func(_ int, s *goquery.Selection) {
		if text := strings.Join(strings.Fields(s.Text()), " "); text != "" {
			lines = append(lines, text)
		}
	})
	d := detailInfo{description: strings.Join(lines, "\n")}
	for _, line := range lines {
		switch {
		case strings.Contains(line, "고용형태") && strings.Contains(line, "정규직"):
			d.employmentType = "Full-time"
		case strings.Contains(line, "고용형태") && strings.Contains(line, "계약직"):
			d.employmentType = "Contract"
		case strings.Contains(line, "고용형태") && strings.Contains(line, "인턴"):
			d.employmentType = "Internship"
		case strings.Contains(line, "채용유형") && strings.Contains(line, "경력"):
			d.careerLevel = "경력"
		case strings.Contains(line, "채용유형") && strings.Contains(line, "신입"):
			d.careerLevel = "신입"
		case strings.Contains(line, "근무지역"):
			d.location = strings.TrimSpace(strings.TrimPrefix(line, "근무지역 :"))
		}
	}
	return d, nil
}
func (a *Adapter) get(url string) (*goquery.Document, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; notify-me-crawler/1.0)")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return goquery.NewDocumentFromReader(resp.Body)
}
