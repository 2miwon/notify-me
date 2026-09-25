import SwiftUI

struct SettingsView: View {
    @ObservedObject var credentials: CredentialsStore
    @Binding var isPresented: Bool
    var onSave: () -> Void

    @State private var backend: StoreBackend = .notion
    @State private var notionToken: String = ""
    @State private var notionJobsDatabaseID: String = ""
    @State private var notionFreelanceDatabaseID: String = ""
    @State private var notionOpportunitiesDatabaseID: String = ""
    @State private var sheetsServiceAccountJSON: String = ""
    @State private var sheetsSpreadsheetID: String = ""
    @State private var sheetsSheetName: String = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Data Source")
                .font(.headline)

            Picker("Backend", selection: $backend) {
                ForEach(StoreBackend.allCases) { backend in
                    Text(backend.label).tag(backend)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()

            switch backend {
            case .notion:
                notionFields
            case .sheets:
                sheetsFields
            }

            HStack {
                Spacer()
                Button("Cancel") { isPresented = false }
                Button("Save") {
                    credentials.backend = backend
                    credentials.notionToken = notionToken
                    credentials.notionJobsDatabaseID = notionJobsDatabaseID
                    credentials.notionFreelanceDatabaseID = notionFreelanceDatabaseID
                    credentials.notionOpportunitiesDatabaseID = notionOpportunitiesDatabaseID
                    credentials.sheetsServiceAccountJSON = sheetsServiceAccountJSON
                    credentials.sheetsSpreadsheetID = sheetsSpreadsheetID
                    credentials.sheetsSheetName = sheetsSheetName
                    isPresented = false
                    onSave()
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(20)
        .frame(width: 380)
        .onAppear {
            backend = credentials.backend
            notionToken = credentials.notionToken
            notionJobsDatabaseID = credentials.notionJobsDatabaseID
            notionFreelanceDatabaseID = credentials.notionFreelanceDatabaseID
            notionOpportunitiesDatabaseID = credentials.notionOpportunitiesDatabaseID
            sheetsServiceAccountJSON = credentials.sheetsServiceAccountJSON
            sheetsSpreadsheetID = credentials.sheetsSpreadsheetID
            sheetsSheetName = credentials.sheetsSheetName
        }
    }

    private var notionFields: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Internal Integration Secret")
                .font(.caption)
                .foregroundStyle(.secondary)
            SecureField("secret_...", text: $notionToken)
                .textFieldStyle(.roundedBorder)

            Text("Jobs database ID")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextField("32-character ID from the Jobs database URL", text: $notionJobsDatabaseID)
                .textFieldStyle(.roundedBorder)

            Text("Freelance database ID")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextField("32-character ID (optional for now)", text: $notionFreelanceDatabaseID)
                .textFieldStyle(.roundedBorder)

            Text("Competition / opportunity database ID")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextField("32-character ID (optional for now)", text: $notionOpportunitiesDatabaseID)
                .textFieldStyle(.roundedBorder)
        }
    }

    private var sheetsFields: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Service Account Key (whole JSON file contents)")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextEditor(text: $sheetsServiceAccountJSON)
                .font(.system(.caption, design: .monospaced))
                .frame(height: 100)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(.separator))

            Text("Spreadsheet ID")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextField("from the sheet's URL", text: $sheetsSpreadsheetID)
                .textFieldStyle(.roundedBorder)

            Text("Sheet/Tab Name (optional, default \"Postings\")")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextField("Postings", text: $sheetsSheetName)
                .textFieldStyle(.roundedBorder)
        }
    }
}
