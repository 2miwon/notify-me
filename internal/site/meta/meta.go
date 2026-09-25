// Package meta implements the one browser-rendered adapter in this project.
// Meta Careers loads search results through a session-bound GraphQL request;
// the server HTML does not contain job cards. We deliberately read the
// rendered public DOM rather than replaying that private request.
package meta

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/2miwon/notify-me/internal/job"
)

const searchURL = "https://www.metacareers.com/jobsearch/?q=seoul"

var jobIDFromURL = regexp.MustCompile(`/jobs/(\d+)`)

// Adapter renders Meta's Seoul search page in Chromium. It is opt-in via
// META_HEADLESS_ENABLED so the normal crawler remains HTTP-only at runtime.
type Adapter struct {
	DevOnly bool
}

func New(devOnly bool) *Adapter { return &Adapter{DevOnly: devOnly} }

func (a *Adapter) Name() string { return "meta" }

type renderedLink struct {
	Href string `json:"href"`
	Text string `json:"text"`
}

func (a *Adapter) Fetch() ([]job.Posting, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	allocatorCtx, cancelAllocator := chromedp.NewExecAllocator(ctx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Headless,
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
			chromedp.Flag("disable-dev-shm-usage", true),
		)...,
	)
	defer cancelAllocator()
	browserCtx, cancelBrowser := chromedp.NewContext(allocatorCtx)
	defer cancelBrowser()

	var links []renderedLink
	const collectJobs = `Array.from(document.querySelectorAll('a[href*="/jobs/"]'))
  .map(a => ({href: a.href, text: a.innerText}))
  .filter(item => item.href && item.text)`
	if err := chromedp.Run(browserCtx,
		chromedp.Navigate(searchURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		// Meta starts its data request after hydration. A bounded wait is more
		// reliable here than waiting for a particular CSS class that changes
		// whenever its front end is redeployed.
		chromedp.Sleep(6*time.Second),
		chromedp.Evaluate(collectJobs, &links),
	); err != nil {
		return nil, fmt.Errorf("meta: render Seoul search: %w", err)
	}
	if len(links) == 0 {
		// A zero-card DOM is ambiguous: there may truly be no Seoul roles,
		// but it can also mean Meta changed its rendered markup or withheld
		// data from this browser session. Returning an error makes the shared
		// crawler skip expiry instead of deleting known postings on uncertainty.
		return nil, fmt.Errorf("meta: no rendered job links; refusing to treat the listing as empty")
	}

	seen := make(map[string]bool)
	postings := make([]job.Posting, 0, len(links))
	for _, link := range links {
		id := jobID(link.Href)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		title := titleFromLink(link.Text)
		if title == "" || (a.DevOnly && !job.IsDevTitle(title)) {
			continue
		}
		postings = append(postings, job.Posting{
			Site:       a.Name(),
			ExternalID: id,
			Title:      title,
			Company:    "Meta",
			URL:        canonicalURL(link.Href),
			// The rendered search itself is constrained to Seoul. Meta's card
			// markup is deliberately volatile, so don't depend on a location
			// child selector merely to repeat the selected query value.
			Location: "Seoul, South Korea",
			PostedAt: time.Now().UTC(),
		})
	}
	return postings, nil
}

func jobID(raw string) string {
	match := jobIDFromURL.FindStringSubmatch(raw)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func canonicalURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func titleFromLink(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			return line
		}
	}
	return ""
}
