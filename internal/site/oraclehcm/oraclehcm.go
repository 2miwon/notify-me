// Package oraclehcm implements site.Adapter for career sites on Oracle
// Recruiting Cloud's "Candidate Experience" (JPMorgan Chase:
// jpmc.fa.oraclecloud.com, site CX_1001). The search page reads a public
// REST resource, filtered by a geography id:
//
//	GET https://<host>/hcmRestApi/resources/latest/recruitingCEJobRequisitions
//	    ?onlyData=true&finder=findReqs;siteNumber=<site>,limit=50,offset=N,
//	     locationId=<geography id>,sortBy=POSTING_DATES_DESC
//	  {"items": [{"TotalJobsCount", "requisitionList": [{Id, Title,
//	    PrimaryLocation, JobFamily, JobFunction, PostedDate, JobSchedule}]}]}
//	GET .../recruitingCEJobRequisitionDetails?expand=all&onlyData=true
//	    &finder=ById;Id="<id>",siteNumber=<site>
//	  {"items": [{ExternalDescriptionStr, ExternalResponsibilitiesStr,
//	    ExternalQualificationsStr, ...}]}
//
// Find the geography id with the location typeahead the page itself uses:
// .../recruitingCESearchAutoSuggestions?onlyData=true&finder=findByLoc;string=Korea
// (Korea, Republic of = 300000000289411 on JPMC's site).
package oraclehcm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/2miwon/notify-me/internal/htmlutil"
	"github.com/2miwon/notify-me/internal/job"
)

const (
	pageSize = 50
	maxPages = 20
)

// Adapter crawls one Oracle Candidate Experience site for one geography.
type Adapter struct {
	SiteName   string
	Company    string
	Host       string
	SiteNumber string
	LocationID string
	// DevOnly keeps job family/function/title that look technical
	// (job.LooksDev).
	DevOnly bool

	client *http.Client
}

func New(siteName, company, host, siteNumber, locationID string, devOnly bool) *Adapter {
	return &Adapter{
		SiteName: siteName, Company: company, Host: host, SiteNumber: siteNumber,
		LocationID: locationID, DevOnly: devOnly, client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *Adapter) Name() string { return a.SiteName }

type requisition struct {
	ID              string `json:"Id"`
	Title           string `json:"Title"`
	PrimaryLocation string `json:"PrimaryLocation"`
	JobFamily       string `json:"JobFamily"`
	JobFunction     string `json:"JobFunction"`
	PostedDate      string `json:"PostedDate"`
	JobSchedule     string `json:"JobSchedule"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	if a.client == nil {
		a.client = &http.Client{Timeout: 30 * time.Second}
	}
	var postings []job.Posting
	offset := 0
	for page := 0; page < maxPages; page++ {
		target := fmt.Sprintf("https://%s/hcmRestApi/resources/latest/recruitingCEJobRequisitions?onlyData=true&expand=requisitionList.secondaryLocations"+
			"&finder=findReqs;siteNumber=%s,limit=%d,offset=%d,locationId=%s,sortBy=POSTING_DATES_DESC",
			a.Host, a.SiteNumber, pageSize, offset, a.LocationID)
		var resp struct {
			Items []struct {
				TotalJobsCount  int           `json:"TotalJobsCount"`
				RequisitionList []requisition `json:"requisitionList"`
			} `json:"items"`
		}
		if err := a.getJSON(target, &resp); err != nil {
			return postings, fmt.Errorf("%s: search at %d: %w", a.SiteName, offset, err)
		}
		if len(resp.Items) == 0 {
			break
		}
		reqs := resp.Items[0].RequisitionList
		for _, r := range reqs {
			if a.DevOnly && !job.LooksDev(r.Title, r.JobFamily, r.JobFunction) {
				continue
			}
			postings = append(postings, a.toPosting(r))
		}
		offset += len(reqs)
		if len(reqs) == 0 || offset >= resp.Items[0].TotalJobsCount {
			break
		}
	}
	return postings, nil
}

func (a *Adapter) toPosting(r requisition) job.Posting {
	p := job.Posting{
		Site:           a.SiteName,
		ExternalID:     r.ID,
		Title:          strings.TrimSpace(r.Title),
		Company:        a.Company,
		URL:            fmt.Sprintf("https://%s/hcmUI/CandidateExperience/en/sites/%s/job/%s", a.Host, a.SiteNumber, r.ID),
		Location:       r.PrimaryLocation,
		EmploymentType: job.EmploymentTypeFromTitle(r.Title),
		PostedAt:       time.Now().UTC(),
	}
	if p.EmploymentType == "" {
		p.EmploymentType = job.CanonicalEmploymentType(strings.ToLower(r.JobSchedule))
	}
	if t, err := time.Parse("2006-01-02", r.PostedDate); err == nil {
		p.ApplicationStart = &t
	}
	if desc, err := a.fetchDescription(r.ID); err == nil {
		p.Description = desc
	}
	p.MinYearsExperience = job.ExtractMinYearsExperience(p.Title + "\n" + p.Description)
	return p
}

func (a *Adapter) fetchDescription(id string) (string, error) {
	target := fmt.Sprintf("https://%s/hcmRestApi/resources/latest/recruitingCEJobRequisitionDetails?expand=all&onlyData=true"+
		"&finder=ById;Id=%%22%s%%22,siteNumber=%s", a.Host, id, a.SiteNumber)
	var resp struct {
		Items []struct {
			Description      string `json:"ExternalDescriptionStr"`
			Responsibilities string `json:"ExternalResponsibilitiesStr"`
			Qualifications   string `json:"ExternalQualificationsStr"`
		} `json:"items"`
	}
	if err := a.getJSON(target, &resp); err != nil {
		return "", err
	}
	if len(resp.Items) == 0 {
		return "", fmt.Errorf("no details for %s", id)
	}
	d := resp.Items[0]
	var parts []string
	for _, html := range []string{d.Description, d.Responsibilities, d.Qualifications} {
		if text := htmlutil.StripToText(html); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func (a *Adapter) getJSON(target string, out any) error {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
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
