// Package job defines the site-agnostic posting type shared by every
// crawler adapter and every store backend.
package job

import "time"

// Posting is one job listing, normalized from whatever shape the source
// site returns.
type Posting struct {
	// Site is the adapter name that produced this posting (e.g. "wanted").
	Site string
	// ExternalID is the posting's ID on the source site. Combined with Site
	// it uniquely identifies a posting even if the URL scheme changes.
	ExternalID string
	Title      string
	Company    string
	URL        string
	Location   string
	// PostedAt is when the crawler first observed the posting. Most job
	// sites don't expose a reliable "posted at" timestamp in their listing
	// APIs, so this is a first-seen time, not the site's own posting date.
	PostedAt time.Time

	// The fields below are optional: nil/empty means the source site
	// doesn't expose that information, not "unknown". A Store should skip
	// writing an absent field rather than writing a zero value over it.

	// EmploymentType is normalized to a common vocabulary across sites
	// ("Full-time"/"Contract"/"Internship"/"Temporary") — each adapter
	// maps its own site's native labels onto this set (see
	// internal/site/naver's employmentTypeNames) so a single
	// config/keywords.yaml employment_types entry works regardless of
	// which site reported it.
	EmploymentType string
	// CareerLevel is the site's own label, e.g. "신입"/"경력"/"무관".
	CareerLevel string
	// ApplicationStart/ApplicationDeadline are the site's real posting
	// window, when it publishes one (unlike PostedAt, which is ours).
	ApplicationStart    *time.Time
	ApplicationDeadline *time.Time
	// Description is the full posting body as plain text, when the
	// adapter fetches the detail page for it.
	Description string
	// MinYearsExperience is a best-effort guess at the posting's years-
	// of-experience requirement, extracted from title+description by
	// ExtractMinYearsExperience — see its docs for accuracy caveats. Not
	// something any site reports as structured data.
	MinYearsExperience *int
	// MinimumDegree is a normalized minimum academic degree requirement
	// extracted from title and description. Empty means the source text did
	// not state a recognizable requirement.
	MinimumDegree string
}
