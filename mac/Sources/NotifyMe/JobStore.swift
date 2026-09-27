import Foundation

/// Property names this app is allowed to write, mirroring the checkbox
/// constants in internal/notion/schema.go and internal/sheets/sheets.go
/// on the Go side. Title/Company/URL/Site/Location/First Seen/Expired are
/// never written from here — those are the crawler's job.
enum WritableProperty: String {
    case seen = "Seen"
    case bookmarked = "Bookmarked"
    case hidden = "Hidden"
    case applied = "Applied"
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

/// Sends a write request, retrying when the service says "slow down"
/// (HTTP 429) or hiccups (5xx). Notion allows ~3 requests/second and
/// Sheets ~60 writes/minute, so a burst of hide/bookmark clicks used to
/// get rejected — and a rejected write looked like the click was ignored.
func dataWithRetry(for request: URLRequest, attempts: Int = 5) async throws -> (Data, URLResponse) {
    var attempt = 0
    while true {
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        attempt += 1
        guard (status == 429 || status >= 500) && attempt < attempts else {
            return (data, response)
        }
        let retryAfter = (response as? HTTPURLResponse)?
            .value(forHTTPHeaderField: "Retry-After").flatMap(Double.init)
        let delay = retryAfter ?? min(Double(1 << (attempt - 1)), 8)
        try await Task.sleep(for: .seconds(delay))
    }
}

/// Runs checkbox writes one at a time, in click order. Parallel writes
/// are what trip the rate limits above, and two writes to the same
/// posting could otherwise land out of order (last click losing).
actor WriteQueue {
    private var tail: Task<Void, Never>?

    func run(_ operation: @escaping @Sendable () async throws -> Void) async throws {
        let previous = tail
        let task = Task<Result<Void, Error>, Never> {
            await previous?.value
            do { try await operation(); return .success(()) } catch { return .failure(error) }
        }
        tail = Task { _ = await task.value }
        try await task.value.get()
    }
}
