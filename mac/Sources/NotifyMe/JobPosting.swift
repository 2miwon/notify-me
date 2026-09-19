import Foundation

/// One card in the feed. Field names intentionally mirror the Notion
/// property names in `internal/notion/schema.go` on the Go side.
///
/// `seen`/`bookmarked`/`hidden` are `var` because this app writes them
/// back to Notion (see `NotionClient.setCheckbox`) and updates its local
/// copy optimistically; `expired` is set only by the Go crawler, so the
/// app only ever reads it.
struct JobPosting: Identifiable, Hashable {
    let id: String
    let title: String
    let company: String
    let url: URL?
    let site: String
    let location: String
    let firstSeen: Date?

    var seen: Bool
    var bookmarked: Bool
    var hidden: Bool
    let expired: Bool

    // Optional — empty/nil for a site whose adapter doesn't report this
    // (e.g. wanted), populated for one that does (currently naver).
    let employmentType: String
    let careerLevel: String
    let applicationStart: Date?
    let applicationDeadline: Date?
    /// Best-effort years-of-experience requirement extracted from the
    /// posting's own text (see internal/job/experience.go on the Go
    /// side) — not something any site reports as structured data, so
    /// this is nil far more often than it's wrong, but it can be wrong.
    let minYearsExperience: Int?
    /// Best-effort normalized minimum academic degree requirement extracted
    /// from the posting's title and description on the crawler side.
    let minimumDegree: String

    /// Full posting body text. Present immediately for the Sheets backend
    /// (it's just another column, free with the row); nil for Notion
    /// until `JobStore.fetchDescription(id:)` is called on demand — the
    /// body lives in the page's own content there, a separate API call.
    /// `var` so the detail view can cache the lazily-fetched result.
    var description: String?

    /// Whole calendar days until `applicationDeadline`, or nil if the
    /// site didn't report one (e.g. wanted). 0 = deadline is today,
    /// negative = already past it.
    var daysUntilDeadline: Int? {
        guard let deadline = applicationDeadline else { return nil }
        let calendar = Calendar.current
        return calendar.dateComponents(
            [.day],
            from: calendar.startOfDay(for: Date()),
            to: calendar.startOfDay(for: deadline)
        ).day
    }

    /// "D-3" / "D-DAY" / "D+2" (past deadline) — the conventional Korean
    /// job-posting-deadline label — or nil if there's no deadline to show.
    var dDayLabel: String? {
        guard let days = daysUntilDeadline else { return nil }
        if days > 0 { return "D-\(days)" }
        if days == 0 { return "D-DAY" }
        return "D+\(-days)"
    }
}

enum PostingSortOrder: String, CaseIterable, Identifiable {
    case newest       // First Seen, most recent first (default)
    case oldest       // First Seen, earliest first — "먼저 올라온순"
    case deadlineSoon // Application Deadline ascending — "마감임박순"

    var id: String { rawValue }

    var label: String {
        switch self {
        case .newest: return "최신순"
        case .oldest: return "먼저 올라온순"
        case .deadlineSoon: return "마감임박순"
        }
    }

    /// Ready to hand to `Array.sorted(by:)`.
    var comparator: (JobPosting, JobPosting) -> Bool {
        switch self {
        case .newest:
            return { ($0.firstSeen ?? .distantPast) > ($1.firstSeen ?? .distantPast) }
        case .oldest:
            return { ($0.firstSeen ?? .distantPast) < ($1.firstSeen ?? .distantPast) }
        case .deadlineSoon:
            return { a, b in
                // Postings with no deadline sort last regardless of how
                // close everything else is to running out.
                switch (a.applicationDeadline, b.applicationDeadline) {
                case let (da?, db?): return da < db
                case (nil, nil): return false
                case (nil, _): return false
                case (_, nil): return true
                }
            }
        }
    }
}
