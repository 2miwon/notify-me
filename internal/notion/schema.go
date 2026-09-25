package notion

// Notion database property names. Kept as constants (not scattered string
// literals) so relabeling a column in Notion only requires changing it
// here, not in the create/dedupe logic.
//
// Expected property types when creating the database in Notion:
//
//	Title                 -- Title (Notion requires exactly one Title property per DB)
//	Company               -- Text
//	URL                   -- URL   (used as the dedupe key)
//	Site                  -- Select
//	Location              -- Text
//	First Seen            -- Date
//	Seen                  -- Checkbox (set by the macOS app when a card is opened)
//	Bookmarked            -- Checkbox (set by the macOS app)
//	Hidden                -- Checkbox (set by the macOS app; hides the card from the feed)
//	Applied               -- Checkbox (set by the macOS app when you've applied)
//	Expired               -- Checkbox (set by the crawler when a posting drops out of
//	                         a site's live listing)
//	Employment Type       -- Select (optional; normalized across sites to
//	                         Full-time/Contract/Internship/Temporary — only
//	                         set when the source site provides it)
//	Career Level          -- Select (optional; e.g. 신입/경력/무관)
//	Application Start     -- Date (optional; the site's own posting window, not
//	                         to be confused with First Seen)
//	Application Deadline  -- Date (optional)
//	Min Years Experience  -- Number (optional; best-effort guess extracted from
//	                         the posting's own text, see internal/job.ExtractMinYearsExperience)
//	Minimum Degree        -- Select (optional; normalized minimum academic degree
//	                         extracted from the posting's own text)
//
// A posting's full description (when the adapter fetches one) goes into
// the page body as paragraph blocks, not a property — see CreatePosting.
const (
	PropTitle               = "Title"
	PropCompany             = "Company"
	PropURL                 = "URL"
	PropSite                = "Site"
	PropLocation            = "Location"
	PropFirstSeen           = "First Seen"
	PropSeen                = "Seen"
	PropBookmarked          = "Bookmarked"
	PropHidden              = "Hidden"
	PropApplied             = "Applied"
	PropExpired             = "Expired"
	PropEmploymentType      = "Employment Type"
	PropCareerLevel         = "Career Level"
	PropApplicationStart    = "Application Start"
	PropApplicationDeadline = "Application Deadline"
	PropMinYearsExperience  = "Min Years Experience"
	PropMinimumDegree       = "Minimum Degree"
)
