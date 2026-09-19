import Foundation

/// Client for the Notion database the crawler writes new postings into.
/// The app queries it for the feed, and writes back the three user-driven
/// checkboxes (Seen/Bookmarked/Hidden) — never Title/Company/URL/Expired,
/// which stay the crawler's job. The integration secret needs "Update
/// content" capability enabled in Notion for the writes to succeed.
struct NotionClient: JobStore {
    var token: String
    var databaseID: String

    private let apiVersion = "2022-06-28"

    enum NotionError: Error, LocalizedError {
        case http(Int, String)
        var errorDescription: String? {
            switch self {
            case .http(let code, let body): return "Notion API error \(code): \(body)"
            }
        }
    }

    /// Fetches every page in the database, newest first, mapped to
    /// `JobPosting`. Paginates until Notion reports no more results.
    func fetchPostings() async throws -> [JobPosting] {
        var postings: [JobPosting] = []
        var cursor: String? = nil

        repeat {
            let (page, nextCursor) = try await fetchPage(startCursor: cursor)
            postings.append(contentsOf: page)
            cursor = nextCursor
        } while cursor != nil

        return postings.sorted { ($0.firstSeen ?? .distantPast) > ($1.firstSeen ?? .distantPast) }
    }

    /// Sets one checkbox property on one page. Notion's page update only
    /// changes properties present in the request body, so this can't
    /// accidentally clobber Title/URL/Expired/etc.
    func setCheckbox(id: String, property: WritableProperty, value: Bool) async throws {
        let url = URL(string: "https://api.notion.com/v1/pages/\(id)")!
        var request = URLRequest(url: url)
        request.httpMethod = "PATCH"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue(apiVersion, forHTTPHeaderField: "Notion-Version")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        let body: [String: Any] = [
            "properties": [property.rawValue: ["checkbox": value]]
        ]
        request.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            let status = (response as? HTTPURLResponse)?.statusCode ?? -1
            throw NotionError.http(status, String(data: data, encoding: .utf8) ?? "")
        }
    }

    /// Reads a page's body content (what CreatePosting on the Go side
    /// wrote as paragraph blocks) and joins it back into plain text.
    /// Notion's database query never returns page content — it's a
    /// separate resource — so this is always an extra round trip,
    /// unlike Sheets where the description rides along with the row.
    func fetchDescription(id: String) async throws -> String {
        var paragraphs: [String] = []
        var cursor: String? = nil

        repeat {
            var components = URLComponents(string: "https://api.notion.com/v1/blocks/\(id)/children")!
            var query = [URLQueryItem(name: "page_size", value: "100")]
            if let cursor { query.append(URLQueryItem(name: "start_cursor", value: cursor)) }
            components.queryItems = query

            var request = URLRequest(url: components.url!)
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
            request.setValue(apiVersion, forHTTPHeaderField: "Notion-Version")

            let (data, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                let status = (response as? HTTPURLResponse)?.statusCode ?? -1
                throw NotionError.http(status, String(data: data, encoding: .utf8) ?? "")
            }

            let decoded = try JSONDecoder().decode(BlockChildrenResponse.self, from: data)
            paragraphs.append(contentsOf: decoded.results.map(\.plainText).filter { !$0.isEmpty })
            cursor = decoded.hasMore ? decoded.nextCursor : nil
        } while cursor != nil

        return paragraphs.joined(separator: "\n\n")
    }

    private func fetchPage(startCursor: String?) async throws -> ([JobPosting], String?) {
        let url = URL(string: "https://api.notion.com/v1/databases/\(databaseID)/query")!
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue(apiVersion, forHTTPHeaderField: "Notion-Version")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")

        var body: [String: Any] = ["page_size": 100]
        if let startCursor { body["start_cursor"] = startCursor }
        request.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, response) = try await URLSession.shared.data(for: request)
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            let status = (response as? HTTPURLResponse)?.statusCode ?? -1
            throw NotionError.http(status, String(data: data, encoding: .utf8) ?? "")
        }

        let decoded = try JSONDecoder().decode(QueryResponse.self, from: data)
        let postings = decoded.results.map { $0.toJobPosting() }
        return (postings, decoded.hasMore ? decoded.nextCursor : nil)
    }
}

// MARK: - Notion API response shapes

private struct QueryResponse: Decodable {
    let results: [NotionPage]
    let hasMore: Bool
    let nextCursor: String?

    enum CodingKeys: String, CodingKey {
        case results
        case hasMore = "has_more"
        case nextCursor = "next_cursor"
    }
}

private struct BlockChildrenResponse: Decodable {
    let results: [NotionBlock]
    let hasMore: Bool
    let nextCursor: String?

    enum CodingKeys: String, CodingKey {
        case results
        case hasMore = "has_more"
        case nextCursor = "next_cursor"
    }
}

/// Only handles the one block type CreatePosting (Go side) ever writes —
/// paragraphs. Any other block type just contributes empty text.
private struct NotionBlock: Decodable {
    let paragraph: Paragraph?

    struct Paragraph: Decodable {
        let richText: [RichTextItem]
        enum CodingKeys: String, CodingKey { case richText = "rich_text" }
    }

    struct RichTextItem: Decodable {
        let plainText: String
        enum CodingKeys: String, CodingKey { case plainText = "plain_text" }
    }

    var plainText: String {
        paragraph?.richText.map(\.plainText).joined() ?? ""
    }
}

private struct NotionPage: Decodable {
    let id: String
    let properties: [String: NotionProperty]

    func toJobPosting() -> JobPosting {
        JobPosting(
            id: id,
            title: properties["Title"]?.plainText ?? "(no title)",
            company: properties["Company"]?.plainText ?? "",
            url: properties["URL"]?.urlValue,
            site: properties["Site"]?.selectName ?? "",
            location: properties["Location"]?.plainText ?? "",
            firstSeen: properties["First Seen"]?.dateStart,
            seen: properties["Seen"]?.boolValue ?? false,
            bookmarked: properties["Bookmarked"]?.boolValue ?? false,
            hidden: properties["Hidden"]?.boolValue ?? false,
            expired: properties["Expired"]?.boolValue ?? false,
            employmentType: properties["Employment Type"]?.selectName ?? "",
            careerLevel: properties["Career Level"]?.selectName ?? "",
            applicationStart: properties["Application Start"]?.dateStart,
            applicationDeadline: properties["Application Deadline"]?.dateStart,
            minYearsExperience: properties["Min Years Experience"]?.intValue,
            minimumDegree: properties["Minimum Degree"]?.selectName ?? "",
            description: nil // fetched on demand — see fetchDescription
        )
    }
}

/// Notion property values are tagged unions on the "type" field. This
/// decodes just the handful of shapes this app's schema actually uses
/// (title, rich_text, url, select, date, checkbox) and exposes them
/// uniformly.
private struct NotionProperty: Decodable {
    let type: String
    let title: [RichTextItem]?
    let richText: [RichTextItem]?
    let url: String?
    let select: SelectValue?
    let date: DateValue?
    let checkbox: Bool?
    let number: Double?

    enum CodingKeys: String, CodingKey {
        case type, title, url, select, date, checkbox, number
        case richText = "rich_text"
    }

    struct RichTextItem: Decodable {
        let plainText: String
        enum CodingKeys: String, CodingKey { case plainText = "plain_text" }
    }

    struct SelectValue: Decodable {
        let name: String
    }

    struct DateValue: Decodable {
        let start: String
    }

    var plainText: String {
        (title ?? richText)?.map(\.plainText).joined() ?? ""
    }

    var urlValue: URL? {
        url.flatMap(URL.init(string:))
    }

    var selectName: String? {
        select?.name
    }

    var boolValue: Bool {
        checkbox ?? false
    }

    var intValue: Int? {
        number.map { Int($0) }
    }

    var dateStart: Date? {
        guard let start = date?.start else { return nil }
        return ISO8601DateFormatter.notion.date(from: start)
            ?? ISO8601DateFormatter.notionDateOnly.date(from: start)
    }
}

private extension ISO8601DateFormatter {
    static let notion: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
    static let notionDateOnly: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withFullDate]
        return f
    }()
}
