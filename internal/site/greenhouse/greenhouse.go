// Package greenhouse implements site.Adapter for companies whose careers
// page is backed by Greenhouse's public Job Board API — a plain public
// JSON endpoint, no auth needed, regardless of what the company's own
// careers UI is actually built on. Sendbird and Moloco both use it; any
// other "boards-api.greenhouse.io/v1/boards/<slug>" board should work
// the same way:
//
//	GET https://boards-api.greenhouse.io/v1/boards/<slug>/jobs/?content=true
package greenhouse

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

// Adapter crawls one Greenhouse job board.
type Adapter struct {
	CompanySlug string
	// Company is the display name stored on each posting.
	Company string
	// Locations is a substring allow-list (OR'd, case-insensitive)
	// against Greenhouse's own `location.name`. Empty means no
	// restriction.
	Locations []string
	// DevOnly, when true, drops postings whose title doesn't look like a
	// software/engineering role. Greenhouse `departments` names vary too
	// much between companies (and even within one — Moloco alone spreads
	// engineering work across "Engineering", "Ads Platform", "Ads
	// Quality", "MCM Quality", "Moloco Next") to allow-list reliably, so
	// this checks the title instead, in English and Korean.
	DevOnly bool

	client *http.Client
}

func New(companySlug, company string, locations []string, devOnly bool) *Adapter {
	return &Adapter{
		CompanySlug: companySlug,
		Company:     company,
		Locations:   locations,
		DevOnly:     devOnly,
		client:      &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.CompanySlug }

type jobsResponse struct {
	Jobs []posting `json:"jobs"`
}

type posting struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	Departments []struct {
		Name string `json:"name"`
	} `json:"departments"`
	AbsoluteURL    string `json:"absolute_url"`
	Content        string `json:"content"`
	FirstPublished string `json:"first_published"`
	// Metadata is each company's own custom fields — free-form names and
	// values, but a few companies expose employment type/deadline here
	// (Toss: "Employment_Type" = "정규직", "...클로징 일자" = "2026-10-19";
	// Canonical: "Employment Length" = "Full-time"/"Permanent").
	Metadata []struct {
		Name  string          `json:"name"`
		Value json.RawMessage `json:"value"`
	} `json:"metadata"`
}

// metadataString returns the first string-valued custom field whose name
// contains any of the given fragments (case-insensitive).
func (p posting) metadataString(fragments ...string) string {
	for _, m := range p.Metadata {
		name := strings.ToLower(m.Name)
		for _, f := range fragments {
			if !strings.Contains(name, f) {
				continue
			}
			var s string
			if json.Unmarshal(m.Value, &s) == nil && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

var kst = time.FixedZone("KST", 9*60*60)

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}
	if a.CompanySlug == "" {
		return nil, fmt.Errorf("greenhouse: no company slug configured")
	}

	url := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs/?content=true", a.CompanySlug)
	req, err := http.NewRequest(http.MethodGet, url, nil)
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
	var data jobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	var out []job.Posting
	for _, p := range data.Jobs {
		if !matchesLocation(p.Location.Name, a.Locations) {
			continue
		}
		title := strings.TrimSpace(p.Title)
		if a.DevOnly && !devRelated(title, departmentNames(p.Departments)) {
			continue
		}

		// Greenhouse's own `content` field is HTML that's itself been
		// HTML-escaped once into the JSON string value (e.g. literal
		// "&lt;p&gt;" rather than "<p>") — unescape once before parsing
		// it as HTML, or the tags never get recognized as tags at all.
		description := htmlutil.StripToText(html.UnescapeString(p.Content))

		jp := job.Posting{
			Site:        a.Name(),
			ExternalID:  fmt.Sprintf("%d", p.ID),
			Title:       title,
			Company:     a.Company,
			URL:         p.AbsoluteURL,
			Location:    p.Location.Name,
			PostedAt:    time.Now().UTC(),
			Description: description,
		}
		if t, err := time.Parse(time.RFC3339, p.FirstPublished); err == nil {
			jp.ApplicationStart = &t
		}
		// Greenhouse has no standard employment-type/career field: prefer
		// what the title itself says (KRAFTON writes "(5년 이상 / 계약직)"),
		// then the company's own custom field if it has one.
		jp.EmploymentType = job.EmploymentTypeFromTitle(title)
		if jp.EmploymentType == "" {
			jp.EmploymentType = job.CanonicalKoreanEmploymentType(p.metadataString("employment"))
		}
		jp.CareerLevel = job.CareerLevelFromTitle(title)
		if deadline := p.metadataString("클로징 일자", "deadline"); deadline != "" {
			if t, err := time.ParseInLocation("2006-01-02", deadline, kst); err == nil {
				jp.ApplicationDeadline = &t
			}
		}
		jp.MinYearsExperience = job.ExtractMinYearsExperience(jp.Title + "\n" + jp.Description)
		out = append(out, jp)
	}
	return out, nil
}

func matchesLocation(location string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	lower := strings.ToLower(location)
	for _, candidate := range allowed {
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(candidate))) {
			return true
		}
	}
	return false
}

// devDepartmentHints catches a posting whose department is clearly
// technical even when its title alone wouldn't be enough to tell — e.g.
// Canonical's "Embedded Linux Consultant" or "Technical Author" sit under
// "Field Engineering"/"Excellence Engineering" without "engineer"
// anywhere in the title itself.
var devDepartmentHints = []string{"engineering", "sre"}

func departmentNames(departments []struct {
	Name string `json:"name"`
}) []string {
	names := make([]string, len(departments))
	for i, d := range departments {
		names[i] = d.Name
	}
	return names
}

func devRelated(title string, departments []string) bool {
	if job.IsDevTitle(title) {
		return true
	}
	// A PM/analyst/compliance title under an engineering department
	// isn't a dev role — see job.IsNonDevTitle.
	if job.IsNonDevTitle(title) {
		return false
	}
	for _, dept := range departments {
		deptLower := strings.ToLower(dept)
		for _, hint := range devDepartmentHints {
			if strings.Contains(deptLower, hint) {
				return true
			}
		}
	}
	return false
}
