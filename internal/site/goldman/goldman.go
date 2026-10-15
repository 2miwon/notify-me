// Package goldman implements site.Adapter for Goldman Sachs' careers site
// (higher.gs.com), a Next.js app over a public GraphQL gateway:
//
//	POST https://api-higher.gs.com/gateway/api/v1/graphql
//	  query GetRoles($searchQueryInput: RoleSearchQueryInput!) — the same
//	  query the results page sends, with a LOCATION filter whose value is
//	  the country name as the site spells it ("Korea, Republic of").
//	GET  https://higher.gs.com/roles/<sourceId>
//	  __NEXT_DATA__ → props.pageProps.role.descriptionHtml
package goldman

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	graphqlURL = "https://api-higher.gs.com/gateway/api/v1/graphql"
	roleURL    = "https://higher.gs.com/roles/"
	pageSize   = 50
	maxPages   = 20
	userAgent  = "Mozilla/5.0 (compatible; notify-me-crawler/1.0)"
)

const rolesQuery = `query GetRoles($searchQueryInput: RoleSearchQueryInput!) {
  roleSearch(searchQueryInput: $searchQueryInput) {
    totalCount
    items {
      roleId
      corporateTitle
      jobTitle
      jobFunction
      locations { primary state country city }
      division
      jobType { code description }
      externalSource { sourceId }
    }
  }
}`

var nextDataPattern = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)

// Adapter crawls higher.gs.com for one country.
type Adapter struct {
	// Country is the LOCATION filter value, e.g. "Korea, Republic of".
	Country string
	// DevOnly keeps titles/functions/divisions that look technical
	// (job.LooksDev) — Engineering division roles, not banking.
	DevOnly bool

	client *http.Client
}

func New(country string, devOnly bool) *Adapter {
	return &Adapter{Country: country, DevOnly: devOnly, client: &http.Client{Timeout: 30 * time.Second}}
}

func (a *Adapter) Name() string { return "goldman" }

type role struct {
	RoleID         string `json:"roleId"`
	CorporateTitle string `json:"corporateTitle"`
	JobTitle       string `json:"jobTitle"`
	JobFunction    string `json:"jobFunction"`
	Division       string `json:"division"`
	Locations      []struct {
		Primary bool   `json:"primary"`
		City    string `json:"city"`
		Country string `json:"country"`
	} `json:"locations"`
	JobType *struct {
		Description string `json:"description"`
	} `json:"jobType"`
	ExternalSource struct {
		SourceID string `json:"sourceId"`
	} `json:"externalSource"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	var postings []job.Posting
	seen := 0
	for page := 0; page < maxPages; page++ {
		roles, total, err := a.search(page)
		if err != nil {
			return postings, fmt.Errorf("goldman: page %d: %w", page, err)
		}
		for _, r := range roles {
			if a.DevOnly && !job.LooksDev(r.JobTitle, r.JobFunction, r.Division) {
				continue
			}
			postings = append(postings, a.toPosting(r))
		}
		seen += len(roles)
		if len(roles) == 0 || seen >= total {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) search(page int) ([]role, int, error) {
	body, _ := json.Marshal(map[string]any{
		"operationName": "GetRoles",
		"query":         rolesQuery,
		"variables": map[string]any{
			"searchQueryInput": map[string]any{
				"page": map[string]int{"pageSize": pageSize, "pageNumber": page},
				"sort": map[string]string{"sortStrategy": "RELEVANCE", "sortOrder": "DESC"},
				"filters": []map[string]any{{
					"filterCategoryType": "LOCATION",
					"filters":            []map[string]any{{"filter": a.Country, "subFilters": []string{}}},
				}},
				"experiences": []string{"EARLY_CAREER", "PROFESSIONAL"},
				"searchTerm":  "",
			},
		},
	})
	req, err := http.NewRequest(http.MethodPost, graphqlURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://higher.gs.com")
	req.Header.Set("Referer", "https://higher.gs.com/")
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
		Data struct {
			RoleSearch struct {
				TotalCount int    `json:"totalCount"`
				Items      []role `json:"items"`
			} `json:"roleSearch"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, err
	}
	if len(out.Errors) > 0 {
		return nil, 0, fmt.Errorf("graphql: %s", out.Errors[0].Message)
	}
	return out.Data.RoleSearch.Items, out.Data.RoleSearch.TotalCount, nil
}

func (a *Adapter) toPosting(r role) job.Posting {
	var cities []string
	for _, l := range r.Locations {
		if l.City != "" {
			cities = append(cities, l.City)
		}
	}
	location := strings.Join(cities, " / ")
	if location == "" {
		location = a.Country
	}
	id := r.ExternalSource.SourceID
	if id == "" {
		id = r.RoleID
	}
	p := job.Posting{
		Site:           a.Name(),
		ExternalID:     id,
		Title:          strings.TrimSpace(r.JobTitle),
		Company:        "Goldman Sachs",
		URL:            roleURL + id,
		Location:       location,
		EmploymentType: job.EmploymentTypeFromTitle(r.JobTitle),
		PostedAt:       time.Now().UTC(),
	}
	if p.EmploymentType == "" && r.JobType != nil {
		p.EmploymentType = job.CanonicalEmploymentType(strings.ToLower(r.JobType.Description))
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
	m := nextDataPattern.FindSubmatch(page)
	if m == nil {
		return "", fmt.Errorf("__NEXT_DATA__ not found")
	}
	var data struct {
		Props struct {
			PageProps struct {
				Role struct {
					DescriptionHTML string `json:"descriptionHtml"`
				} `json:"role"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(m[1], &data); err != nil {
		return "", err
	}
	return htmlutil.StripToText(data.Props.PageProps.Role.DescriptionHTML), nil
}
