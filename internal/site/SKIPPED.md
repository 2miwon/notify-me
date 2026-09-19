# Sites investigated and skipped

Not adapter code — this documents sites that were checked and found not
crawlable with this project's approach (plain `net/http`, no headless
browser), so the same investigation isn't repeated later and the reason
isn't lost.

## Meta (metacareers.com)

Listing data isn't in the server HTML at all (confirmed: fetched the raw
page, searched for a known job title from the rendered result — zero
matches in ~390KB of markup). It's loaded via Facebook's internal,
versioned GraphQL API (`POST /graphql`), which requires session/build
tokens (`lsd`, `__hs`, `__rev`) scraped from a freshly loaded page and
which change on Meta's own redeploys. A plain `curl` without a real
browser session gets HTTP 400 outright.

Would need a headless browser (chromedp) to render the page and read the
DOM after JS runs — technically possible, but adds a heavy dependency
(bundling/launching Chromium) to a project whose crawler is otherwise a
single static Go binary with zero external runtime deps, for one site.
Skipped rather than take on that cost; revisit if chromedp gets added for
some other reason anyway.

## ByteDance (joinbytedance.com)

Same "no server HTML, client-fetched" situation as Meta, but for a
different reason: the real endpoint (confirmed by triggering the site's
own "Search now" button and watching the network request) is
`POST /api/v1/search/job/posts`. Calling it — even from *inside* a real
browser session on the actual page, not just via `curl` — returns
`508 Access Denied` from Akamai's bot management (`errors.edgesuite.net`).
The live site's own real users are being blocked by this, not just
automated requests; it's not a header/token problem to work around.

Akamai bot management is also known for aggressively blocking datacenter
IP ranges, which is exactly what GitHub Actions runners are — so even if
this specific block clears up, a scheduled crawl from GitHub Actions
would be a likely ongoing target for it. Skipped.
