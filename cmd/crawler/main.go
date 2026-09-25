// Command crawler fetches events from every registered site adapter,
// filters them against config/keywords.yaml, skips anything already stored
// in the configured datastore, creates a record for each genuinely new
// posting, and removes postings that disappeared from a site's live listing.
//
// Required environment variables:
//
//	STORE_BACKEND  "notion" (default) or "sheets"
//
//	# STORE_BACKEND=notion:
//	NOTION_TOKEN            Notion internal integration secret
//	NOTION_JOBS_DATABASE_ID Target Jobs database ID (NOTION_DATABASE_ID is a migration fallback)
//
//	# STORE_BACKEND=sheets:
//	GOOGLE_SERVICE_ACCOUNT_JSON  raw contents of a Google service account key file
//	GOOGLE_SHEETS_SPREADSHEET_ID target spreadsheet ID
//	GOOGLE_SHEETS_SHEET_NAME     sheet/tab name (default "Postings")
//
// Optional:
//
//	CONFIG_PATH  path to the keyword/site config (default "config/keywords.yaml")
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/2miwon/notify-me/internal/config"
	"github.com/2miwon/notify-me/internal/job"
	"github.com/2miwon/notify-me/internal/notion"
	"github.com/2miwon/notify-me/internal/sheets"
	"github.com/2miwon/notify-me/internal/site"
	"github.com/2miwon/notify-me/internal/site/amazon"
	"github.com/2miwon/notify-me/internal/site/apple"
	"github.com/2miwon/notify-me/internal/site/ashby"
	"github.com/2miwon/notify-me/internal/site/automattic"
	"github.com/2miwon/notify-me/internal/site/bucketplace"
	"github.com/2miwon/notify-me/internal/site/coupang"
	"github.com/2miwon/notify-me/internal/site/daangn"
	"github.com/2miwon/notify-me/internal/site/dunamu"
	"github.com/2miwon/notify-me/internal/site/google"
	"github.com/2miwon/notify-me/internal/site/greenhouse"
	"github.com/2miwon/notify-me/internal/site/greetinghr"
	"github.com/2miwon/notify-me/internal/site/kakao"
	"github.com/2miwon/notify-me/internal/site/kakaobank"
	"github.com/2miwon/notify-me/internal/site/lever"
	"github.com/2miwon/notify-me/internal/site/line"
	"github.com/2miwon/notify-me/internal/site/meta"
	"github.com/2miwon/notify-me/internal/site/microsoft"
	"github.com/2miwon/notify-me/internal/site/naver"
	"github.com/2miwon/notify-me/internal/site/navercloud"
	"github.com/2miwon/notify-me/internal/site/ncsoft"
	"github.com/2miwon/notify-me/internal/site/netflix"
	"github.com/2miwon/notify-me/internal/site/netmarble"
	"github.com/2miwon/notify-me/internal/site/nhn"
	"github.com/2miwon/notify-me/internal/site/ninehire"
	"github.com/2miwon/notify-me/internal/site/nvidia"
	"github.com/2miwon/notify-me/internal/site/openai"
	"github.com/2miwon/notify-me/internal/site/sap"
	"github.com/2miwon/notify-me/internal/site/shopify"
	"github.com/2miwon/notify-me/internal/site/wanted"
	"github.com/2miwon/notify-me/internal/site/woowahan"
	"github.com/2miwon/notify-me/internal/site/workday"
	"github.com/2miwon/notify-me/internal/site/yonsei"
	"github.com/2miwon/notify-me/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("crawler: %v", err)
	}
}

func run() error {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config/keywords.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	adapters := buildAdapters(cfg)
	if onlySite := os.Getenv("ONLY_SITE"); onlySite != "" {
		adapters = filterAdapters(adapters, onlySite)
	}
	// PROBE_SITE=<name> fetches just that adapter and prints what it
	// returned — no store access, no config filters — for checking a new
	// or changed adapter locally.
	if name := os.Getenv("PROBE_SITE"); name != "" {
		return probe(adapters, name)
	}
	if len(adapters) == 0 {
		log.Println("no site adapters configured, nothing to do")
		return nil
	}

	// Reading the existing database should fail quickly. It must not share a
	// deadline with fetching every external board and then writing a large
	// batch of new pages: doing so used to cancel all late Notion writes.
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), time.Minute)
	defer cancelSetup()

	st, err := buildStore(setupCtx)
	if err != nil {
		return err
	}
	if notionStore, ok := st.(*notion.Client); ok {
		added, _, err := notionStore.EnsureSchema(setupCtx)
		if err != nil {
			return err
		}
		if len(added) > 0 {
			log.Printf("added missing Notion properties: %v", added)
		}
	}
	log.Printf("using store backend: %s", st.Name())

	existing, err := st.ExistingPostings(setupCtx)
	if err != nil {
		// Dedup/expiry state is unusable without this — better to stop
		// than to risk creating duplicate rows or wrongly expiring live
		// postings.
		return err
	}
	log.Printf("loaded %d existing postings from %s", len(existing), st.Name())
	cancelSetup()

	// External job boards are independent. Fetch a small bounded set in
	// parallel, then process the results in configured order so deduplication
	// and per-site expiry remain deterministic.
	fetched := fetchAll(adapters, 6)

	// Notion's default plan admits about three requests per second. A single
	// paced writer leaves headroom for the desktop app and avoids a 429 storm.
	// This deadline is intentionally separate from the fetch phase.
	writeCtx, cancelWrites := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancelWrites()
	writePacer := newWritePacer(st.Name() == "notion")
	defer writePacer.Stop()
	write := func(operation func(context.Context) error) error {
		if err := writePacer.Wait(writeCtx); err != nil {
			return err
		}
		return operation(writeCtx)
	}

	var (
		totalFetched int
		totalNew     int
		totalExpired int
		siteErrs     []error
	)

	for _, result := range fetched {
		a := result.adapter
		postings, fetchErr := result.postings, result.err
		log.Printf("fetched %d postings from %s in %s", len(postings), a.Name(), result.duration.Round(time.Millisecond))
		if fetchErr != nil {
			log.Printf("ERROR: %s: fetch failed: %v", a.Name(), fetchErr)
			siteErrs = append(siteErrs, fetchErr)
			// Still process whatever partial results came back, but see
			// the expiry-sweep note below.
		}
		totalFetched += len(postings)

		liveURLs := make(map[string]bool, len(postings))
		for _, p := range postings {
			if !cfg.MatchesLocation(p.Location) {
				continue
			}
			liveURLs[p.URL] = true
			p.MinimumDegree = job.ExtractMinimumDegree(p.Title + "\n" + p.Description)

			if rec, ok := existing[p.URL]; ok {
				// Woo's original adapter had no detail hydration, so existing
				// records have empty page bodies. Fill only empty bodies; this
				// preserves anything already stored by the user or an earlier run.
				if a.Name() == "woowahan" && p.Description != "" {
					if err := write(func(ctx context.Context) error { return st.EnsureDescription(ctx, rec.ID, p.Description) }); err != nil {
						log.Printf("ERROR: %s: backfill description failed for %s: %v", a.Name(), p.URL, err)
						siteErrs = append(siteErrs, err)
					}
				}
				if p.MinYearsExperience != nil && (rec.MinYearsExperience == nil || *p.MinYearsExperience != *rec.MinYearsExperience) {
					if err := write(func(ctx context.Context) error {
						return st.UpdateMinYearsExperience(ctx, rec.ID, *p.MinYearsExperience)
					}); err != nil {
						log.Printf("ERROR: %s: update experience failed for %s: %v", a.Name(), p.URL, err)
						siteErrs = append(siteErrs, err)
					} else {
						rec.MinYearsExperience = p.MinYearsExperience
						existing[p.URL] = rec
					}
				}
				if a.Name() == "kakaobank" && rec.CareerLevel != "" {
					if err := write(func(ctx context.Context) error { return st.UpdateCareerLevel(ctx, rec.ID, "") }); err != nil {
						log.Printf("ERROR: %s: clear incorrect career level for %s: %v", a.Name(), p.URL, err)
						siteErrs = append(siteErrs, err)
					} else {
						rec.CareerLevel = ""
						existing[p.URL] = rec
					}
				}
				// Fill tags an adapter learned to report after the record was
				// stored (e.g. greenhouse reading "(5년 이상 / 계약직)" from
				// titles); never overwrite a value that's already there.
				if p.EmploymentType != "" && rec.EmploymentType == "" {
					if err := write(func(ctx context.Context) error { return st.UpdateEmploymentType(ctx, rec.ID, p.EmploymentType) }); err != nil {
						log.Printf("ERROR: %s: update employment type failed for %s: %v", a.Name(), p.URL, err)
						siteErrs = append(siteErrs, err)
					} else {
						rec.EmploymentType = p.EmploymentType
						existing[p.URL] = rec
					}
				}
				if p.CareerLevel != "" && rec.CareerLevel == "" && a.Name() != "kakaobank" {
					if err := write(func(ctx context.Context) error { return st.UpdateCareerLevel(ctx, rec.ID, p.CareerLevel) }); err != nil {
						log.Printf("ERROR: %s: update career level failed for %s: %v", a.Name(), p.URL, err)
						siteErrs = append(siteErrs, err)
					} else {
						rec.CareerLevel = p.CareerLevel
						existing[p.URL] = rec
					}
				}
				if p.MinimumDegree != "" && p.MinimumDegree != rec.MinimumDegree {
					if err := write(func(ctx context.Context) error { return st.UpdateMinimumDegree(ctx, rec.ID, p.MinimumDegree) }); err != nil {
						log.Printf("ERROR: %s: update degree failed for %s: %v", a.Name(), p.URL, err)
						siteErrs = append(siteErrs, err)
					} else {
						rec.MinimumDegree = p.MinimumDegree
						existing[p.URL] = rec
					}
				}
				continue
			}
			if !cfg.MatchesKeywords(p.Title) || cfg.ExcludedByKeyword(p.Title) {
				continue
			}
			if !cfg.MatchesEmploymentType(p.EmploymentType) || !cfg.MatchesCareerLevel(p.CareerLevel) {
				continue
			}
			if !cfg.MatchesYearsExperience(p.MinYearsExperience) {
				continue
			}
			if !cfg.MatchesMinimumDegree(p.MinimumDegree) {
				continue
			}

			if err := write(func(ctx context.Context) error { return st.CreatePosting(ctx, p) }); err != nil {
				log.Printf("ERROR: %s: create posting failed for %s: %v", a.Name(), p.URL, err)
				siteErrs = append(siteErrs, err)
				continue
			}

			existing[p.URL] = store.ExistingPosting{Site: a.Name()}
			totalNew++
			log.Printf("added: [%s] %s @ %s", p.Site, p.Title, p.Company)
		}

		if fetchErr != nil {
			// A partial fetch means "missing from liveURLs" no longer
			// implies "gone from the site" — skip the expiry sweep this
			// run rather than risk mass false-expiring postings that are
			// still live but just weren't reached.
			log.Printf("skipping expiry sweep for %s: fetch had errors", a.Name())
			continue
		}

		for url, rec := range existing {
			if rec.Site != a.Name() || liveURLs[url] {
				continue
			}
			// Keep applied-to postings as the user's application history:
			// flag them expired instead of deleting the record.
			if rec.Applied {
				if rec.Expired {
					continue
				}
				if err := write(func(ctx context.Context) error { return st.MarkExpired(ctx, rec.ID) }); err != nil {
					log.Printf("ERROR: %s: mark applied posting expired failed for %s: %v", a.Name(), url, err)
					siteErrs = append(siteErrs, err)
					continue
				}
				rec.Expired = true
				existing[url] = rec
				totalExpired++
				log.Printf("expired (kept, applied): [%s] %s", a.Name(), url)
				continue
			}
			if err := write(func(ctx context.Context) error { return st.DeletePosting(ctx, rec.ID) }); err != nil {
				log.Printf("ERROR: %s: delete expired posting failed for %s: %v", a.Name(), url, err)
				siteErrs = append(siteErrs, err)
				continue
			}
			delete(existing, url)
			totalExpired++
			log.Printf("deleted expired: [%s] %s", a.Name(), url)
		}
	}

	log.Printf("done: fetched=%d new=%d expired=%d errors=%d", totalFetched, totalNew, totalExpired, len(siteErrs))

	if len(siteErrs) > 0 {
		// Non-zero exit so the GitHub Actions run is visibly marked failed,
		// without having discarded the postings that did succeed above.
		os.Exit(1)
	}
	return nil
}

type fetchResult struct {
	adapter  site.Adapter
	postings []job.Posting
	err      error
	duration time.Duration
}

// fetchAll limits simultaneous requests without serializing unrelated job
// boards. Results retain the adapter order so the caller's write/expiry pass
// stays predictable.
func fetchAll(adapters []site.Adapter, workers int) []fetchResult {
	if workers > len(adapters) {
		workers = len(adapters)
	}
	if workers < 1 {
		return nil
	}

	results := make([]fetchResult, len(adapters))
	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for index := range jobs {
				adapter := adapters[index]
				started := time.Now()
				postings, err := adapter.Fetch()
				results[index] = fetchResult{
					adapter:  adapter,
					postings: postings,
					err:      err,
					duration: time.Since(started),
				}
			}
		}()
	}
	for index := range adapters {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	return results
}

// writePacer serializes writes to Notion at 2.5 requests/second, a little
// below its standard 3 requests/second budget. Non-Notion stores remain
// unpaced because they have different throughput rules.
type writePacer struct {
	ticker *time.Ticker
}

func newWritePacer(enabled bool) *writePacer {
	if !enabled {
		return &writePacer{}
	}
	return &writePacer{ticker: time.NewTicker(400 * time.Millisecond)}
}

func (p *writePacer) Wait(ctx context.Context) error {
	if p.ticker == nil {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ticker.C:
		return nil
	}
}

func (p *writePacer) Stop() {
	if p.ticker != nil {
		p.ticker.Stop()
	}
}

func buildAdapters(cfg *config.Config) []site.Adapter {
	var adapters []site.Adapter

	if sc, ok := cfg.Sites["wanted"]; ok {
		adapters = append(adapters, wanted.New(sc.JobGroupID, sc.MaxPages))
	}
	if sc, ok := cfg.Sites["naver"]; ok {
		adapters = append(adapters, naver.New(sc.SubJobCodes))
	}
	if sc, ok := cfg.Sites["navercloud"]; ok {
		adapters = append(adapters, navercloud.New(sc.NaverCloudJobGroups))
	}
	if sc, ok := cfg.Sites["nhn"]; ok {
		adapters = append(adapters, nhn.New(sc.NHNJobGroupID))
	}
	if sc, ok := cfg.Sites["openai"]; ok {
		adapters = append(adapters, openai.New(sc.OpenAIDepartments, sc.OpenAILocations))
	}
	if _, ok := cfg.Sites["google"]; ok {
		adapters = append(adapters, google.New())
	}
	if sc, ok := cfg.Sites["apple"]; ok {
		adapters = append(adapters, apple.New(sc.AppleLocationCode, sc.MaxPages))
	}
	if sc, ok := cfg.Sites["microsoft"]; ok {
		adapters = append(adapters, microsoft.New(sc.MicrosoftQuery, sc.MicrosoftLocation, sc.MicrosoftDevOnly))
	}
	if sc, ok := cfg.Sites["amazon"]; ok {
		adapters = append(adapters, amazon.New(sc.AmazonCategories, sc.AmazonLocations))
	}
	if sc, ok := cfg.Sites["netflix"]; ok {
		adapters = append(adapters, netflix.New(sc.NetflixLocations, sc.NetflixDevOnly))
	}
	// Meta requires a real Chromium session. Keep it out of the normal
	// lightweight run unless the dedicated workflow opts in explicitly.
	if sc, ok := cfg.Sites["meta"]; ok && os.Getenv("META_HEADLESS_ENABLED") == "true" {
		adapters = append(adapters, meta.New(sc.MetaDevOnly))
	}
	if sc, ok := cfg.Sites["nvidia"]; ok {
		adapters = append(adapters, nvidia.New(sc.NvidiaQuery, sc.NvidiaLocation))
	}
	if sc, ok := cfg.Sites["kakaomobility"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany, sc.GreetinghrDevOnly))
	}
	if sc, ok := cfg.Sites["kakaopay"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany, sc.GreetinghrDevOnly))
	}
	if sc, ok := cfg.Sites["upstage"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany, sc.GreetinghrDevOnly))
	}
	if sc, ok := cfg.Sites["sap"]; ok {
		adapters = append(adapters, sap.New(sc.SAPListPath, sc.SAPDevOnly))
	}
	if sc, ok := cfg.Sites["kakaopaysec"]; ok {
		adapters = append(adapters, ninehire.New(sc.NinehireSubdomain, sc.NinehireCompanyID, sc.NinehireCompany, sc.NinehireDevOnly))
	}
	if sc, ok := cfg.Sites["snowflake"]; ok {
		adapters = append(adapters, ashby.New(sc.AshbyCompanySlug, sc.AshbyCompany, sc.AshbyLocations, sc.AshbyDevOnly))
	}
	if sc, ok := cfg.Sites["channeltalk"]; ok {
		adapters = append(adapters, lever.New(sc.LeverCompanySlug, sc.LeverCompany, sc.LeverCountries, sc.LeverTeams, sc.LeverDevOnly))
	}
	if sc, ok := cfg.Sites["sendbird"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["moloco"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["canonical"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["gitlab"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["toss"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["krafton"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["vercel"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["elastic"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["mongodb"]; ok {
		adapters = append(adapters, greenhouse.New(sc.GreenhouseCompanySlug, sc.GreenhouseCompany, sc.GreenhouseLocations, sc.GreenhouseDevOnly))
	}
	if sc, ok := cfg.Sites["kurly"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany, sc.GreetinghrDevOnly))
	}
	if sc, ok := cfg.Sites["musinsa"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany, sc.GreetinghrDevOnly))
	}
	if sc, ok := cfg.Sites["noluniverse"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany, sc.GreetinghrDevOnly))
	}
	if sc, ok := cfg.Sites["hyperconnect"]; ok {
		adapters = append(adapters, lever.New(sc.LeverCompanySlug, sc.LeverCompany, sc.LeverCountries, sc.LeverTeams, sc.LeverDevOnly))
	}
	if sc, ok := cfg.Sites["1password"]; ok {
		adapters = append(adapters, ashby.New(sc.AshbyCompanySlug, sc.AshbyCompany, sc.AshbyLocations, sc.AshbyDevOnly))
	}
	if sc, ok := cfg.Sites["automattic"]; ok {
		adapters = append(adapters, automattic.New(sc.AutomatticDevOnly))
	}
	if sc, ok := cfg.Sites["netmarble"]; ok {
		adapters = append(adapters, netmarble.New(sc.NetmarbleDevOnly))
	}
	if sc, ok := cfg.Sites["ncsoft"]; ok {
		adapters = append(adapters, ncsoft.New(sc.NcsoftDevOnly))
	}
	if sc, ok := cfg.Sites["shopify"]; ok {
		adapters = append(adapters, shopify.New(sc.ShopifyLocations, sc.ShopifyDevOnly))
	}
	if sc, ok := cfg.Sites["bucketplace"]; ok {
		adapters = append(adapters, bucketplace.New(sc.BucketplaceDevOnly))
	}
	// Workday sites are keyed by config name rather than hard-wired here:
	// any entry with workday_host set is one. Sorted for a stable order.
	var workdayNames []string
	for name, sc := range cfg.Sites {
		if sc.WorkdayHost != "" {
			workdayNames = append(workdayNames, name)
		}
	}
	sort.Strings(workdayNames)
	for _, name := range workdayNames {
		sc := cfg.Sites[name]
		adapters = append(adapters, workday.New(sc.WorkdayHost, sc.WorkdayTenant, sc.WorkdaySite, sc.WorkdayCompany, name, sc.WorkdaySearchText, sc.WorkdayLocations, sc.WorkdayDevOnly))
	}
	if _, ok := cfg.Sites["yonsei"]; ok && yonsei.Configured() {
		adapters = append(adapters, yonsei.New())
	} else if _, ok := cfg.Sites["yonsei"]; ok {
		log.Printf("skipping yonsei: YONSEI_ID/YONSEI_PW are not configured")
	}
	if sc, ok := cfg.Sites["line"]; ok {
		adapters = append(adapters, line.New(sc.JobFields, sc.Cities, sc.Regions))
	}
	if sc, ok := cfg.Sites["kakaobank"]; ok {
		adapters = append(adapters, kakaobank.New(sc.RecruitClassNames, sc.RecruitEmployeeTypes))
	}
	if sc, ok := cfg.Sites["kakao"]; ok {
		adapters = append(adapters, kakao.New(sc.KakaoCompany, sc.KakaoPart, sc.KakaoSkillSet, sc.KakaoEmployeeType))
	}
	if sc, ok := cfg.Sites["woowahan"]; ok {
		adapters = append(adapters, woowahan.New(sc.WoowahanJobGroupCodes, sc.WoowahanJobCodes))
	}
	if sc, ok := cfg.Sites["coupang"]; ok {
		adapters = append(adapters, coupang.New(sc.CoupangLocations, sc.CoupangDepartments))
	}
	if sc, ok := cfg.Sites["daangn"]; ok {
		adapters = append(adapters, daangn.New(sc.DaangnDepartmentSlugs))
	}
	if _, ok := cfg.Sites["dunamu"]; ok {
		adapters = append(adapters, dunamu.New())
	}

	return adapters
}

func filterAdapters(adapters []site.Adapter, name string) []site.Adapter {
	for _, adapter := range adapters {
		if adapter.Name() == name {
			return []site.Adapter{adapter}
		}
	}
	return nil
}

func buildStore(ctx context.Context) (store.Store, error) {
	backend := os.Getenv("STORE_BACKEND")
	if backend == "" {
		backend = "notion"
	}

	switch backend {
	case "notion":
		token := os.Getenv("NOTION_TOKEN")
		// Jobs is its own database. Keep the old name as a migration fallback
		// so existing GitHub Actions/local .env files do not suddenly break.
		databaseID := os.Getenv("NOTION_JOBS_DATABASE_ID")
		if databaseID == "" {
			databaseID = os.Getenv("NOTION_DATABASE_ID")
		}
		if token == "" || databaseID == "" {
			log.Fatal("NOTION_TOKEN and NOTION_JOBS_DATABASE_ID (or legacy NOTION_DATABASE_ID) must be set for STORE_BACKEND=notion")
		}
		return notion.New(token, databaseID), nil

	case "sheets":
		saJSON := os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON")
		spreadsheetID := os.Getenv("GOOGLE_SHEETS_SPREADSHEET_ID")
		if saJSON == "" || spreadsheetID == "" {
			log.Fatal("GOOGLE_SERVICE_ACCOUNT_JSON and GOOGLE_SHEETS_SPREADSHEET_ID must be set for STORE_BACKEND=sheets")
		}
		sheetName := os.Getenv("GOOGLE_SHEETS_SHEET_NAME")
		return sheets.New(ctx, []byte(saJSON), spreadsheetID, sheetName)

	default:
		log.Fatalf("unknown STORE_BACKEND %q (want \"notion\" or \"sheets\")", backend)
		return nil, nil // unreachable
	}
}

func probe(adapters []site.Adapter, name string) error {
	for _, a := range adapters {
		if a.Name() != name {
			continue
		}
		start := time.Now()
		postings, err := a.Fetch()
		for _, p := range postings {
			desc := len([]rune(p.Description))
			fmt.Printf("- %s | %s | %s | emp=%q career=%q deadline=%v desc=%d\n  %s\n",
				p.Title, p.Company, p.Location, p.EmploymentType, p.CareerLevel, p.ApplicationDeadline != nil, desc, p.URL)
		}
		fmt.Printf("%s: %d postings in %s, err=%v\n", name, len(postings), time.Since(start).Round(time.Millisecond), err)
		return nil
	}
	return fmt.Errorf("no configured adapter named %q", name)
}
