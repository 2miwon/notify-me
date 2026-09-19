// Package store defines the pluggable datastore interface the crawler
// writes postings to. Add a new backend (a spreadsheet, a different
// no-code DB, ...) by adding a new package under internal/store/<name>
// implementing Store, and registering it in cmd/crawler/main.go — the
// fetch/filter/dedupe/expiry logic never needs to change. Mirrors how
// internal/site.Adapter makes crawl targets pluggable.
package store

import (
	"context"

	"github.com/2miwon/notify-me/internal/job"
)

// ExistingPosting is what the crawler needs to know about a posting
// already stored in the backend: enough to skip re-creating it, and
// enough to delete it if it drops out of its site's live listing. ID is
// opaque to callers — a Notion page ID, a spreadsheet row number, whatever
// the backend needs to address that record again.
type ExistingPosting struct {
	ID                 string
	Site               string
	Expired            bool
	MinimumDegree      string
	CareerLevel        string
	MinYearsExperience *int
}

// Store is one place postings can be written to and read back from.
// Seen/Bookmarked/Hidden belong to whatever client app reads the same
// backend and are never touched by the crawler. When a posting disappears
// from a fully fetched listing, the crawler removes its whole record.
type Store interface {
	// Name identifies the backend, used in logs.
	Name() string
	// ExistingPostings returns every stored posting keyed by URL.
	ExistingPostings(ctx context.Context) (map[string]ExistingPosting, error)
	// CreatePosting adds one new record for p. Callers should check
	// ExistingPostings first; this does not itself check for duplicates.
	CreatePosting(ctx context.Context, p job.Posting) error
	// UpdateMinimumDegree refreshes the extracted degree for a posting that
	// was already stored before degree persistence was added.
	UpdateMinimumDegree(ctx context.Context, id, degree string) error
	// UpdateCareerLevel corrects metadata already persisted by an adapter.
	UpdateCareerLevel(ctx context.Context, id, level string) error
	// UpdateMinYearsExperience refreshes an extracted career requirement.
	UpdateMinYearsExperience(ctx context.Context, id string, years int) error
	// DeletePosting permanently removes one posting which no longer appears
	// in a successfully fetched source listing.
	DeletePosting(ctx context.Context, id string) error
}
