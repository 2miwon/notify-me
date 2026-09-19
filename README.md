# notify-me

Crawls Events, stores new ones in a pluggable datastore (Notion or
Google Sheets), and shows them in a small translucent macOS widget window
where you can bookmark, hide, or just see what's new.

```
notify-me/
├── cmd/crawler/            # entry point: fetch -> filter -> dedupe -> write to the store
├── internal/
│   ├── job/                # shared Posting type
│   ├── site/                # Adapter interface + one package per site
│   │   ├── naver/           # recruit.navercorp.com adapter (currently active)
│   │   ├── navercloud/      # recruit.navercloudcorp.com adapter (currently active)
│   │   ├── nhn/              # careers.nhn.com Tech adapter (currently active)
│   │   ├── openai/           # OpenAI Ashby API adapter (technical departments)
│   │   ├── google/            # Google Careers South Korea Early/Intern adapter
│   │   ├── yonsei/           # authenticated Career Yonsei 추천채용 adapter
│   │   ├── line/            # careers.linecorp.com adapter (currently active)
│   │   ├── kakaobank/       # recruit.kakaobank.com adapter (currently active)
│   │   ├── kakao/            # careers.kakao.com adapter (currently active)
│   │   ├── woowahan/         # career.woowahan.com adapter (currently active)
│   │   ├── coupang/          # Coupang Greenhouse public-board adapter (currently active)
│   │   ├── daangn/           # careers.daangn.com adapter (currently active)
│   │   ├── dunamu/           # dunamu.com Engineering adapter (currently active)
│   ├── config/              # loads config/keywords.yaml
│   ├── store/                # Store interface — the pluggable datastore contract
│   ├── notion/              # Store implementation: Notion API
│   └── sheets/              # Store implementation: Google Sheets API
├── config/keywords.yaml     # edit this to change filter keywords / site settings
├── .github/workflows/       # build.yml (compile) + crawl.yml (run on schedule)
└── mac/                     # SwiftUI widget-style app (Swift Package) — reads
                              # the feed and writes back Seen/Bookmarked/Hidden,
                              # against whichever backend is configured in Settings
```

## Pluggable datastore

`internal/store.Store` is the contract both the crawler and (via its own
Swift `JobStore` protocol) the macOS app code against — four methods:
list existing postings, create one, delete one expired posting, get the backend's
name. `internal/notion` and `internal/sheets` are the two implementations
today; adding a third backend means writing one new package against that
interface, nothing else changes. Pick the active one with the
`STORE_BACKEND` env var (`notion`, the default, or `sheets`) on the
crawler; the macOS app has the same choice as a Settings picker.

## Posting states

Postings retain the following fields in both backends. `Expired` is kept only
for backward compatibility with records created by earlier crawler versions:
(Notion checkboxes, or spreadsheet columns of the same name):

| State | Set by | Meaning |
|---|---|---|
| Seen | mac app, when a card is opened | dims the title, never auto-resets |
| Bookmarked | mac app, star button | shown in the "bookmarked only" filter |
| Hidden | mac app, x button | removed from the feed permanently |
| Expired | legacy crawler field | existing records may still carry this old value |

The crawler only creates new records or removes postings no longer in a
source's live listing; it never touches Seen/Bookmarked/Hidden. The app only
updates Seen/Bookmarked/Hidden and never creates records. Because dedup skips
any URL already stored, re-crawling never duplicates a live posting.

**How "expired" is detected:** each run, the crawler re-fetches a site's
entire live listing and diffs it against what's already stored for that
site. Anything missing from the fresh fetch gets removed (archived to Notion's
trash when using Notion). This is only accurate if the fetch actually covers
the whole listing — see the
`max_pages` note in `config/keywords.yaml`.

## Cross-site normalization

Two fields are deliberately *not* just "whatever the site said" — the
point of both is that one `config/keywords.yaml` filter value works no
matter which site produced the posting:

- **EmploymentType** is normalized to one vocabulary (`Full-time`,
  `Contract`, `Internship`, `Temporary`) that every adapter maps its own
  site's native labels onto — see `employmentTypeNames` in
  `internal/site/naver/naver.go` and `canonicalEmploymentType` in
  `internal/site/line/line.go`. Without this, filtering to full-time
  roles meant listing every site's own spelling of it
  (`employment_types: ["정규", "Full-time"]`); now it's just
  `employment_types: ["Full-time"]`.
- **MinYearsExperience** (`internal/job/experience.go`) is extracted from
  a posting's title+description by regex, looking for phrasings like
  "10년 이상" or "5+ years" — no site reports this as structured data, so
  every adapter that fetches a description runs the same extractor over
  it. This is a best-effort heuristic, not a real parse: postings are
  free text written inconsistently by many different people, so it will
  miss unusual phrasings and can occasionally misfire (e.g. a contract
  length phrased as "N년" could be mistaken for an experience
  requirement). Filter with `min_years_experience`/`max_years_experience`
  in `config/keywords.yaml`; a posting where nothing was extracted always
  passes regardless of these bounds.
- **MinimumDegree** (`internal/job/degree.go`) is the best-effort normalized
  minimum academic requirement extracted from the same title/body text:
  `High School`, `Associate`, `Bachelor`, `Master`, or `Doctorate`. Configure
  `minimum_degrees` with any of these (or Korean shorthand such as `학사`,
  `석사`, `박사`) to include only matching requirements. As with experience,
  a posting without a clear requirement is left in rather than guessed at.
  The extracted value is saved as `Minimum Degree` in Notion or the
  `Minimum Degree` column in Google Sheets and displayed in the macOS app.

## Sites

### naver (recruit.navercorp.com) — currently active

The listing page looks server-rendered but is actually populated by the
same JSON endpoint its own "load more" button calls:
`/rcrt/loadJobList.do?subJobCdArr=<codes>&firstIndex=<offset>` — no auth,
clean fields (title, company, real application dates, employment type,
career level), paginated 10/page with a `totalSize` telling you when to
stop. `internal/site/naver/naver.go` hits that directly.

To change which categories are crawled: open
https://recruit.navercorp.com/rcrt/list.do, tick the filters you want,
and read the resulting URL's `subJobCdArr` param back into
`sub_job_codes` in `config/keywords.yaml`.

The per-posting detail page (`rcrt/view.do?annoId=...`) *is* genuinely
static HTML with no JSON equivalent, so its full body text is scraped
with goquery — the one place this project actually uses goquery per the
original plan. Two non-obvious gotchas if you're touching this code:

- The CMS's WYSIWYG content nests a `<p>` directly inside
  `<p class="detail_text">`, and HTML5 parsing rules auto-close a `<p>`
  the moment another one starts — so a selector for `.detail_text` finds
  the elements but their `.Text()` comes back empty, because the real
  text ends up in anonymous sibling elements instead. Selecting the
  parent `.detail_box` (a `<div>`, immune to this) picks it up regardless
  of where the broken nesting put it.
- Nearly every posting closes with the same boilerplate (hiring-process
  steps, military-service eligibility, OFAC sanctions notice, "results
  are emailed to you") — not useful per-posting content, just clutter.
  Its heading text isn't consistent enough across postings to cut a
  whole trailing section by title match (tried it, missed real cases),
  so `boilerplateLineMarkers` in `naver.go` instead drops any line
  containing one of a curated set of telltale phrases, checked against
  each heading/paragraph/list item's *whole* text before `<br>` tags
  (some postings hard-wrap one boilerplate sentence across several of
  them) get split into display lines — otherwise a wrapped sentence can
  dodge the match by having its keyword split across two fragments. This
  is a best-effort keyword list, not a complete one; postings aren't
  authored consistently enough for it to ever be exhaustive.

### line (careers.linecorp.com) — currently active

The listing page is Gatsby (a React static-site generator), which bakes
each page's full GraphQL query result into a static JSON file at build
time: `/page-data/ko/jobs/page-data.json` returns *every* job in one shot
(381 as of writing) — the page's own `ci`/`co`/`fi` filter dropdowns are
applied purely client-side in the browser after that loads, not sent to
any server, so the adapter fetches with no query string and filters
in Go instead (`internal/site/line/line.go`). A job's own page-data.json
(`/page-data/ko/jobs/<id>/page-data.json`) conveniently includes the full
posting body as an HTML string directly in the JSON — no page to scrape
at all, goquery just strips its tags.

One gotcha: a rolling ("until filled") posting reports a sentinel
`end_date` of `2999-12-31` instead of omitting it — the adapter checks
the `until_filled` flag and leaves `ApplicationDeadline` nil rather than
surfacing a nonsense multi-century D-day countdown.

To change the filter: open https://careers.linecorp.com/ko/jobs, tick
the filters you want, and read the resulting URL's `fi`/`ci`/`co` params
back into `job_fields`/`cities`/`regions` in `config/keywords.yaml`.

### kakaobank (recruit.kakaobank.com) — currently active

KakaoBank's public list API returns each notice's actual reception start and
end timestamp. The adapter uses the end timestamp for `ApplicationDeadline`,
so the macOS app displays the real D-day; notices past that timestamp are
excluded even though the API retains historical notices. This also lets the
normal expiry sweep remove a closed notice from the store.

The default filters mirror the supplied jobs URL. Change
`recruit_class_names` or `recruit_employee_types` under `sites.kakaobank` in
`config/keywords.yaml` to match a different selection; leave either list empty
to not filter on it.

### kakao (careers.kakao.com) — currently active

The default configuration follows the Kakao corporation's Technology listing.
Its public API returns job descriptions, employment type, location, and its
open/closed status directly. `endDate` is used when present; notices with no
end date are treated as rolling postings. A source-closed notice is omitted
from the live set so the common expiry sweep removes it from the store.

Change `kakao_company`, `kakao_part`, `kakao_skill_set`, and
`kakao_employee_type` in `config/keywords.yaml` to follow another Kakao
careers filter.

### woowahan (career.woowahan.com) — currently active

The default filters mirror the supplied 우아한형제들/배민 recruitment URL.
The public API is paginated; it reports normal empty results when no matching
roles are open, which the crawler treats as a valid live listing. Closed
notices, and notices whose reported deadline has passed, are excluded so the
usual expiry sweep can remove stored postings.

Change `woowahan_job_group_codes` or `woowahan_job_codes` in
`config/keywords.yaml` to follow a different Baemin careers filter.

### wanted (wanted.co.kr) — adapter built, not enabled by default

The listing page is Next.js with jobs fetched client-side, but from a
public JSON API (`/api/chaos/navigation/v1/results`) — no goquery or
headless browser needed there either. See `internal/site/wanted/wanted.go`.
Re-enable it by uncommenting its block in `config/keywords.yaml`.

### Adding another site

1. Check whether the listing page's data is server-rendered HTML, or (like
   both sites above) fetched from a JSON API your browser's Network tab
   can find — check XHR/Fetch requests before assuming you need goquery
   or a headless browser.
2. Add `internal/site/<name>/<name>.go` implementing `site.Adapter`
   (`Name() string`, `Fetch() ([]job.Posting, error)`).
3. Register it in `buildAdapters` in `cmd/crawler/main.go`.
4. Add its settings under `sites:` in `config/keywords.yaml`.

## Notion setup

1. Go to https://www.notion.so/my-integrations, click **New integration**,
   give it a name (e.g. "notify-me"), select your workspace, and create it.
   Under **Capabilities**, make sure **Read content**, **Insert content**,
   and **Update content** are all checked — the crawler needs Insert (new
   postings) and Update (archiving removed postings), and the macOS app needs
   Update (Seen/Bookmarked/Hidden). Copy the **Internal Integration
   Secret** — this is `NOTION_TOKEN`, used by both the crawler and the app.
2. In Notion, create a new (empty) database (table) — any title, any
   default properties, doesn't matter.
3. Open the database's `...` menu -> **Connections** -> connect the
   integration you just created (it needs access or every API call 404s).
4. Copy the database ID from its URL:
   `https://www.notion.so/<workspace>/<DATABASE_ID>?v=...` — the 32-char
   hex string right after the workspace name (dashes optional). This is
   `NOTION_DATABASE_ID`.
5. Provision the schema — instead of adding properties by hand,
   run this once (needs `NOTION_TOKEN`/`NOTION_DATABASE_ID` set, same as
   below):

   ```bash
   make notion-init
   ```

   This renames the database's default title property to "Title" (a
   fresh database usually calls it "Name") and adds every property
   `internal/notion/schema.go` expects (Company, URL, Site, Location,
   First Seen, Seen/Bookmarked/Hidden/Expired, and the optional Employment
   Type/Career Level/Application Start/Application Deadline/Min Years
   Experience — these five stay blank for a site that doesn't report or
   extract them, like Wanted; naver and line fill in most of them today).
   Safe to re-run any time — it only adds what's missing and never
   touches an existing property, so it won't clobber manual edits or data
   already crawled in. A posting's full scraped body text goes into the
   Notion page's own content, not a property — open the page to read it.

## Google Sheets setup (alternative to Notion)

Sheets doesn't have Notion's "integration" concept — the equivalent is a
Google Cloud **service account**, which both the crawler and the macOS
app authenticate as.

1. In [Google Cloud Console](https://console.cloud.google.com/), create a
   project (or use an existing one) and enable the **Google Sheets API**
   (APIs & Services -> Enable APIs -> search "Google Sheets API").
   Free — no billing account required for this API's free quota.
2. Go to **APIs & Services -> Credentials -> Create Credentials -> Service
   account**. Give it any name and finish creation (no roles needed).
3. Open the new service account -> **Keys** tab -> **Add Key -> Create new
   key -> JSON**. This downloads a `.json` file — its entire contents are
   `GOOGLE_SERVICE_ACCOUNT_JSON`. Treat it like a password: it's a
   long-lived credential, not a token that expires.
4. Create a Google Sheet, add a tab (default name expected: "Postings"),
   and **Share** it with the service account's email — it's the
   `client_email` field inside the JSON file, looks like
   `xxx@yyy.iam.gserviceaccount.com`. Give it **Editor** access, the same
   idea as sharing a Notion database with a Notion integration.
5. Copy the spreadsheet ID from its URL:
   `https://docs.google.com/spreadsheets/d/<SPREADSHEET_ID>/edit` — this is
   `GOOGLE_SHEETS_SPREADSHEET_ID`.

The crawler creates the header row (`Title | Company | URL | Site |
Location | First Seen | Seen | Bookmarked | Hidden | Expired |
Employment Type | Career Level | Application Start | Application
Deadline | Description | Min Years Experience | Minimum Degree`, matching
`internal/sheets/sheets.go`) automatically on first run if the sheet is
empty. Unlike Notion, Sheets has no separate "page body" concept, so
Description is just its own column here — full scraped text and all,
since a Sheets cell can hold up to 50,000 characters.

## Local testing (crawler)

```bash
# Notion (default backend):
export STORE_BACKEND=notion   # optional, this is the default
export NOTION_TOKEN=secret_xxx
export NOTION_DATABASE_ID=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
go run ./cmd/crawler

# or Google Sheets:
export STORE_BACKEND=sheets
export GOOGLE_SERVICE_ACCOUNT_JSON="$(cat service-account-key.json)"
export GOOGLE_SHEETS_SPREADSHEET_ID=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
go run ./cmd/crawler
```

Edit `config/keywords.yaml` for what actually gets saved (title
keywords/exclusions, `employment_types`/`career_levels` allow-lists — the
latter two only affect sites that report them, currently naver) and
per-site crawl settings. A posting only gets saved if it passes every
filter. Changing filters is retroactive only in the sense that it stops
*new* matches from being saved — it doesn't remove postings already
stored, since dedup keys off URL alone regardless of the current filter.

## GitHub Actions

Add whichever backend's secrets you're using as repository secrets
(Settings -> Secrets and variables -> Actions) — `NOTION_TOKEN` +
`NOTION_DATABASE_ID` for Notion, or `GOOGLE_SERVICE_ACCOUNT_JSON` +
`GOOGLE_SHEETS_SPREADSHEET_ID` for Sheets — plus `STORE_BACKEND` if you're
using Sheets (Notion is the default, so it needs no secret for that case).
Two workflows, split so the Go toolchain only runs when code actually
changes:

- **`build.yml`** — runs on push to `main` (only when `cmd/`, `internal/`,
  or `go.mod`/`go.sum` change) or manual dispatch. Compiles the crawler
  and publishes it as the `crawler` asset on a `latest` GitHub Release
  (a rolling release, overwritten on every build — not a version tag).
- **`crawl.yml`** — runs every 15 minutes. No `setup-go`, no compiling: it
  checks out just `config/` (so keyword edits take effect without a
  rebuild), downloads the `crawler` binary from the `latest` release via
  `gh release download`, and runs it.

Both can also be triggered manually from the Actions tab
(`workflow_dispatch`). The first time you push, run `build.yml` once
(manually, or via a push) before `crawl.yml` has anything to download.

Note: GitHub Actions' scheduled cron can lag by a few minutes under load —
common for free-tier usage, not a sign anything is broken.

## macOS app (mac/)

A SwiftUI app that reads whichever backend is configured and shows
postings as a **kanban-style board — one scrollable column per site**,
laid out left-to-right so more sites means using the window's width
rather than an ever-longer single list. It writes back too
(Seen/Bookmarked/Hidden only — see "Posting states" above), through the
same `JobStore` protocol both backends implement (`NotionClient.swift`,
`GoogleSheetsStore.swift`) — the UI code (`ContentView.swift`) never
knows which one is active.

The window is a normal resizable one (960x640 by default, no minimum
that's absurdly small) with vibrancy behind the content — earlier this
was a tiny fixed-size floating widget with no window chrome; that fought
normal ergonomics once there was enough content to actually want to
resize, drag between spaces, or close with a traffic-light button, so
`VisualEffectView.swift`'s `WindowConfigurator` now only makes the
background transparent and leaves the rest of window behavior alone.

**Filtering, in the app, not just in `config/keywords.yaml`:** a search
field (title/company) plus multi-select menus for Employment Type and
Career Level sit above the board — built from whatever values actually
show up in the current feed, so they're never stale. These are local,
instant, and non-destructive: they change what's *shown*, and (like
per-site muting) never touch what's stored in Notion/Sheets. Compare with
editing `config/keywords.yaml`, which changes what the crawler *saves* in
the first place — the two are complementary, not alternatives.

**Reading the full posting, in the app:** clicking a card opens a detail
sheet with the full scraped body text, not just the title/company summary
— and marks it Seen the moment you view it. A separate "Open Posting"
button in the sheet is what actually launches the browser. Description
text arrives differently per backend: it's just another Sheets column,
so it's already on the `JobPosting` the moment the feed loads; Notion has
no "long text" property type (see "Notion setup" above — the crawler puts
it in the page's own body content instead), so the app fetches it lazily,
one extra API call, only when you actually open a card's detail sheet.

**Why a plain Swift Package instead of Tauri or a full Xcode project:**
getting a genuinely translucent/vibrancy window (`NSVisualEffectView`) is
native-AppKit territory either way, so Tauri would mean carrying a
Rust+WebView runtime just to reach the same effect Swift gets for free.
A Swift Package (`Package.swift`) avoids hand-writing an `.xcodeproj`
by hand while still being open-able in Xcode later if you want code
signing, an app icon, or to distribute a real `.app` bundle.

**Google Sheets from the app needs a small extra dependency:** writing to
Sheets requires an OAuth2 access token, which for a service account means
signing a JWT with RS256. Foundation has no RSA signing, so `Package.swift`
pulls in [apple/swift-crypto](https://github.com/apple/swift-crypto)'s
`_CryptoExtras` module for that one operation (`GoogleAuth.swift`) — no
other external dependency is used anywhere else in the app.

Run it locally, either as a terminal-attached process or as a real app:

```bash
make mac-run    # swift run — fastest edit/rebuild loop, but terminal-bound
make mac-open   # packages mac/build-app.sh's NotifyMe.app and opens it
```

`make mac-open` (or `make mac-app` without opening it) builds an actual
`NotifyMe.app` at `mac/.build/release-app/NotifyMe.app` — double-click it,
drag it to `/Applications`, or right-click -> Add to Dock, same as any
other Mac app. No Xcode project needed for that: a `.app` is just a
folder shaped a specific way (`Contents/MacOS/<executable>` +
`Contents/Info.plist`, see `mac/Info.plist` and `mac/build-app.sh`), and
LaunchServices treats anything shaped like that as a real app — ad-hoc
codesigned so Gatekeeper doesn't complain about a locally-built binary.

On first launch, click the gear icon, pick **Notion** or **Google Sheets**
at the top, and fill in that backend's fields (Notion: integration secret
+ database ID; Sheets: the whole service-account JSON + spreadsheet ID).
These are stored in `UserDefaults` — fine for a single-user local tool;
move to Keychain if you ever share this app with anyone else (the Sheets
service-account key especially, since unlike a Notion token it doesn't
expire on its own).

It refreshes every 60 seconds. On a card: the star toggles Bookmarked,
the x hides it from the feed, clicking anywhere else opens the detail
sheet. In the detail sheet: Bookmark/Hide are there too, and "Open
Posting" launches the URL. The star icon in the filter bar shows
bookmarks only. New crawler runs remove postings that have closed; legacy
records that were already marked Expired remain visible (dimmed, with a "마감"
tag) unless also hidden.

**D-day:** a card and its detail sheet show a "D-N" countdown (turning
orange at D-3, red at D-DAY/past due) computed from `applicationDeadline`
— only shown for postings whose site actually reports a deadline
(currently naver and line; a site like wanted without one falls back to
a plain "N days ago" from `First Seen` instead).

**Sorting:** the ↕ menu in the filter bar picks how every column orders
its cards — 최신순 (newest by First Seen, the default), 먼저 올라온순
(oldest first), or 마감임박순 (soonest deadline first; postings with no
deadline sort to the bottom regardless).

**Hiding a whole site's column:** the eye-slash icon in each column's
header hides that column; a "N hidden" menu appears in the filter bar to
bring one back. This is different from removing a site from
`config/keywords.yaml`: that only stops the crawler from saving *new*
postings from that site going forward, while this hide applies instantly
to postings already in the feed and is stored locally
(`CredentialsStore.mutedSites`) — nothing is deleted or changed in
Notion/Sheets, so bringing a column back shows everything again.

**Known limitation:** because `swift run` launches a bare executable (no
`.app` bundle), macOS defaults to treating it as a background-only process
with no visible window. `NotifyMeApp.swift` works around this by forcing
`NSApp.setActivationPolicy(.regular)` on launch — a real Xcode-built `.app`
wouldn't need that. I verified the package builds and the process runs as
a foreground app (confirmed via `lsappinfo`), but this sandbox has no
attached display for me to screenshot the actual window — please run it
yourself once to confirm the board layout and vibrancy look right.
