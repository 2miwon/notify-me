// Package lever implements site.Adapter for companies whose careers page
// is backed by Lever's public postings API — a plain public JSON
// endpoint, no auth needed, regardless of what the company's own careers
// UI is actually built on. Channel Talk (channel.io, Lever slug "zoyi")
// is one instance; any other "jobs.lever.co/<slug>" board should work
// the same way:
//
//	GET https://api.lever.co/v0/postings/<slug>?mode=json
package lever

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

// pageSize is how many postings one API request asks for.
const pageSize = 50

// Adapter crawls one Lever postings board.
type Adapter struct {
	CompanySlug string
	// Company is the display name stored on each posting. Empty means use
	// each posting's own department (Match Group's board mixes Hyperconnect,
	// Tinder, Match Group AI, ... under one Lever account).
	Company string
	// Countries is an exact-match allow-list (OR'd) against Lever's own
	// `country` field (e.g. "KR"). Empty means no restriction.
	Countries []string
	// Teams is an exact-match allow-list (OR'd) against
	// categories.team. Empty means no restriction.
	Teams []string
	// WorkplaceTypes is an exact-match allow-list (OR'd) against Lever's
	// workplaceType ("remote", "hybrid", "onsite"). Empty means no
	// restriction. Binance's "Asia" roles are mostly remote; its hybrid ones
	// require relocating to its Hong Kong or Taipei offices.
	WorkplaceTypes []string
	// DevOnly keeps only postings whose team/department or title looks
	// technical (job.IsDevCategory OR job.IsDevTitle) — for boards where
	// engineering is spread over several team names.
	DevOnly bool

	client *http.Client
}

func New(companySlug, company string, countries, teams, workplaceTypes []string, devOnly bool) *Adapter {
	return &Adapter{
		CompanySlug:    companySlug,
		Company:        company,
		Countries:      countries,
		Teams:          teams,
		WorkplaceTypes: workplaceTypes,
		DevOnly:        devOnly,
		client:         &http.Client{Timeout: 45 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.CompanySlug }

type posting struct {
	ID            string `json:"id"`
	Text          string `json:"text"`
	HostedURL     string `json:"hostedUrl"`
	CreatedAt     int64  `json:"createdAt"`
	Country       string `json:"country"`
	WorkplaceType string `json:"workplaceType"`
	Categories    struct {
		Team       string `json:"team"`
		Department string `json:"department"`
		Location   string `json:"location"`
		Commitment string `json:"commitment"`
	} `json:"categories"`
	DescriptionPlain string `json:"descriptionPlain"`
	// Lever splits a posting in three: the intro (descriptionPlain), named
	// bullet sections ("What You'll Do", "Requirements", ...) in lists, and a
	// closing note (additionalPlain). Reading only the first drops every
	// requirement and responsibility.
	Lists []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"lists"`
	AdditionalPlain string `json:"additionalPlain"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 45 * time.Second}
	}
	if a.CompanySlug == "" {
		return nil, fmt.Errorf("lever: no company slug configured")
	}

	// Large boards (Binance: ~300 postings, 3MB) take longer than the client
	// timeout as one response, so read the board in pages.
	var postings []posting
	for skip := 0; ; skip += pageSize {
		// Lever's latency is spiky (the same page has taken 63s, then 6s),
		// so retry a timed-out page rather than failing the whole board.
		var page []posting
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if page, err = a.fetchPage(skip); err == nil {
				break
			}
		}
		if err != nil {
			return nil, err
		}
		postings = append(postings, page...)
		if len(page) < pageSize {
			break
		}
	}

	var out []job.Posting
	for _, p := range postings {
		if !matches(p.Country, a.Countries) || !matches(p.Categories.Team, a.Teams) || !matches(p.WorkplaceType, a.WorkplaceTypes) {
			continue
		}
		if a.DevOnly && !job.LooksDev(p.Text, p.Categories.Team, p.Categories.Department) {
			continue
		}
		postedAt := time.Now().UTC()
		var appStart *time.Time
		if p.CreatedAt > 0 {
			t := time.UnixMilli(p.CreatedAt).UTC()
			appStart = &t
		}
		company := a.Company
		if company == "" {
			company = strings.TrimSpace(p.Categories.Department)
		}
		jp := job.Posting{
			Site:             a.Name(),
			ExternalID:       p.ID,
			Title:            strings.TrimSpace(p.Text),
			Company:          company,
			URL:              p.HostedURL,
			Location:         p.Categories.Location,
			EmploymentType:   commitment(p.Categories.Commitment),
			PostedAt:         postedAt,
			ApplicationStart: appStart,
			Description:      p.fullDescription(),
		}
		jp.MinYearsExperience = job.ExtractMinYearsExperience(jp.Title + "\n" + jp.Description)
		out = append(out, jp)
	}
	return out, nil
}

func (a *Adapter) fetchPage(skip int) ([]posting, error) {
	url := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json&limit=%d&skip=%d", a.CompanySlug, pageSize, skip)
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
	var postings []posting
	if err := json.NewDecoder(resp.Body).Decode(&postings); err != nil {
		return nil, err
	}
	return postings, nil
}

// fullDescription stitches together the intro, each bullet section
// (heading, then one "• " line per <li>) and the closing note. The bullet
// marker is what keeps a section heading distinguishable from its items
// once blank lines are collapsed.
func (p posting) fullDescription() string {
	parts := []string{p.DescriptionPlain}
	for _, l := range p.Lists {
		items := htmlutil.StripToText(strings.NewReplacer("<li>", "\n• ", "</li>", "\n", "<br>", "\n", "<br/>", "\n").Replace(l.Content))
		parts = append(parts, l.Text, items)
	}
	parts = append(parts, p.AdditionalPlain)
	return normalizeText(strings.Join(parts, "\n"))
}

// normalizeText collapses runs of blank lines in Lever's already-plain-text
// descriptionPlain field, matching the tidiness of every other adapter's
// description without needing an HTML stripper on non-HTML input.
func normalizeText(source string) string {
	var lines []string
	for _, raw := range strings.Split(source, "\n") {
		line := strings.Join(strings.Fields(raw), " ")
		if line != "" && (len(lines) == 0 || lines[len(lines)-1] != line) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func matches(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func commitment(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "intern"), strings.Contains(lower, "인턴"):
		return "Internship"
	case strings.Contains(lower, "contract"), strings.Contains(lower, "계약"):
		return "Contract"
	case strings.Contains(lower, "temporary"):
		return "Temporary"
	case strings.Contains(lower, "full"), strings.Contains(lower, "permanent"), strings.Contains(lower, "regular"), strings.Contains(lower, "정규"):
		return "Full-time"
	default:
		return strings.TrimSpace(raw)
	}
}
