import Foundation

/// Posting bodies, kept so the search box can match description text.
///
/// Notion's database query never returns page content, so a body costs
/// one extra request per posting. Fetching all of them on every refresh
/// would be far too slow, so this fetches each body once in the
/// background (newest postings first, paced under Notion's ~3 requests/
/// second limit) and keeps them on disk — after the first full pass only
/// new postings need fetching. Sheets rows already carry the body, so
/// those are indexed instantly with no requests.
///
/// Keyed by posting URL rather than `JobPosting.id`: a Sheets id is a row
/// number, which shifts when rows above it are deleted.
@MainActor
final class DescriptionIndex: ObservableObject {
    /// Normalized body text, as shown in the detail view.
    @Published private(set) var texts: [String: String] = [:]
    /// Postings still waiting for their body; 0 once everything is indexed.
    @Published private(set) var pendingCount = 0

    /// Lowercased copies of `texts`, so a search doesn't re-fold every
    /// body on every keystroke.
    private var lowered: [String: String] = [:]
    private var queue: [JobPosting] = []
    private var worker: Task<Void, Never>?
    /// Bodies that failed to load this session — retried on next launch
    /// rather than hammered on every refresh.
    private var failedKeys: Set<String> = []
    private var saveTask: Task<Void, Never>?

    private static let requestGap: Duration = .milliseconds(400)
    private static let publishEvery = 20

    init() {
        load()
    }

    static func key(for posting: JobPosting) -> String {
        posting.url?.absoluteString ?? posting.id
    }

    func text(for posting: JobPosting) -> String? {
        texts[Self.key(for: posting)]
    }

    /// Whether this posting's body contains `query` (already lowercased).
    func bodyContains(_ posting: JobPosting, loweredQuery: String) -> Bool {
        lowered[Self.key(for: posting)]?.contains(loweredQuery) ?? false
    }

    func set(_ text: String, for posting: JobPosting) {
        store(text, key: Self.key(for: posting))
        scheduleSave()
    }

    /// Queues bodies for every posting not indexed yet. Safe to call on
    /// every refresh: an indexing pass already underway keeps going with
    /// the updated queue instead of starting over.
    func index(_ postings: [JobPosting], makeStore: @escaping () throws -> JobStore) {
        // Drop bodies of postings no longer in the store (the crawler
        // deletes expired ones; applied ones stay, so their bodies do too).
        // `postings` is always a complete fetch — a failed refresh throws
        // before getting here — so nothing live is pruned by mistake.
        let live = Set(postings.map(Self.key(for:)))
        let stale = texts.keys.filter { !live.contains($0) }
        for key in stale {
            texts[key] = nil
            lowered[key] = nil
        }
        failedKeys.formIntersection(live)

        var inline = !stale.isEmpty
        for posting in postings {
            if let body = posting.description, texts[Self.key(for: posting)] == nil {
                store(normalize(body), key: Self.key(for: posting))
                inline = true
            }
        }
        if inline { scheduleSave() }

        queue = postings
            .filter {
                let key = Self.key(for: $0)
                return texts[key] == nil && !failedKeys.contains(key)
            }
            .sorted { ($0.firstSeen ?? .distantPast) > ($1.firstSeen ?? .distantPast) }
        pendingCount = queue.count
        guard worker == nil, !queue.isEmpty else { return }

        worker = Task { [weak self] in
            await self?.drain(makeStore: makeStore)
        }
    }

    private func drain(makeStore: @escaping () throws -> JobStore) async {
        defer { worker = nil }
        guard let jobStore = try? makeStore() else { return }
        var sincePublish = 0
        var fetched: [(String, String)] = []

        func publish() {
            for (key, text) in fetched { store(text, key: key) }
            fetched.removeAll()
            pendingCount = queue.count
            scheduleSave()
        }

        while !Task.isCancelled, !queue.isEmpty {
            let posting = queue.removeFirst()
            let key = Self.key(for: posting)
            if texts[key] != nil { continue }
            do {
                let body = try await jobStore.fetchDescription(id: posting.id)
                fetched.append((key, normalize(body)))
            } catch {
                failedKeys.insert(key)
            }
            sincePublish += 1
            // Batch updates: every published change re-renders the board.
            if sincePublish >= Self.publishEvery {
                sincePublish = 0
                publish()
            }
            try? await Task.sleep(for: Self.requestGap)
        }
        publish()
    }

    private func store(_ text: String, key: String) {
        texts[key] = text
        lowered[key] = text.lowercased()
    }

    // MARK: - Persistence

    nonisolated private static var fileURL: URL? {
        guard let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else {
            return nil
        }
        return dir.appendingPathComponent("NotifyMe", isDirectory: true)
            .appendingPathComponent("descriptions.json")
    }

    private func load() {
        guard let url = Self.fileURL,
              let data = try? Data(contentsOf: url),
              let saved = try? JSONDecoder().decode([String: String].self, from: data)
        else { return }
        texts = saved
        lowered = saved.mapValues { $0.lowercased() }
    }

    /// Debounced so a burst of indexed bodies writes the file once.
    private func scheduleSave() {
        saveTask?.cancel()
        let snapshot = texts
        saveTask = Task.detached(priority: .utility) {
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled, let url = Self.fileURL else { return }
            try? FileManager.default.createDirectory(
                at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            if let data = try? JSONEncoder().encode(snapshot) {
                try? data.write(to: url, options: .atomic)
            }
        }
    }

    /// Same cleanup the detail view always applied: trim, drop blank and
    /// repeated lines, and undo the escaped dots Notion's export adds.
    private func normalize(_ source: String) -> String {
        var lines: [String] = []
        for rawLine in source.components(separatedBy: .newlines) {
            let line = rawLine.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !line.isEmpty, lines.last != line else { continue }
            lines.append(line.replacingOccurrences(of: "\\.", with: "."))
        }
        return lines.joined(separator: "\n")
    }
}
