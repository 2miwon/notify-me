// Package automattic implements site.Adapter for automattic.com's own
// careers page.
//
// Automattic is fully distributed (no offices, hire-from-anywhere), and
// its listing genuinely carries no location field at all — there's
// nothing to restrict by country the way most other adapters do, so
// DevOnly is this adapter's only filter.
//
// Automattic isn't on a guessable public Greenhouse board slug (unlike
// Canonical/GitLab/Sendbird/Moloco) — its own careers page embeds the
// data directly instead, server-rendered by a WordPress plugin, as a
// plain JS array assigned to a page-global variable:
//
//	GET https://automattic.com/work-with-us/jobs/
//	  <script>const ghJobsData = [ {id, title, slug, metadata:
//	    {Category: [...], Team: [...]}, content, href}, ... ]</script>
package automattic

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const listURL = "https://automattic.com/work-with-us/jobs/"

// Adapter crawls automattic.com/work-with-us/jobs/.
type Adapter struct {
	// DevOnly, when true, drops postings that don't look like a
	// software/engineering role — see devRelated.
	DevOnly bool

	client *http.Client
}

func New(devOnly bool) *Adapter {
	return &Adapter{DevOnly: devOnly, client: &http.Client{Timeout: 20 * time.Second}}
}

func (a *Adapter) Name() string { return "automattic" }

type posting struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Slug     string `json:"slug"`
	Metadata struct {
		Category []string `json:"Category"`
		Team     []string `json:"Team"`
	} `json:"metadata"`
	Content string `json:"content"`
	Href    string `json:"href"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 20 * time.Second}
	}

	req, err := http.NewRequest(http.MethodGet, listURL, nil)
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
		return nil, fmt.Errorf("automattic: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("automattic: read body: %w", err)
	}

	postings, err := extractJobs(string(body))
	if err != nil {
		return nil, fmt.Errorf("automattic: %w", err)
	}

	var out []job.Posting
	for _, p := range postings {
		title := strings.TrimSpace(p.Title)
		if a.DevOnly && !devRelated(title, p.Metadata.Category) {
			continue
		}

		url := p.Href
		if url == "" {
			url = fmt.Sprintf("https://automattic.com/work-with-us/job/%s/", p.Slug)
		}

		jp := job.Posting{
			Site:        a.Name(),
			ExternalID:  fmt.Sprintf("%d", p.ID),
			Title:       title,
			Company:     "Automattic",
			URL:         url,
			PostedAt:    time.Now().UTC(),
			Description: htmlutil.StripToText(html.UnescapeString(p.Content)),
		}
		jp.MinYearsExperience = job.ExtractMinYearsExperience(jp.Title + "\n" + jp.Description)
		out = append(out, jp)
	}
	return out, nil
}

// extractJobs pulls the `const ghJobsData = [...]` array literal out of
// the page's own inline script and decodes it. raw_decode-style parsing
// (via json.Decoder) so trailing script content after the array's
// closing bracket doesn't matter.
func extractJobs(pageHTML string) ([]posting, error) {
	const marker = "const ghJobsData = "
	idx := strings.Index(pageHTML, marker)
	if idx == -1 {
		return nil, fmt.Errorf("ghJobsData not found")
	}
	dec := json.NewDecoder(strings.NewReader(pageHTML[idx+len(marker):]))
	var postings []posting
	if err := dec.Decode(&postings); err != nil {
		return nil, fmt.Errorf("decode ghJobsData: %w", err)
	}
	return postings, nil
}

var devTitleKeywords = []string{
	"engineer", "developer", "swe", "software", "sre", "devops",
	"architect", "programmer", "scientist",
}

var devCategoryHints = []string{"engineering", "infrastructure"}

func devRelated(title string, categories []string) bool {
	lower := strings.ToLower(title)
	for _, kw := range devTitleKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	for _, cat := range categories {
		catLower := strings.ToLower(cat)
		for _, hint := range devCategoryHints {
			if strings.Contains(catLower, hint) {
				return true
			}
		}
	}
	return false
}
