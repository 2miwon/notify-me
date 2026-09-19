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
//	NOTION_DATABASE_ID      Target database ID
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
	"log"
	"os"
	"time"

	"github.com/2miwon/notify-me/internal/config"
	"github.com/2miwon/notify-me/internal/job"
	"github.com/2miwon/notify-me/internal/notion"
	"github.com/2miwon/notify-me/internal/sheets"
	"github.com/2miwon/notify-me/internal/site"
	"github.com/2miwon/notify-me/internal/site/amazon"
	"github.com/2miwon/notify-me/internal/site/apple"
	"github.com/2miwon/notify-me/internal/site/ashby"
	"github.com/2miwon/notify-me/internal/site/coupang"
	"github.com/2miwon/notify-me/internal/site/daangn"
	"github.com/2miwon/notify-me/internal/site/dunamu"
	"github.com/2miwon/notify-me/internal/site/google"
	"github.com/2miwon/notify-me/internal/site/greetinghr"
	"github.com/2miwon/notify-me/internal/site/kakao"
	"github.com/2miwon/notify-me/internal/site/kakaobank"
	"github.com/2miwon/notify-me/internal/site/line"
	"github.com/2miwon/notify-me/internal/site/microsoft"
	"github.com/2miwon/notify-me/internal/site/naver"
	"github.com/2miwon/notify-me/internal/site/navercloud"
	"github.com/2miwon/notify-me/internal/site/netflix"
	"github.com/2miwon/notify-me/internal/site/nhn"
	"github.com/2miwon/notify-me/internal/site/ninehire"
	"github.com/2miwon/notify-me/internal/site/nvidia"
	"github.com/2miwon/notify-me/internal/site/openai"
	"github.com/2miwon/notify-me/internal/site/sap"
	"github.com/2miwon/notify-me/internal/site/wanted"
	"github.com/2miwon/notify-me/internal/site/woowahan"
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
	if len(adapters) == 0 {
		log.Println("no site adapters configured, nothing to do")
		return nil
	}

	// 4 minutes, not 5 (GitHub Actions' crawl.yml job has a hard 5-minute
	// timeout) — this run's own list of adapters has grown to include
	// several that fetch a full detail page per posting (apple, amazon,
	// microsoft, netflix, nvidia), so 2 minutes for the whole run across
	// every site was already tight before that and now regularly isn't
	// enough, which surfaces as Notion writes near the end of the adapter
	// list failing with "context deadline exceeded" rather than as a
	// clean failure earlier.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	st, err := buildStore(ctx)
	if err != nil {
		return err
	}
	if notionStore, ok := st.(*notion.Client); ok {
		added, _, err := notionStore.EnsureSchema(ctx)
		if err != nil {
			return err
		}
		if len(added) > 0 {
			log.Printf("added missing Notion properties: %v", added)
		}
	}
	log.Printf("using store backend: %s", st.Name())

	existing, err := st.ExistingPostings(ctx)
	if err != nil {
		// Dedup/expiry state is unusable without this — better to stop
		// than to risk creating duplicate rows or wrongly expiring live
		// postings.
		return err
	}
	log.Printf("loaded %d existing postings from %s", len(existing), st.Name())

	var (
		totalFetched int
		totalNew     int
		totalExpired int
		siteErrs     []error
	)

	for _, a := range adapters {
		postings, fetchErr := a.Fetch()
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
				if p.MinYearsExperience != nil && (rec.MinYearsExperience == nil || *p.MinYearsExperience != *rec.MinYearsExperience) {
					if err := st.UpdateMinYearsExperience(ctx, rec.ID, *p.MinYearsExperience); err != nil {
						log.Printf("ERROR: %s: update experience failed for %s: %v", a.Name(), p.URL, err)
						siteErrs = append(siteErrs, err)
					} else {
						rec.MinYearsExperience = p.MinYearsExperience
						existing[p.URL] = rec
					}
				}
				if a.Name() == "kakaobank" && rec.CareerLevel != "" {
					if err := st.UpdateCareerLevel(ctx, rec.ID, ""); err != nil {
						log.Printf("ERROR: %s: clear incorrect career level for %s: %v", a.Name(), p.URL, err)
						siteErrs = append(siteErrs, err)
					} else {
						rec.CareerLevel = ""
						existing[p.URL] = rec
					}
				}
				if p.MinimumDegree != "" && p.MinimumDegree != rec.MinimumDegree {
					if err := st.UpdateMinimumDegree(ctx, rec.ID, p.MinimumDegree); err != nil {
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

			if err := st.CreatePosting(ctx, p); err != nil {
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
			if err := st.DeletePosting(ctx, rec.ID); err != nil {
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
	if sc, ok := cfg.Sites["nvidia"]; ok {
		adapters = append(adapters, nvidia.New(sc.NvidiaQuery, sc.NvidiaLocation))
	}
	if sc, ok := cfg.Sites["kakaomobility"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany))
	}
	if sc, ok := cfg.Sites["kakaopay"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany))
	}
	if sc, ok := cfg.Sites["upstage"]; ok {
		adapters = append(adapters, greetinghr.New(sc.GreetinghrSubdomain, sc.GreetinghrBaseURL, sc.GreetinghrListPath, sc.GreetinghrCompany))
	}
	if sc, ok := cfg.Sites["sap"]; ok {
		adapters = append(adapters, sap.New(sc.SAPListPath, sc.SAPDevOnly))
	}
	if sc, ok := cfg.Sites["kakaopaysec"]; ok {
		adapters = append(adapters, ninehire.New(sc.NinehireSubdomain, sc.NinehireCompanyID, sc.NinehireCompany, sc.NinehireDevOnly))
	}
	if sc, ok := cfg.Sites["snowflake"]; ok {
		adapters = append(adapters, ashby.New(sc.AshbyCompanySlug, sc.AshbyCompany, sc.AshbyLocations))
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

func buildStore(ctx context.Context) (store.Store, error) {
	backend := os.Getenv("STORE_BACKEND")
	if backend == "" {
		backend = "notion"
	}

	switch backend {
	case "notion":
		token := os.Getenv("NOTION_TOKEN")
		databaseID := os.Getenv("NOTION_DATABASE_ID")
		if token == "" || databaseID == "" {
			log.Fatal("NOTION_TOKEN and NOTION_DATABASE_ID must be set for STORE_BACKEND=notion")
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
