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
    var applied: Bool
    /// Set by the user when an application was turned down — kept apart
    /// from `applied` so the history stays, but the 지원한 공고 view can
    /// separate live applications from closed ones.
    var rejected: Bool
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
    /// Explicit visa-support classification from the crawler. "Not stated"
    /// means the posting did not make a claim either way.
    let visaSponsorship: String

    /// Visa badge text, or nil when it says nothing useful: a role based in
    /// Korea needs no visa, so "Not stated" there is just noise.
    var visaBadgeValue: String? {
        if visaSponsorship.isEmpty { return nil }
        if visaSponsorship == "Not stated" && isKoreaBased { return nil }
        return visaSponsorship
    }

    /// Korea-based when the location says so, or — since many Korean boards
    /// leave location blank — when the company or location is written in
    /// Hangul.
    var isKoreaBased: Bool {
        let place = location.lowercased()
        if ["korea", "seoul", "pangyo", "bundang", "seongnam"].contains(where: place.contains) {
            return true
        }
        return (company + location).unicodeScalars.contains { (0xAC00...0xD7A3).contains($0.value) }
    }

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

/// How postings the user has applied to are treated by the board.
enum AppliedFilter: String, CaseIterable, Identifiable {
    case all          // applied postings stay mixed in with everything else
    case notApplied   // hide what's already been applied to
    case appliedOnly  // a single cross-site "지원한 공고" collection

    var id: String { rawValue }

    var label: String {
        switch self {
        case .all: return "지원 포함"
        case .notApplied: return "미지원만"
        case .appliedOnly: return "지원한 공고"
        }
    }

    var systemImage: String {
        switch self {
        case .all: return "tray.full"
        case .notApplied: return "tray"
        case .appliedOnly: return "checkmark.seal"
        }
    }
}

/// Which applications the 지원한 공고 view shows.
enum AppliedStatus: String, CaseIterable, Identifiable {
    case active    // still waiting on a result
    case rejected  // turned down
    case all

    var id: String { rawValue }

    var label: String {
        switch self {
        case .active: return "진행 중"
        case .rejected: return "불합격"
        case .all: return "전체"
        }
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

// MARK: - Country

extension JobPosting {
    /// Countries this posting is located in, derived from the free-text
    /// location each source reports ("Seoul, Korea – Republic of Korea",
    /// "서울특별시 강남구", "Hong Kong / Singapore", "Remote - APAC"). A
    /// posting listing several offices belongs to each of them. Sources
    /// that report no location at all are Korean-only boards, so an empty
    /// location from one of those counts as 한국.
    var countries: [String] {
        let text = location.lowercased()
        if text.trimmingCharacters(in: .whitespaces).isEmpty {
            return Self.koreanOnlySites.contains(site) ? ["한국"] : ["기타"]
        }
        var found: [String] = []
        for (country, keywords) in Self.countryKeywords where keywords.contains(where: { text.contains($0) }) {
            found.append(country)
        }
        // "Taipei, Greater China" / "Republic of China (Taiwan)" mention
        // China but mean Taiwan; keep 중국 only for a mainland city.
        if found.contains("대만"), !["shanghai", "beijing", "shenzhen"].contains(where: { text.contains($0) }) {
            found.removeAll { $0 == "중국" }
        }
        if found.isEmpty {
            found = Self.remoteKeywords.contains(where: { text.contains($0) }) ? ["원격·글로벌"] : ["기타"]
        }
        return found
    }

    /// Ordered so the filter menu lists the likely-relevant countries first.
    static let countryOrder = ["한국", "일본", "싱가포르", "홍콩", "중국", "대만", "미국", "인도", "원격·글로벌", "기타"]

    private static let countryKeywords: [(String, [String])] = [
        ("한국", ["korea", "seoul", "서울", "경기", "성남", "판교", "분당", "강남", "수원", "동탄", "화성",
                 "pangyo", "bundang", "gyeonggi", "hwaseong", "dongtan", "busan", "부산", "인천", "대전",
                 "세종", "대한민국", "한국"]),
        ("일본", ["japan", "tokyo", "osaka", "東京", "日本", "일본", "도쿄"]),
        ("싱가포르", ["singapore", "싱가포르"]),
        ("홍콩", ["hong kong", "kowloon", "홍콩"]),
        ("중국", ["china", "shanghai", "beijing", "shenzhen", "중국"]),
        ("대만", ["taiwan", "taipei", "대만"]),
        ("미국", ["united states", "usa", "u.s.", " us", "us-", "new york", "san francisco", "seattle",
                 "california", "palo alto", "mountain view", "austin", "boston", "chicago"]),
        ("인도", ["india", "bengaluru", "bangalore", "mumbai", "hyderabad", "gurugram", "pune"]),
    ]

    private static let remoteKeywords = ["remote", "anywhere", "worldwide", "global", "apac", "apj", "asia"]

    /// Sites (adapter names) that only ever list Korean postings and don't
    /// always fill in a location.
    private static let koreanOnlySites: Set<String> = [
        "kakao", "kakaobank", "kakaomobility", "kakaopay", "kakaopaysec", "kakaohealthcare", "kakaogames",
        "kream", "line", "daangn", "woowahan", "coupang", "naver", "navercloud", "nhn", "tosscareers",
        "krafton", "kurly", "musinsa", "noluniverse", "bucketplace", "yanolja", "bithumb", "zigbang",
        "myrealtrip", "banksalad", "ridi", "upstage", "netmarble", "ncsoft", "hybe", "wrtn", "wanted",
        "yonsei", "dunamu", "zoyi", "furiosaai",
    ]
}

// MARK: - Work location

extension JobPosting {
    /// Tags for the 근무지 filter: every country this posting is in, any
    /// city it names, and 원격 for remote roles — so picking "한국" keeps
    /// all of Korea while picking "서울" narrows to Seoul alone. A posting
    /// with no city keeps only its country (and a Korean board's blank
    /// location falls back to the company's known office, if any).
    var locationTags: Set<String> {
        var tags = Set(countries)
        let text = location.lowercased()
        // A region-only location ("APAC") isn't remote work by itself.
        if tags.remove("원격·글로벌") != nil { tags.insert("기타") }
        for (city, keywords) in Self.cityKeywords where keywords.contains(where: { text.contains($0) }) {
            tags.insert(city)
        }
        if Self.remoteWorkKeywords.contains(where: { text.contains($0) }) {
            tags.insert("원격")
            tags.remove("기타")
        }
        if text.trimmingCharacters(in: .whitespaces).isEmpty, let city = Self.defaultKoreanCity[site] {
            tags.insert(city)
        }
        return tags
    }

    /// The 근무지 menu: each country with the cities worth telling apart
    /// inside it. Countries without a city list are offered as a whole.
    static let locationGroups: [(country: String, cities: [String])] = [
        ("한국", ["서울", "판교·성남", "경기 기타", "지방"]),
        ("일본", ["도쿄", "오사카"]),
        ("싱가포르", []),
        ("홍콩", []),
        ("중국", []),
        ("대만", []),
        ("미국", []),
        ("인도", []),
        ("원격", []),
        ("기타", []),
    ]

    private static let remoteWorkKeywords = ["remote", "anywhere", "worldwide", "home based", "home-based",
                                             "distributed", "원격", "재택"]

    private static let cityKeywords: [(String, [String])] = [
        ("서울", ["seoul", "서울", "강남", "송파", "역삼", "성수", "여의도", "구로"]),
        ("판교·성남", ["pangyo", "bundang", "seongnam", "판교", "분당", "성남"]),
        ("경기 기타", ["suwon", "hwaseong", "dongtan", "yongin", "수원", "화성", "동탄", "용인", "기흥", "평택", "이천"]),
        ("지방", ["busan", "daejeon", "daegu", "gwangju", "ulsan", "sejong", "부산", "대전", "대구", "광주", "울산", "세종", "구미", "창원", "제주"]),
        ("도쿄", ["tokyo", "東京", "도쿄"]),
        ("오사카", ["osaka", "大阪", "오사카"]),
    ]

    /// Where a Korean board's postings are when it leaves location blank
    /// — only companies with one main office.
    private static let defaultKoreanCity: [String: String] = [
        "kakao": "판교·성남", "kakaobank": "판교·성남", "kakaopay": "판교·성남",
        "kakaomobility": "판교·성남", "kakaogames": "판교·성남", "naver": "판교·성남",
        "navercloud": "판교·성남", "nhn": "판교·성남", "line": "판교·성남", "ncsoft": "판교·성남",
        "tosscareers": "서울", "coupang": "서울", "woowahan": "서울", "daangn": "서울",
        "kurly": "서울", "musinsa": "서울", "krafton": "서울", "bucketplace": "서울",
        "bithumb": "서울", "zigbang": "서울", "myrealtrip": "서울", "banksalad": "서울",
        "ridi": "서울", "hybe": "서울", "wrtn": "서울", "dunamu": "서울", "zoyi": "서울",
        "furiosaai": "서울", "netmarble": "서울",
    ]
}

// MARK: - Industry

extension JobPosting {
    /// The company's industry, from a fixed per-site table. Aggregator
    /// boards (wanted, yonsei) list many companies, so they fall in 기타.
    var industry: String {
        Self.industryBySite[site] ?? "기타"
    }

    static let industryOrder = [
        "빅테크", "IT 플랫폼", "커머스", "핀테크·가상자산", "AI", "클라우드·B2B SW",
        "반도체·하드웨어", "게임", "모빌리티·여행", "콘텐츠·엔터", "광고·마케팅테크",
        "헬스케어", "프롭테크", "컨설팅", "투자은행", "자산운용", "퀀트·헤지펀드", "기타",
    ]

    private static let industryBySite: [String: String] = {
        let groups: [String: [String]] = [
            "빅테크": ["google", "microsoft", "apple", "amazon", "meta", "netflix", "nvidia"],
            "IT 플랫폼": ["naver", "kakao", "line", "daangn", "woowahan", "nhn", "reddit", "zoyi",
                        "matchgroup", "hyperconnect"],
            "커머스": ["coupang", "kurly", "musinsa", "kream", "bucketplace", "shopify"],
            "핀테크·가상자산": ["tosscareers", "kakaopay", "kakaopaysec", "kakaobank", "banksalad",
                          "dunamu", "bithumb", "coinbase", "stripe", "binance"],
            "AI": ["upstage", "anthropic", "openai", "cohere", "wrtn", "cursor"],
            "클라우드·B2B SW": ["cloudflare", "datadog", "databricks", "snowflake", "elastic", "mongodb",
                             "gitlab", "grafanalabs", "canonical", "vercel", "supabase", "notion",
                             "posthog", "palantir", "sap", "sendbird", "twilio", "dropbox",
                             "navercloud", "1password", "automattic"],
            "반도체·하드웨어": ["qualcomm", "furiosaai", "amd"],
            "게임": ["krafton", "netmarble", "ncsoft", "kakaogames", "nexon"],
            "모빌리티·여행": ["kakaomobility", "yanolja", "noluniverse", "myrealtrip", "airbnb"],
            "콘텐츠·엔터": ["hybe", "ridi"],
            "광고·마케팅테크": ["moloco"],
            "헬스케어": ["kakaohealthcare"],
            "프롭테크": ["zigbang"],
            "컨설팅": ["mckinsey", "bcg", "kearney", "bain"],
            "투자은행": ["jpmorgan", "goldman", "morganstanley", "citi"],
            "자산운용": ["blackrock"],
            "퀀트·헤지펀드": ["worldquant"],
        ]
        var table: [String: String] = [:]
        for (industry, sites) in groups {
            for site in sites { table[site] = industry }
        }
        return table
    }()
}

// MARK: - Role

extension JobPosting {
    /// Job functions read off the title — a posting can be several
    /// ("Backend/Data Engineer"). A dev title naming no specialty
    /// ("Software Engineer") is SW 일반; anything else matching none is 기타.
    var roles: Set<String> {
        let padded = " " + Self.normalizedTitle(title) + " "
        var found = Set<String>()
        for (role, keywords) in Self.roleKeywords {
            let hit = keywords.contains { keyword in
                // Hangul words take particles and compounds ("백엔드개발자"),
                // so they match as substrings; Latin ones as whole words.
                keyword.unicodeScalars.contains { (0xAC00...0xD7A3).contains($0.value) }
                    ? padded.contains(keyword)
                    : padded.contains(" " + keyword + " ")
            }
            if hit { found.insert(role) }
        }
        if !found.isEmpty { return found }
        let generic = Self.genericDevKeywords.contains { keyword in
            keyword.unicodeScalars.contains { (0xAC00...0xD7A3).contains($0.value) }
                ? padded.contains(keyword)
                : padded.contains(" " + keyword + " ")
        }
        return [generic ? "SW 일반" : "기타"]
    }

    private static let genericDevKeywords = [
        "software", "developer", "engineer", "engineering", "programmer", "sw", "swe", "개발", "엔지니어",
    ]

    static let roleOrder = [
        "백엔드", "프론트엔드", "풀스택", "모바일", "ML·AI", "데이터", "인프라·DevOps",
        "보안", "임베디드·HW", "QA", "솔루션·기술영업", "SW 일반", "기타",
    ]

    /// Lowercased, with punctuation turned into single spaces so "Back-end",
    /// "AI/ML" and "(iOS)" split into plain words.
    private static func normalizedTitle(_ title: String) -> String {
        let mapped = title.lowercased().unicodeScalars.map { scalar -> Character in
            CharacterSet.alphanumerics.contains(scalar) ? Character(scalar) : " "
        }
        return String(mapped).split(separator: " ").joined(separator: " ")
    }

    private static let roleKeywords: [(String, [String])] = [
        ("백엔드", ["backend", "back end", "server", "서버", "백엔드", "java", "spring", "golang", "node js",
                  "api", "kotlin", "go", "rust", "distributed systems"]),
        ("프론트엔드", ["frontend", "front end", "프론트", "react", "web developer", "web engineer", "웹 개발",
                    "웹개발", "ui engineer", "javascript", "typescript"]),
        ("풀스택", ["fullstack", "full stack", "풀스택"]),
        ("모바일", ["ios", "android", "mobile", "flutter", "react native", "모바일", "안드로이드"]),
        ("ML·AI", ["ml", "machine learning", "ai", "deep learning", "llm", "nlp", "computer vision", "mlops",
                  "research scientist", "applied scientist", "research engineer", "genai", "머신러닝", "딥러닝",
                  "인공지능", "추천", "검색 모델"]),
        ("데이터", ["data", "analytics", "데이터", "big data", "etl"]),
        ("인프라·DevOps", ["devops", "sre", "site reliability", "infrastructure", "infra", "cloud engineer",
                         "platform engineer", "kubernetes", "network engineer", "systems engineer",
                         "system engineer", "dba", "database", "인프라", "클라우드", "데브옵스", "네트워크"]),
        ("보안", ["security", "red team", "threat", "vulnerability", "보안"]),
        ("임베디드·HW", ["embedded", "firmware", "hardware", "fpga", "asic", "soc", "rtl", "verification",
                       "chip", "silicon", "임베디드", "펌웨어", "하드웨어", "반도체"]),
        ("QA", ["qa", "quality assurance", "test engineer", "sdet", "테스트", "품질"]),
        ("솔루션·기술영업", ["solutions architect", "solution architect", "solutions engineer", "solution engineer",
                       "customer engineer", "sales engineer", "forward deployed", "professional services",
                       "technical account", "presales", "pre sales", "기술지원", "기술 영업", "기술영업"]),
    ]
}

// MARK: - Condition filters

/// Upper bound on the experience a posting may ask for. Postings that
/// state nothing pass every option — recall matters more than precision.
enum ExperienceFilter: String, CaseIterable, Identifiable {
    case all, newcomer, upTo2, upTo3, upTo5

    var id: String { rawValue }

    var label: String {
        switch self {
        case .all: return "연차 전체"
        case .newcomer: return "신입 가능"
        case .upTo2: return "2년 이하"
        case .upTo3: return "3년 이하"
        case .upTo5: return "5년 이하"
        }
    }

    private var maxYears: Int? {
        switch self {
        case .all: return nil
        case .newcomer: return 0
        case .upTo2: return 2
        case .upTo3: return 3
        case .upTo5: return 5
        }
    }

    private static let seniorTitleWords: Set<String> = [
        "senior", "sr", "staff", "principal", "lead", "director", "head", "manager", "vp",
    ]
    private static let seniorKoreanHints = ["시니어", "리드", "팀장", "책임"]

    private static func hasSeniorTitle(_ title: String) -> Bool {
        let words = Set(title.lowercased().components(separatedBy: CharacterSet.alphanumerics.inverted))
        return !words.isDisjoint(with: seniorTitleWords) || seniorKoreanHints.contains(where: title.contains)
    }

    func matches(_ posting: JobPosting) -> Bool {
        guard let max = maxYears else { return true }
        if let years = posting.minYearsExperience { return years <= max }
        // No stated years: a senior-titled role is still not junior-friendly.
        if max <= 3 && Self.hasSeniorTitle(posting.title) { return false }
        if max == 0 && posting.careerLevel == "경력" { return false }
        return true
    }
}

enum DeadlineFilter: String, CaseIterable, Identifiable {
    case all, within3, within7, hasDeadline

    var id: String { rawValue }

    var label: String {
        switch self {
        case .all: return "마감 전체"
        case .within3: return "3일 이내 마감"
        case .within7: return "7일 이내 마감"
        case .hasDeadline: return "마감일 있는 공고만"
        }
    }

    func matches(_ posting: JobPosting) -> Bool {
        switch self {
        case .all: return true
        case .hasDeadline: return posting.applicationDeadline != nil
        case .within3: return (0...3).contains(posting.daysUntilDeadline ?? -1)
        case .within7: return (0...7).contains(posting.daysUntilDeadline ?? -1)
        }
    }
}

/// How recently the crawler first saw the posting.
enum RecencyFilter: String, CaseIterable, Identifiable {
    case all, day1, day3, day7

    var id: String { rawValue }

    var label: String {
        switch self {
        case .all: return "등록 전체"
        case .day1: return "24시간 내 새 공고"
        case .day3: return "3일 내 새 공고"
        case .day7: return "7일 내 새 공고"
        }
    }

    func matches(_ posting: JobPosting) -> Bool {
        let days: Double
        switch self {
        case .all: return true
        case .day1: days = 1
        case .day3: days = 3
        case .day7: days = 7
        }
        guard let firstSeen = posting.firstSeen else { return false }
        return firstSeen >= Date().addingTimeInterval(-days * 86_400)
    }
}
