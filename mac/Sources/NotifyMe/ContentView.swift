import SwiftUI
import AppKit

private enum Workspace: String, CaseIterable, Identifiable {
    case jobs, freelance, opportunities

    var id: Self { self }
    var label: String {
        switch self {
        case .jobs: return "Jobs"
        case .freelance: return "Freelance"
        case .opportunities: return "공모전·대회"
        }
    }
}

private enum JobsPresentation: String, CaseIterable, Identifiable {
    case board
    case list

    var id: Self { self }
    var label: String { self == .board ? "회사별" : "전체" }
    var icon: String { self == .board ? "rectangle.split.3x1" : "list.bullet" }
}

struct ContentView: View {
    @StateObject private var credentials = CredentialsStore()
    @State private var postings: [JobPosting] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var showSettings = false
    @State private var showBookmarkedOnly = false
    @State private var appliedFilter: AppliedFilter = .all
    @State private var searchText = ""
    @State private var employmentTypeFilter: Set<String> = []
    @State private var careerLevelFilter: Set<String> = []
    @State private var minimumDegreeFilter: Set<String> = []
    @State private var sortOrder: PostingSortOrder = .newest
    @State private var detailPostingID: String?
    @State private var descriptionCache: [String: String] = [:]
    @State private var loadingDescriptionIDs: Set<String> = []
    @State private var workspace: Workspace = .jobs
    @State private var jobsPresentation: JobsPresentation = .board

    private let refreshInterval: TimeInterval = 60

    // MARK: - Derived state

    private var availableSites: [String] {
        Set(postings.map(\.site)).sorted()
    }

    /// A site's column only shows up while it actually has a posting
    /// passing the current filters — with ~20 sites now, several are
    /// routinely empty (nothing currently open, or nothing left after a
    /// dev-only site filter), and an always-present empty column for
    /// each one is just clutter. This is automatic and filter-driven,
    /// not persisted — a site reappears the moment a new posting for it
    /// exists, unlike the explicit, sticky mute below.
    private var visibleSites: [String] {
        availableSites.filter { !credentials.mutedSites.contains($0) && !postings(for: $0).isEmpty }
    }

    private var availableEmploymentTypes: [String] {
        Set(postings.map(\.employmentType).filter { !$0.isEmpty }).sorted()
    }

    private var availableCareerLevels: [String] {
        Set(postings.map(\.careerLevel).filter { !$0.isEmpty }).sorted()
    }

    private var availableMinimumDegrees: [String] {
        Set(postings.map(\.minimumDegree).filter { !$0.isEmpty })
            .sorted { degreeSortRank($0) < degreeSortRank($1) }
    }

    /// Every filter except the per-site mute, which is applied per-column
    /// in `board` instead (so a muted site's column just doesn't render,
    /// rather than being an empty column). Sorted last, per `sortOrder`,
    /// so it applies uniformly within each site's column.
    private var filteredPostings: [JobPosting] {
        postings
            // The applied collection is a record of what was sent out, so
            // it keeps postings that were later hidden from the feed.
            .filter { appliedFilter == .appliedOnly || !$0.hidden }
            .filter {
                switch appliedFilter {
                case .all: return true
                case .notApplied: return !$0.applied
                case .appliedOnly: return $0.applied
                }
            }
            .filter { !showBookmarkedOnly || $0.bookmarked }
            .filter { employmentTypeFilter.isEmpty || employmentTypeFilter.contains($0.employmentType) }
            .filter { careerLevelFilter.isEmpty || careerLevelFilter.contains($0.careerLevel) }
            .filter { minimumDegreeFilter.isEmpty || minimumDegreeFilter.contains($0.minimumDegree) }
            .filter {
                searchText.isEmpty
                    || $0.title.localizedCaseInsensitiveContains(searchText)
                    || $0.company.localizedCaseInsensitiveContains(searchText)
            }
            .sorted(by: sortOrder.comparator)
    }

    private func postings(for site: String) -> [JobPosting] {
        filteredPostings.filter { $0.site == site }
    }

    /// The all-company list honours the same filters and sorting as the
    /// board. Site muting is retained except in the applied-history view,
    /// where hidden site columns have never suppressed the user's records.
    private var mixedPostings: [JobPosting] {
        if appliedFilter == .appliedOnly {
            return filteredPostings
        }
        return filteredPostings.filter { !credentials.mutedSites.contains($0.site) }
    }

    /// Looked up live from `postings` (rather than storing a snapshot)
    /// so an in-sheet action like toggling Bookmark shows up immediately.
    private var detailPosting: JobPosting? {
        guard let id = detailPostingID else { return nil }
        return postings.first { $0.id == id }
    }

    // MARK: - Body

    var body: some View {
        ZStack(alignment: .topLeading) {
            VisualEffectView().ignoresSafeArea()

            VStack(spacing: 0) {
                header
                if workspace == .jobs {
                    filterBar
                    if !credentials.isConfigured {
                        settingsPrompt
                    } else if let errorMessage {
                        errorView(errorMessage)
                    } else {
                        if jobsPresentation == .board {
                            board
                        } else {
                            mixedList
                        }
                    }
                } else {
                    workspaceEmptyState
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        }
        .frame(
            minWidth: 720,
            idealWidth: 960,
            maxWidth: .infinity,
            minHeight: 480,
            idealHeight: 640,
            maxHeight: .infinity,
            alignment: .topLeading
        )
        .background(WindowConfigurator())
        .task { await refreshLoop() }
        .onReceive(NotificationCenter.default.publisher(for: .refreshPostings)) { _ in
            Task { await refresh() }
        }
        .sheet(isPresented: $showSettings) {
            SettingsView(credentials: credentials, isPresented: $showSettings) {
                Task { await refresh() }
            }
        }
        .sheet(isPresented: Binding(
            get: { detailPostingID != nil },
            set: { if !$0 { detailPostingID = nil } }
        )) {
            if let posting = detailPosting {
                DetailView(
                    posting: posting,
                    description: descriptionCache[posting.id],
                    isLoadingDescription: loadingDescriptionIDs.contains(posting.id),
                    onOpen: { openPosting(posting) },
                    onToggleBookmark: { toggle(posting, .bookmarked) },
                    onToggleApplied: { toggle(posting, .applied) },
                    onToggleHidden: {
                        toggle(posting, .hidden)
                        detailPostingID = nil // it just vanished from the board
                    },
                    onClose: { detailPostingID = nil }
                )
                .task(id: posting.id) {
                    markSeen(posting)
                    await loadDescriptionIfNeeded(posting)
                }
            }
        }
    }

    // MARK: - Header / filter bar

    private var header: some View {
        HStack {
            Picker("Workspace", selection: $workspace) {
                ForEach(Workspace.allCases) { item in
                    Text(item.label).tag(item)
                }
            }
            .pickerStyle(.segmented)
            .frame(width: 360)
            Spacer()
            if isLoading {
                ProgressView().controlSize(.small)
            }
            Button {
                showSettings = true
            } label: {
                Image(systemName: "gearshape")
            }
            .buttonStyle(.plain)
        }
        .padding(EdgeInsets(top: 14, leading: 16, bottom: 6, trailing: 16))
    }

    private var workspaceEmptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: workspace == .freelance ? "briefcase" : "trophy")
                .font(.system(size: 28))
                .foregroundStyle(.secondary)
            Text("\(workspace.label) workspace")
                .font(.headline)
            Text("별도 Notion 데이터베이스를 연결하면 이 탭에서 관리할 수 있어요.")
                .foregroundStyle(.secondary)
            Button("데이터 소스 설정") { showSettings = true }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var filterBar: some View {
        HStack(spacing: 10) {
            HStack(spacing: 4) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                TextField("Search title or company", text: $searchText)
                    .textFieldStyle(.plain)
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 5)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 8))
            .frame(maxWidth: 240)

            multiSelectMenu(title: "Employment", options: availableEmploymentTypes, selection: $employmentTypeFilter)
            multiSelectMenu(title: "Career", options: availableCareerLevels, selection: $careerLevelFilter)
            multiSelectMenu(
                title: "학력",
                options: availableMinimumDegrees,
                selection: $minimumDegreeFilter,
                displayName: degreeLabel
            )

            Menu {
                ForEach(PostingSortOrder.allCases) { order in
                    Button {
                        sortOrder = order
                    } label: {
                        HStack {
                            Text(order.label)
                            if sortOrder == order {
                                Spacer()
                                Image(systemName: "checkmark")
                            }
                        }
                    }
                }
            } label: {
                Label(sortOrder.label, systemImage: "arrow.up.arrow.down")
                    .font(.caption)
            }
            .menuStyle(.borderlessButton)
            .fixedSize()
            .help("Sort")

            Menu {
                ForEach(AppliedFilter.allCases) { filter in
                    Button {
                        appliedFilter = filter
                    } label: {
                        HStack {
                            Text(filter.label)
                            if appliedFilter == filter {
                                Spacer()
                                Image(systemName: "checkmark")
                            }
                        }
                    }
                }
            } label: {
                Label(appliedFilter.label, systemImage: appliedFilter.systemImage)
                    .font(.caption)
                    .foregroundStyle(appliedFilter == .all ? Color.primary : appliedGreen)
            }
            .menuStyle(.borderlessButton)
            .fixedSize()
            .help("지원한 공고 포함 여부")

            Button {
                showBookmarkedOnly.toggle()
            } label: {
                Image(systemName: showBookmarkedOnly ? "star.fill" : "star")
            }
            .buttonStyle(.plain)
            .help("Show bookmarked only")

            Picker("공고 보기", selection: $jobsPresentation) {
                ForEach(JobsPresentation.allCases) { presentation in
                    Image(systemName: presentation.icon)
                        .accessibilityLabel(presentation.label)
                        .tag(presentation)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(width: 64)
            .help(jobsPresentation == .board ? "전체 목록으로 보기" : "회사별 보드로 보기")

            if !credentials.mutedSites.isEmpty {
                Menu {
                    ForEach(availableSites.filter { credentials.mutedSites.contains($0) }, id: \.self) { site in
                        Button("Show \(site)") { credentials.toggleMuted(site: site) }
                    }
                } label: {
                    Label("\(credentials.mutedSites.count) hidden", systemImage: "eye.slash")
                        .font(.caption)
                }
            }

            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.bottom, 10)
    }

    private func multiSelectMenu(
        title: String,
        options: [String],
        selection: Binding<Set<String>>,
        displayName: @escaping (String) -> String = { $0 }
    ) -> some View {
        Menu {
            if options.isEmpty {
                Text("None yet")
            }
            ForEach(options, id: \.self) { option in
                Button {
                    if selection.wrappedValue.contains(option) {
                        selection.wrappedValue.remove(option)
                    } else {
                        selection.wrappedValue.insert(option)
                    }
                } label: {
                    HStack {
                        Text(displayName(option))
                        if selection.wrappedValue.contains(option) {
                            Spacer()
                            Image(systemName: "checkmark")
                        }
                    }
                }
            }
            if !selection.wrappedValue.isEmpty {
                Divider()
                Button("Clear") { selection.wrappedValue.removeAll() }
            }
        } label: {
            Label(
                selection.wrappedValue.isEmpty ? title : "\(title) (\(selection.wrappedValue.count))",
                systemImage: "line.3.horizontal.decrease.circle"
            )
            .font(.caption)
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
    }

    // MARK: - Board (one column per site)

    private var board: some View {
        ScrollView(.horizontal) {
            HStack(alignment: .top, spacing: 12) {
                if appliedFilter == .appliedOnly {
                    appliedCollection
                } else if visibleSites.isEmpty {
                    emptyBoardMessage
                } else {
                    ForEach(visibleSites, id: \.self) { site in
                        SiteColumn(
                            site: site,
                            postings: postings(for: site),
                            onHideSite: { credentials.toggleMuted(site: site) },
                            onSelect: { detailPostingID = $0.id },
                            onToggleBookmark: { toggle($0, .bookmarked) },
                            onToggleApplied: { toggle($0, .applied) },
                            onToggleHidden: { toggle($0, .hidden) }
                        )
                    }
                }
            }
            .padding(16)
        }
    }

    /// A single vertical feed across every company, rather than one column
    /// per source. It intentionally reuses JobCardView so opening, saved,
    /// applied, hidden, read and deadline states behave exactly as in Board.
    private var mixedList: some View {
        ScrollView {
            LazyVStack(spacing: 8) {
                if mixedPostings.isEmpty {
                    Text(emptyStateMessage)
                        .multilineTextAlignment(.center)
                        .foregroundStyle(.secondary)
                        .padding(.top, 40)
                } else {
                    ForEach(mixedPostings) { posting in
                        JobCardView(
                            posting: posting,
                            showsSite: true,
                            onSelect: { detailPostingID = posting.id },
                            onToggleBookmark: { toggle(posting, .bookmarked) },
                            onToggleApplied: { toggle(posting, .applied) },
                            onToggleHidden: { toggle(posting, .hidden) }
                        )
                    }
                }
            }
            .frame(maxWidth: 920, alignment: .leading)
            .padding(16)
            .frame(maxWidth: .infinity, alignment: .center)
        }
    }

    /// Every applied-to posting in one place, across sites and regardless
    /// of per-site mutes — this view is about tracking applications, not
    /// browsing a company's openings.
    @ViewBuilder
    private var appliedCollection: some View {
        let applied = filteredPostings
        if applied.isEmpty {
            Text("아직 지원한 공고가 없어요 — 카드의 ✓ 버튼으로 지원 표시를 할 수 있어요")
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .padding(.horizontal, 24)
                .padding(.top, 40)
                .frame(maxWidth: .infinity)
        } else {
            SiteColumn(
                site: "지원한 공고",
                postings: applied,
                showsSite: true,
                onHideSite: nil,
                onSelect: { detailPostingID = $0.id },
                onToggleBookmark: { toggle($0, .bookmarked) },
                onToggleApplied: { toggle($0, .applied) },
                onToggleHidden: { toggle($0, .hidden) }
            )
        }
    }

    private var emptyBoardMessage: some View {
        Text(emptyStateMessage)
            .multilineTextAlignment(.center)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 24)
            .padding(.top, 40)
            .frame(maxWidth: .infinity)
    }

    private var emptyStateMessage: String {
        if postings.isEmpty {
            return isLoading ? "Loading…" : "No postings yet"
        }
        if availableSites.allSatisfy({ credentials.mutedSites.contains($0) }) {
            return "Every site is hidden — use the eye icon above to bring one back"
        }
        return "Nothing matches the current filters"
    }

    // MARK: - Settings prompt / error

    private var settingsPrompt: some View {
        VStack(spacing: 12) {
            Spacer()
            Text("Connect a data source (Notion or Google Sheets) to see postings.")
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .padding(.horizontal, 24)
            Button("Open Settings") { showSettings = true }
            Spacer()
        }
    }

    private func errorView(_ message: String) -> some View {
        VStack(spacing: 12) {
            Spacer()
            Image(systemName: "exclamationmark.triangle")
                .font(.title2)
            Text(message)
                .font(.caption)
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .padding(.horizontal, 24)
            Button("Retry") { Task { await refresh() } }
            Spacer()
        }
    }

    // MARK: - Data loading

    private func refreshLoop() async {
        while !Task.isCancelled {
            await refresh()
            try? await Task.sleep(for: .seconds(refreshInterval))
        }
    }

    private func refresh() async {
        guard credentials.isConfigured else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            let store = try credentials.makeStore()
            postings = try await store.fetchPostings()
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func loadDescriptionIfNeeded(_ posting: JobPosting) async {
        if let cached = descriptionCache[posting.id] {
            descriptionCache[posting.id] = normalizedDescription(cached)
            return
        }
        if let existing = posting.description {
            descriptionCache[posting.id] = normalizedDescription(existing)
            return
        }
        loadingDescriptionIDs.insert(posting.id)
        defer { loadingDescriptionIDs.remove(posting.id) }
        do {
            let store = try credentials.makeStore()
            descriptionCache[posting.id] = normalizedDescription(try await store.fetchDescription(id: posting.id))
        } catch {
            descriptionCache[posting.id] = ""
        }
    }

    /// Some CMS pages duplicate a paragraph in nested layout elements. The
    /// crawler now removes those duplicates at the source, and this cleanup
    /// also makes descriptions saved before that fix display correctly.
    private func normalizedDescription(_ source: String) -> String {
        var lines: [String] = []
        for rawLine in source.components(separatedBy: .newlines) {
            let line = rawLine.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !line.isEmpty, lines.last != line else { continue }
            lines.append(line.replacingOccurrences(of: "\\.", with: "."))
        }
        return lines.joined(separator: "\n")
    }

    // MARK: - Actions

    /// Opens the posting's external URL. Seen is already set by the time
    /// this is reachable (the detail sheet sets it on appear), but this
    /// also marks it in case some future entry point skips the sheet.
    private func openPosting(_ posting: JobPosting) {
        if let url = posting.url { NSWorkspace.shared.open(url) }
        markSeen(posting)
    }

    /// Fires once, the first time a posting is viewed. Never un-marks —
    /// viewing it again shouldn't reset "seen".
    private func markSeen(_ posting: JobPosting) {
        guard !posting.seen else { return }
        setChecked(posting, .seen, true)
    }

    private func toggle(_ posting: JobPosting, _ property: WritableProperty) {
        let newValue: Bool
        switch property {
        case .bookmarked: newValue = !posting.bookmarked
        case .hidden: newValue = !posting.hidden
        case .seen: newValue = !posting.seen
        case .applied: newValue = !posting.applied
        }
        setChecked(posting, property, newValue)
    }

    /// Optimistically updates the local copy so the UI reacts instantly,
    /// then writes to the active store; reverts and surfaces an error if
    /// that fails (e.g. a Notion integration lacking "Update content", or
    /// a Sheets service account not shared on the spreadsheet).
    private func setChecked(_ posting: JobPosting, _ property: WritableProperty, _ value: Bool) {
        guard let index = postings.firstIndex(where: { $0.id == posting.id }) else { return }

        let previous = postings[index]
        apply(value, of: property, to: &postings[index])

        Task {
            do {
                let store = try credentials.makeStore()
                try await store.setCheckbox(id: posting.id, property: property, value: value)
            } catch {
                if let current = postings.firstIndex(where: { $0.id == posting.id }) {
                    postings[current] = previous
                }
                errorMessage = "Couldn't save change: \(error.localizedDescription)"
            }
        }
    }

    private func apply(_ value: Bool, of property: WritableProperty, to posting: inout JobPosting) {
        switch property {
        case .seen: posting.seen = value
        case .bookmarked: posting.bookmarked = value
        case .hidden: posting.hidden = value
        case .applied: posting.applied = value
        }
    }
}

// MARK: - Board column

private struct SiteColumn: View {
    let site: String
    let postings: [JobPosting]
    /// Shows each card's source site — only useful in a column that mixes
    /// sites, i.e. the applied collection.
    var showsSite = false
    /// nil for a column that can't be muted (the applied collection).
    let onHideSite: (() -> Void)?
    let onSelect: (JobPosting) -> Void
    let onToggleBookmark: (JobPosting) -> Void
    let onToggleApplied: (JobPosting) -> Void
    let onToggleHidden: (JobPosting) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(site)
                    .font(.subheadline.weight(.semibold))
                Text("\(postings.count)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                if let onHideSite {
                    Button(action: onHideSite) {
                        Image(systemName: "eye.slash")
                    }
                    .buttonStyle(.plain)
                    .help("Hide \(site) column")
                }
            }
            .padding(.horizontal, 4)

            ScrollView {
                LazyVStack(spacing: 8) {
                    if postings.isEmpty {
                        Text("Nothing here")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .padding(.top, 20)
                    }
                    ForEach(postings) { posting in
                        JobCardView(
                            posting: posting,
                            showsSite: showsSite,
                            onSelect: { onSelect(posting) },
                            onToggleBookmark: { onToggleBookmark(posting) },
                            onToggleApplied: { onToggleApplied(posting) },
                            onToggleHidden: { onToggleHidden(posting) }
                        )
                    }
                }
                .padding(.bottom, 8)
            }
        }
        .frame(width: showsSite ? 420 : 300)
    }
}

/// The one accent used for everything "applied" — the card's border and
/// badge, the filter control, and the detail view's button — so it reads
/// as a single consistent state at a glance.
private let appliedGreen = Color(red: 0.18, green: 0.72, blue: 0.42)

// MARK: - Card

struct JobCardView: View {
    let posting: JobPosting
    var showsSite = false
    let onSelect: () -> Void
    let onToggleBookmark: () -> Void
    let onToggleApplied: () -> Void
    let onToggleHidden: () -> Void

    /// Seen-but-not-applied cards fade back; applied ones don't, since
    /// they're the ones worth tracking rather than ones already dismissed.
    private var isMuted: Bool { posting.seen && !posting.applied }

    var body: some View {
        Button(action: onSelect) {
            VStack(alignment: .leading, spacing: 6) {
                if posting.applied {
                    Label("지원 완료", systemImage: "checkmark.seal.fill")
                        .font(.caption2.weight(.bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 7)
                        .padding(.vertical, 2)
                        .background(appliedGreen, in: Capsule())
                }
                HStack(alignment: .top) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(posting.title)
                            // A viewed posting should stay readable, but no
                            // longer compete visually with new opportunities.
                            .font(.subheadline.weight(isMuted ? .regular : .semibold))
                            .lineLimit(2)
                            .foregroundStyle(isMuted ? Color.gray.opacity(0.65) : Color.primary)
                        Text(showsSite ? "\(posting.company) · \(posting.site)" : posting.company)
                            .font(.caption)
                            .foregroundStyle(isMuted ? Color.gray.opacity(0.55) : Color.secondary)
                    }
                    Spacer()
                    VStack(spacing: 6) {
                        Button(action: onToggleBookmark) {
                            Image(systemName: posting.bookmarked ? "star.fill" : "star")
                                .foregroundStyle(posting.bookmarked ? Color.yellow : Color.secondary)
                        }
                        .buttonStyle(.plain)
                        Button(action: onToggleApplied) {
                            Image(systemName: posting.applied ? "checkmark.circle.fill" : "checkmark.circle")
                                .foregroundStyle(posting.applied ? appliedGreen : Color.secondary)
                        }
                        .buttonStyle(.plain)
                        .help(posting.applied ? "지원 취소" : "지원함으로 표시")
                        Button(action: onToggleHidden) {
                            Image(systemName: "xmark.circle")
                                .foregroundStyle(.secondary)
                        }
                        .buttonStyle(.plain)
                        .help("Hide")
                    }
                }

                if !posting.employmentType.isEmpty || !posting.careerLevel.isEmpty || posting.minYearsExperience != nil || !posting.minimumDegree.isEmpty {
                    HStack(spacing: 4) {
                        if !posting.employmentType.isEmpty { badge(posting.employmentType) }
                        if !posting.careerLevel.isEmpty { badge(posting.careerLevel) }
                        if let years = posting.minYearsExperience { badge("경력 \(years)년+") }
                        if !posting.minimumDegree.isEmpty { badge(degreeLabel(posting.minimumDegree)) }
                    }
                }

                HStack {
                    if posting.expired {
                        Text("마감")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(.red)
                    }
                    if !posting.location.isEmpty {
                        Label(posting.location, systemImage: "mappin.and.ellipse")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    if let label = posting.dDayLabel, let days = posting.daysUntilDeadline {
                        Text(label)
                            .font(.caption2.weight(.bold))
                            .foregroundStyle(dDayColor(days))
                    } else if let firstSeen = posting.firstSeen {
                        Text(firstSeen, style: .relative)
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 10))
            .background(
                posting.applied ? appliedGreen.opacity(0.12) : Color.clear,
                in: RoundedRectangle(cornerRadius: 10)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 10)
                    .strokeBorder(posting.applied ? appliedGreen : Color.clear, lineWidth: 1.5)
            )
            .opacity(posting.expired ? 0.6 : 1)
        }
        .buttonStyle(.plain)
    }

    private func badge(_ text: String) -> some View {
        Text(text)
            .font(.caption2.weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(Color.secondary.opacity(0.15), in: Capsule())
    }
}

/// D-3 and closer reads as urgent (orange), D-DAY/past deadline as red,
/// anything further out as a normal secondary label.
private func dDayColor(_ daysUntilDeadline: Int) -> Color {
    if daysUntilDeadline <= 0 { return .red }
    if daysUntilDeadline <= 3 { return .orange }
    return .secondary
}

private func degreeLabel(_ degree: String) -> String {
    switch degree {
    case "High School": return "고졸"
    case "Associate": return "전문학사"
    case "Bachelor": return "학사"
    case "Master": return "석사"
    case "Doctorate": return "박사"
    default: return degree
    }
}

private func degreeSortRank(_ degree: String) -> Int {
    switch degree {
    case "High School": return 0
    case "Associate": return 1
    case "Bachelor": return 2
    case "Master": return 3
    case "Doctorate": return 4
    default: return 5
    }
}

// MARK: - Detail view

private struct DetailView: View {
    let posting: JobPosting
    let description: String?
    let isLoadingDescription: Bool
    let onOpen: () -> Void
    let onToggleBookmark: () -> Void
    let onToggleApplied: () -> Void
    let onToggleHidden: () -> Void
    let onClose: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(posting.title)
                        .font(.title3.weight(.semibold))
                    Text("\(posting.company) · \(posting.site)")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button("Close", action: onClose)
                    .buttonStyle(.plain)
            }

            HStack(spacing: 8) {
                if let label = posting.dDayLabel, let days = posting.daysUntilDeadline {
                    Text(label)
                        .font(.caption.weight(.bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(dDayColor(days), in: Capsule())
                }
                if !posting.employmentType.isEmpty { badge(posting.employmentType) }
                if !posting.careerLevel.isEmpty { badge(posting.careerLevel) }
                if let years = posting.minYearsExperience { badge("경력 \(years)년+") }
                if !posting.minimumDegree.isEmpty { badge(degreeLabel(posting.minimumDegree)) }
                if !posting.location.isEmpty {
                    Label(posting.location, systemImage: "mappin.and.ellipse")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if posting.expired {
                    Text("마감")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.red)
                }
            }

            if let start = posting.applicationStart, let deadline = posting.applicationDeadline {
                Text("\(start.formatted(date: .abbreviated, time: .omitted)) – \(deadline.formatted(date: .abbreviated, time: .omitted))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Divider()

            ScrollView {
                Group {
                    if isLoadingDescription {
                        ProgressView()
                            .padding(.top, 40)
                    } else if let description, !description.isEmpty {
                        Text(description)
                            .font(.callout)
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    } else {
                        Text("No description available.")
                            .foregroundStyle(.secondary)
                            .padding(.top, 40)
                    }
                }
                .frame(maxWidth: .infinity)
            }

            HStack {
                Button(action: onToggleBookmark) {
                    Label(posting.bookmarked ? "Bookmarked" : "Bookmark",
                          systemImage: posting.bookmarked ? "star.fill" : "star")
                }
                Button(action: onToggleApplied) {
                    Label(posting.applied ? "지원 완료" : "지원함으로 표시",
                          systemImage: posting.applied ? "checkmark.seal.fill" : "checkmark.seal")
                        .foregroundStyle(posting.applied ? appliedGreen : Color.primary)
                }
                Button(action: onToggleHidden) {
                    Label("Hide", systemImage: "eye.slash")
                }
                Spacer()
                Button(action: onOpen) {
                    Label("Open Posting", systemImage: "arrow.up.right.square")
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(20)
        // A job description is a reading surface, not a compact popover.
        // Keep the main content column comfortably wide while capping it so
        // the sheet still fits on smaller laptop screens.
        .frame(
            minWidth: 720,
            idealWidth: 840,
            maxWidth: 1_000,
            minHeight: 600,
            idealHeight: 720,
            maxHeight: 820
        )
    }

    private func badge(_ text: String) -> some View {
        Text(text)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(Color.secondary.opacity(0.15), in: Capsule())
    }
}
