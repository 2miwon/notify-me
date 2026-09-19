import Foundation

/// Property names this app is allowed to write, mirroring the checkbox
/// constants in internal/notion/schema.go and internal/sheets/sheets.go
/// on the Go side. Title/Company/URL/Site/Location/First Seen/Expired are
/// never written from here — those are the crawler's job.
enum WritableProperty: String {
    case seen = "Seen"
    case bookmarked = "Bookmarked"
    case hidden = "Hidden"
}

/// One place postings can be read from and written back to. Notion and
/// Google Sheets both implement this the same way the Go crawler's
/// internal/store.Store lets either backend serve as "the database" —
/// the app's UI only ever talks to this protocol, never a concrete
/// backend type.
protocol JobStore {
    func fetchPostings() async throws -> [JobPosting]
    /// Sets one checkbox on one posting, identified by `JobPosting.id`
    /// (a Notion page ID or a spreadsheet row number, depending on the
    /// backend — opaque to callers either way).
    func setCheckbox(id: String, property: WritableProperty, value: Bool) async throws
    /// Fetches a posting's full body text on demand. For Sheets this is
    /// already on `JobPosting.description` from `fetchPostings` — callers
    /// should check that first and only call this as a fallback/refresh.
    func fetchDescription(id: String) async throws -> String
}
