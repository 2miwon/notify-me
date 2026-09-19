// Package daangn implements site.Adapter for careers.daangn.com. Its jobs
// page is server-rendered with every live card in the HTML; the division
// query string is a client-side filter, so the adapter applies that filter
// itself before fetching each selected posting's JSON-LD description.
package daangn

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	listURL   = "https://careers.daangn.com/jobs/"
	baseURL   = "https://careers.daangn.com"
	userAgent = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
)

var roleIDPattern = regexp.MustCompile(`/jobs/role/(\d+)/`)

type Adapter struct {
	DepartmentSlugs []string
	client          *http.Client
}

func New(departmentSlugs []string) *Adapter {
	return &Adapter{DepartmentSlugs: departmentSlugs, client: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Adapter) Name() string { return "daangn" }

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list status %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse list: %w", err)
	}

	var cards []card
	doc.Find("[data-job-card]").Each(func(_ int, s *goquery.Selection) {
		if s.AttrOr("data-division", "") != "tech" || !matchesDepartment(s.AttrOr("data-department-slugs", ""), a.DepartmentSlugs) {
			return
		}
		link := s.Find("a[href]").First()
		href, ok := link.Attr("href")
		id := roleIDPattern.FindStringSubmatch(href)
		if !ok || len(id) != 2 {
			return
		}
		title := strings.TrimSpace(s.Find("h3").First().Text())
		if title == "" {
			return
		}
		company := strings.TrimSpace(s.Find("ul li").First().Text())
		if company == "" {
			company = "당근"
		}
		cards = append(cards, card{id: id[1], title: title, company: company, url: baseURL + href, employmentType: employmentType(s.AttrOr("data-employment-type", ""))})
	})
	if cards == nil {
		return nil, fmt.Errorf("list has no job cards")
	}

	postings := make([]job.Posting, 0, len(cards))
	for _, card := range cards {
		detail, err := a.fetchDetail(card.url)
		// The listing is authoritative for whether a job is live. A single
		// malformed or temporarily unavailable detail page must not make the
		// whole source fail (which would also disable the expiry sweep).
		// Keep the card's structured listing data and omit only its body.
		if err != nil {
			detail = detailInfo{}
		}
		p := job.Posting{Site: a.Name(), ExternalID: card.id, Title: card.title, Company: card.company, URL: card.url, EmploymentType: card.employmentType, PostedAt: detail.postedAt, Description: detail.description}
		if p.PostedAt.IsZero() {
			p.PostedAt = time.Now().UTC()
		}
		if detail.employmentType != "" {
			p.EmploymentType = employmentType(detail.employmentType)
		}
		p.CareerLevel = careerLevel(card.title)
		p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
		postings = append(postings, p)
	}
	return postings, nil
}

type card struct{ id, title, company, url, employmentType string }
type detailInfo struct {
	description, employmentType string
	postedAt                    time.Time
}
type structuredJob struct {
	Type           string `json:"@type"`
	Description    string `json:"description"`
	DatePosted     string `json:"datePosted"`
	EmploymentType string `json:"employmentType"`
}

func (a *Adapter) fetchDetail(url string) (detailInfo, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return detailInfo{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := a.client.Do(req)
	if err != nil {
		return detailInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return detailInfo{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return detailInfo{}, err
	}
	var result detailInfo
	found := false
	doc.Find(`script[type="application/ld+json"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		var entries []structuredJob
		if json.Unmarshal([]byte(s.Text()), &entries) != nil {
			return true
		}
		for _, entry := range entries {
			if entry.Type != "JobPosting" {
				continue
			}
			result.description = htmlToText(entry.Description)
			result.employmentType = entry.EmploymentType
			result.postedAt, _ = time.Parse("2006-01-02", entry.DatePosted)
			found = true
			return false
		}
		return true
	})
	if !found {
		return detailInfo{}, fmt.Errorf("JobPosting JSON-LD not found")
	}
	return result, nil
}

func matchesDepartment(raw string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, department := range strings.Fields(raw) {
		for _, candidate := range allowed {
			if department == candidate {
				return true
			}
		}
	}
	return false
}

func employmentType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "full-time", "full_time":
		return "Full-time"
	case "intern", "internship":
		return "Internship"
	case "contract", "contractor":
		return "Contract"
	case "temporary":
		return "Temporary"
	default:
		return ""
	}
}

func careerLevel(title string) string {
	switch {
	case strings.Contains(title, "(신입)") || strings.Contains(title, "신입 가능"):
		return "신입"
	case strings.Contains(title, "(경력)"):
		return "경력"
	default:
		return ""
	}
}

func htmlToText(source string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(source))
	if err != nil {
		return strings.Join(strings.Fields(source), " ")
	}
	var lines []string
	doc.Find("h1, h2, h3, h4, h5, h6, p, li").Each(func(_ int, s *goquery.Selection) {
		s.Find("br").ReplaceWithHtml("\n")
		for _, line := range strings.Split(s.Text(), "\n") {
			if line = strings.Join(strings.Fields(line), " "); line != "" {
				// The CMS often nests a paragraph and list item with the
				// same content. goquery correctly exposes both nodes, but
				// emitting each produces visibly duplicated paragraphs.
				if len(lines) == 0 || lines[len(lines)-1] != line {
					lines = append(lines, strings.ReplaceAll(line, `\.`, `.`))
				}
			}
		}
	})
	return strings.Join(lines, "\n")
}
