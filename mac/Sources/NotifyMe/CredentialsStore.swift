import Foundation
import Combine

enum StoreBackend: String, CaseIterable, Identifiable {
    case notion
    case sheets

    var id: String { rawValue }

    var label: String {
        switch self {
        case .notion: return "Notion"
        case .sheets: return "Google Sheets"
        }
    }
}

/// Holds the settings for whichever backend is active, plus which one is
/// active. Stored in UserDefaults for simplicity (this is a single-user,
/// local, personal tool). If you later distribute this app beyond your
/// own machine, move these into the Keychain instead — that goes double
/// for the Sheets service-account key, which is a real credential.
final class CredentialsStore: ObservableObject {
    @Published var backend: StoreBackend {
        didSet { UserDefaults.standard.set(backend.rawValue, forKey: Keys.backend) }
    }
    @Published var notionToken: String {
        didSet { UserDefaults.standard.set(notionToken, forKey: Keys.notionToken) }
    }
    @Published var notionDatabaseID: String {
        didSet { UserDefaults.standard.set(notionDatabaseID, forKey: Keys.notionDatabaseID) }
    }
    /// Whole contents of a Google service-account key JSON file, pasted
    /// as-is.
    @Published var sheetsServiceAccountJSON: String {
        didSet { UserDefaults.standard.set(sheetsServiceAccountJSON, forKey: Keys.sheetsServiceAccountJSON) }
    }
    @Published var sheetsSpreadsheetID: String {
        didSet { UserDefaults.standard.set(sheetsSpreadsheetID, forKey: Keys.sheetsSpreadsheetID) }
    }
    @Published var sheetsSheetName: String {
        didSet { UserDefaults.standard.set(sheetsSheetName, forKey: Keys.sheetsSheetName) }
    }
    /// Site names (job.Posting.Site / the Notion "Site" select / the
    /// Sheets "Site" column — same value everywhere) to hide from the
    /// feed entirely. This is a local, instant, reversible mute: it never
    /// touches the stored data, so postings from a muted site are still
    /// sitting in Notion/Sheets and reappear the moment you unmute it —
    /// unlike per-posting Hidden, which is written back to the store and
    /// shared across whoever else reads it.
    @Published var mutedSites: Set<String> {
        didSet { UserDefaults.standard.set(Array(mutedSites), forKey: Keys.mutedSites) }
    }

    private enum Keys {
        static let backend = "store.backend"
        static let notionToken = "notion.token"
        static let notionDatabaseID = "notion.databaseID"
        static let sheetsServiceAccountJSON = "sheets.serviceAccountJSON"
        static let sheetsSpreadsheetID = "sheets.spreadsheetID"
        static let sheetsSheetName = "sheets.sheetName"
        static let mutedSites = "feed.mutedSites"
    }

    init() {
        let defaults = UserDefaults.standard
        backend = StoreBackend(rawValue: defaults.string(forKey: Keys.backend) ?? "") ?? .notion
        notionToken = defaults.string(forKey: Keys.notionToken) ?? ""
        notionDatabaseID = defaults.string(forKey: Keys.notionDatabaseID) ?? ""
        sheetsServiceAccountJSON = defaults.string(forKey: Keys.sheetsServiceAccountJSON) ?? ""
        sheetsSpreadsheetID = defaults.string(forKey: Keys.sheetsSpreadsheetID) ?? ""
        sheetsSheetName = defaults.string(forKey: Keys.sheetsSheetName) ?? ""
        mutedSites = Set(defaults.stringArray(forKey: Keys.mutedSites) ?? [])
    }

    func toggleMuted(site: String) {
        if mutedSites.contains(site) {
            mutedSites.remove(site)
        } else {
            mutedSites.insert(site)
        }
    }

    var isConfigured: Bool {
        switch backend {
        case .notion:
            return !notionToken.isEmpty && !notionDatabaseID.isEmpty
        case .sheets:
            return !sheetsServiceAccountJSON.isEmpty && !sheetsSpreadsheetID.isEmpty
        }
    }

    /// Builds the active JobStore from current settings. The ContentView
    /// never constructs NotionClient/GoogleSheetsStore directly — this is
    /// the one place that decides which backend is live.
    func makeStore() throws -> JobStore {
        switch backend {
        case .notion:
            return NotionClient(token: notionToken, databaseID: notionDatabaseID)
        case .sheets:
            return try GoogleSheetsStore(
                serviceAccountJSON: Data(sheetsServiceAccountJSON.utf8),
                spreadsheetID: sheetsSpreadsheetID,
                sheetName: sheetsSheetName
            )
        }
    }
}
