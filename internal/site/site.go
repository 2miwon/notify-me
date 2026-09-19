// Package site defines the pluggable interface each job site's crawler
// adapter implements. Add a new site by adding a new package under
// internal/site/<name> and registering it in cmd/crawler/main.go — the
// dedupe, filtering, and Notion-writing logic never needs to change.
package site

import "github.com/2miwon/notify-me/internal/job"

// Adapter fetches the current job listings from one site.
type Adapter interface {
	// Name identifies the site, used as job.Posting.Site and for logging.
	Name() string
	// Fetch returns the current listings. It should return whatever it
	// successfully parsed plus a non-nil error on partial failure, rather
	// than discarding results because of one bad item.
	Fetch() ([]job.Posting, error)
}
