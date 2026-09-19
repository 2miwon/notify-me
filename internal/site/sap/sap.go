// Package sap implements site.Adapter for jobs.sap.com (SAP's SuccessFactors
// Career Site Builder — an older, plain server-rendered platform: no JSON
// API, just an HTML results table and detail pages, scraped with goquery
// the same way as internal/site/apple's listing page.
//
// ListPath is the filtered search's own path+query, copied verbatim from
// the site (e.g. "/go/Technology-Jobs-in-Seoul/918101/join?q=&sortColumn=
// sort_location&sortDirection=asc") — find it by applying whatever filter
// you want on jobs.sap.com and reading the resulting URL back out.
// Pagination appends this platform's own "startrow" offset param.
package sap

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/2miwon/notify-me/internal/job"
)

const (
	baseURL    = "https://jobs.sap.com"
	userAgent  = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
	pageSize   = 25
	dateLayout = "Jan 2, 2006"
)

// Adapter crawls one filtered search on jobs.sap.com.
type Adapter struct {
	// ListPath is the filtered search results page's own path+query.
	ListPath string
	// DevOnly, when true, drops postings that don't look like software/
	// engineering roles — see devRelated. A location-only search returns
	// Sales/Presales postings alongside the actual dev roles.
	DevOnly bool

	client *http.Client
}

func New(listPath string, devOnly bool) *Adapter {
	return &Adapter{ListPath: listPath, DevOnly: devOnly, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "sap" }

type listing struct {
	externalID string
	title      string
	url        string
	location   string
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.ListPath == "" {
		return nil, fmt.Errorf("sap: no list path configured")
	}

	var postings []job.Posting
	for start := 0; ; start += pageSize {
		listings, err := a.fetchListPage(start)
		if err != nil {
			return postings, fmt.Errorf("sap: list at startrow %d: %w", start, err)
		}
		if len(listings) == 0 {
			break
		}

		for _, l := range listings {
			p := job.Posting{
				Site:       a.Name(),
				ExternalID: l.externalID,
				Title:      l.title,
				Company:    "SAP",
				URL:        l.url,
				Location:   l.location,
				PostedAt:   time.Now().UTC(),
			}

			var department string
			// A failed detail fetch is a lesser failure than losing the
			// posting entirely — keep it with just the list fields.
			if d, err := a.fetchDetail(l.url); err == nil {
				p.Description = d.description
				p.EmploymentType = canonicalEmploymentType(d.shiftType)
				department = d.department
				if t, err := time.Parse(dateLayout, d.date); err == nil {
					p.ApplicationStart = &t
				}
			}

			if a.DevOnly && !devRelated(department, p.Title) {
				continue
			}

			p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
			postings = append(postings, p)
		}

		if len(listings) < pageSize {
			break
		}
	}

	return postings, nil
}

func (a *Adapter) fetchListPage(start int) ([]listing, error) {
	pageURL := baseURL + a.ListPath
	if start > 0 {
		sep := "&"
		if !strings.Contains(pageURL, "?") {
			sep = "?"
		}
		pageURL += sep + "startrow=" + strconv.Itoa(start)
	}

	doc, err := a.getDocument(pageURL)
	if err != nil {
		return nil, err
	}

	var listings []listing
	doc.Find("tr.data-row").Each(func(_ int, s *goquery.Selection) {
		link := s.Find("td.colTitle a.jobTitle-link").First()
		href, ok := link.Attr("href")
		if !ok || href == "" {
			return
		}
		title := strings.TrimSpace(link.Text())
		location := strings.TrimSpace(s.Find("td.colLocation span.jobLocation").First().Text())

		listings = append(listings, listing{
			externalID: externalIDFromHref(href),
			title:      title,
			url:        baseURL + href,
			location:   location,
		})
	})
	return listings, nil
}

// externalIDFromHref pulls the numeric requisition ID off the end of a
// detail path like "/job/Seoul-Platform-Engineer-06578/1421104333/".
func externalIDFromHref(href string) string {
	parts := strings.Split(strings.Trim(href, "/"), "/")
	if len(parts) == 0 {
		return href
	}
	return parts[len(parts)-1]
}

type detail struct {
	description string
	department  string
	shiftType   string
	date        string
}

func (a *Adapter) fetchDetail(detailURL string) (*detail, error) {
	doc, err := a.getDocument(detailURL)
	if err != nil {
		return nil, err
	}

	propText := func(name string) string {
		sel := doc.Find(fmt.Sprintf(`span[data-careersite-propertyid="%s"]`, name)).First()
		return strings.TrimSpace(sel.Text())
	}

	descHTML, _ := doc.Find(`span[data-careersite-propertyid="description"]`).First().Html()

	return &detail{
		description: htmlToText(descHTML),
		department:  propText("department"),
		shiftType:   propText("shifttype"),
		date:        propText("date"),
	}, nil
}

func (a *Adapter) getDocument(pageURL string) (*goquery.Document, error) {
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return goquery.NewDocumentFromReader(resp.Body)
}

func htmlToText(fragment string) string {
	if strings.TrimSpace(fragment) == "" {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(fragment))
	if err != nil {
		return ""
	}
	doc.Find("br").ReplaceWithHtml("\n")
	text := doc.Text()
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func canonicalEmploymentType(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "intern"):
		return "Internship"
	case strings.Contains(lower, "fixed term"), strings.Contains(lower, "contract"):
		return "Contract"
	case strings.Contains(lower, "full time"):
		return "Full-time"
	case strings.Contains(lower, "part time"):
		return "Part-time"
	default:
		return strings.TrimSpace(raw)
	}
}

// devRelated decides whether a posting looks like a software/engineering
// role — the same recall-biased, department-hint-OR-title-keyword shape
// as internal/site/microsoft and internal/site/netflix's devRelated. A
// location-only SAP search otherwise returns Sales/Presales roles right
// alongside the actual dev roles.
var devDepartmentHints = []string{"software", "development", "engineering"}

var devTitleKeywords = []string{
	"engineer", "developer", "swe", "software", "sre", "devops",
	"architect", "programmer", "scientist",
}

func devRelated(department, title string) bool {
	lowerDept := strings.ToLower(department)
	for _, hint := range devDepartmentHints {
		if strings.Contains(lowerDept, hint) {
			return true
		}
	}
	lowerTitle := strings.ToLower(title)
	for _, kw := range devTitleKeywords {
		if strings.Contains(lowerTitle, kw) {
			return true
		}
	}
	return false
}
