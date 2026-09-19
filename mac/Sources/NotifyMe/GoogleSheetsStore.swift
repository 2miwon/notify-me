import Foundation

/// JobStore backed by a Google Sheet — the alternative to Notion. Column
/// layout matches internal/sheets/sheets.go on the Go side exactly, so
/// both the crawler and this app agree on what column each field lives
/// in without needing to share code.
struct GoogleSheetsStore: JobStore {
    private let key: GoogleServiceAccountKey
    private let spreadsheetID: String
    private let sheetName: String

    private static let scope = "https://www.googleapis.com/auth/spreadsheets"
    private static let columnLetters = ["A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P", "Q"]

    private enum Col: Int {
        case title, company, url, site, location, firstSeen, seen, bookmarked, hidden, expired,
             employmentType, careerLevel, applicationStart, applicationDeadline, description,
             minYearsExperience, minimumDegree
    }

    /// `serviceAccountJSON` is the whole contents of a downloaded Google
    /// service-account key file — the same one used for
    /// GOOGLE_SERVICE_ACCOUNT_JSON on the crawler side.
    init(serviceAccountJSON: Data, spreadsheetID: String, sheetName: String) throws {
        self.key = try JSONDecoder().decode(GoogleServiceAccountKey.self, from: serviceAccountJSON)
        self.spreadsheetID = spreadsheetID
        self.sheetName = sheetName.isEmpty ? "Postings" : sheetName
    }

    func fetchPostings() async throws -> [JobPosting] {
        let url = valuesURL(range: "\(sheetName)!A2:\(Self.columnLetters.last!)", query: [
            URLQueryItem(name: "valueRenderOption", value: "UNFORMATTED_VALUE")
        ])
        let request = try await authorizedRequest(url: url, method: "GET")
        let (data, response) = try await URLSession.shared.data(for: request)
        try Self.checkOK(response, data)

        struct ValueRange: Decodable { let values: [[SheetCell]]? }
        let decoded = try JSONDecoder().decode(ValueRange.self, from: data)

        let postings: [JobPosting] = (decoded.values ?? []).enumerated().compactMap { index, row in
            let rowNumber = index + 2 // row 1 is the header
            guard let urlString = cell(row, .url) else { return nil }
            return JobPosting(
                id: String(rowNumber),
                title: cell(row, .title) ?? "(no title)",
                company: cell(row, .company) ?? "",
                url: URL(string: urlString),
                site: cell(row, .site) ?? "",
                location: cell(row, .location) ?? "",
                firstSeen: cellDate(row, .firstSeen),
                seen: cellBool(row, .seen),
                bookmarked: cellBool(row, .bookmarked),
                hidden: cellBool(row, .hidden),
                expired: cellBool(row, .expired),
                employmentType: cell(row, .employmentType) ?? "",
                careerLevel: cell(row, .careerLevel) ?? "",
                applicationStart: cellDate(row, .applicationStart),
                applicationDeadline: cellDate(row, .applicationDeadline),
                minYearsExperience: cellInt(row, .minYearsExperience),
                minimumDegree: cell(row, .minimumDegree) ?? "",
                description: cell(row, .description)
            )
        }

        return postings.sorted { ($0.firstSeen ?? .distantPast) > ($1.firstSeen ?? .distantPast) }
    }

    /// Sheets already returns Description as part of every row, so this
    /// exists only to satisfy JobStore for callers that don't have a
    /// freshly-fetched JobPosting handy (or want to refresh a stale one).
    func fetchDescription(id: String) async throws -> String {
        let url = valuesURL(range: "\(sheetName)!\(Self.columnLetters[Col.description.rawValue])\(id)", query: [])
        let request = try await authorizedRequest(url: url, method: "GET")
        let (data, response) = try await URLSession.shared.data(for: request)
        try Self.checkOK(response, data)

        struct ValueRange: Decodable { let values: [[SheetCell]]? }
        let decoded = try JSONDecoder().decode(ValueRange.self, from: data)
        return decoded.values?.first?.first?.stringValue ?? ""
    }

    /// Writes exactly one cell — the row's Expired/Title/etc are never
    /// included in this request, so they can't be clobbered.
    func setCheckbox(id: String, property: WritableProperty, value: Bool) async throws {
        let col: Col
        switch property {
        case .seen: col = .seen
        case .bookmarked: col = .bookmarked
        case .hidden: col = .hidden
        }

        let url = valuesURL(range: "\(sheetName)!\(Self.columnLetters[col.rawValue])\(id)",
                             query: [URLQueryItem(name: "valueInputOption", value: "RAW")])
        var request = try await authorizedRequest(url: url, method: "PUT")
        request.httpBody = try JSONSerialization.data(withJSONObject: ["values": [[value]]])

        let (data, response) = try await URLSession.shared.data(for: request)
        try Self.checkOK(response, data)
    }

    // MARK: - HTTP plumbing

    private func valuesURL(range: String, query: [URLQueryItem]) -> URL {
        let encodedRange = range.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? range
        var components = URLComponents(
            string: "https://sheets.googleapis.com/v4/spreadsheets/\(spreadsheetID)/values/\(encodedRange)")!
        components.queryItems = query
        return components.url!
    }

    private func authorizedRequest(url: URL, method: String) async throws -> URLRequest {
        let token = try await GoogleTokenCache.shared.token(for: key, scope: Self.scope)
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        return request
    }

    private static func checkOK(_ response: URLResponse, _ data: Data) throws {
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            let status = (response as? HTTPURLResponse)?.statusCode ?? -1
            throw GoogleSheetsError.http(status, String(data: data, encoding: .utf8) ?? "")
        }
    }

    // MARK: - Cell access

    private func cell(_ row: [SheetCell], _ col: Col) -> String? {
        guard col.rawValue < row.count else { return nil }
        let value = row[col.rawValue].stringValue
        return value.isEmpty ? nil : value
    }

    private func cellBool(_ row: [SheetCell], _ col: Col) -> Bool {
        guard col.rawValue < row.count else { return false }
        return row[col.rawValue].boolValue
    }

    private func cellDate(_ row: [SheetCell], _ col: Col) -> Date? {
        guard let s = cell(row, col) else { return nil }
        return ISO8601DateFormatter().date(from: s)
    }

    private func cellInt(_ row: [SheetCell], _ col: Col) -> Int? {
        guard col.rawValue < row.count else { return nil }
        if case .number(let n) = row[col.rawValue] { return Int(n) }
        return nil
    }
}

enum GoogleSheetsError: Error, LocalizedError {
    case http(Int, String)
    var errorDescription: String? {
        switch self {
        case .http(let code, let body): return "Google Sheets API error \(code): \(body)"
        }
    }
}

/// The Sheets API returns each cell as whatever JSON type matches its
/// content (string, number, or boolean) under UNFORMATTED_VALUE — this
/// decodes any of the three instead of assuming one.
enum SheetCell: Decodable {
    case string(String)
    case bool(Bool)
    case number(Double)
    case empty

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let b = try? container.decode(Bool.self) {
            self = .bool(b)
        } else if let s = try? container.decode(String.self) {
            self = .string(s)
        } else if let n = try? container.decode(Double.self) {
            self = .number(n)
        } else {
            self = .empty
        }
    }

    var stringValue: String {
        switch self {
        case .string(let s): return s
        case .bool(let b): return b ? "TRUE" : "FALSE"
        case .number(let n): return String(n)
        case .empty: return ""
        }
    }

    var boolValue: Bool {
        switch self {
        case .bool(let b): return b
        case .string(let s): return s.caseInsensitiveCompare("TRUE") == .orderedSame
        case .number, .empty: return false
        }
    }
}
