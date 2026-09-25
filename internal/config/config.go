// Package config loads the crawler's user-editable filter and per-site
// settings from a YAML file, so changing keywords or a site's crawl
// parameters never requires touching Go code.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/2miwon/notify-me/internal/job"
	"gopkg.in/yaml.v3"
)

// SiteConfig holds settings for one site adapter. Fields are a union
// across every adapter — each one only reads the fields it needs — since
// the sites map is small and per-adapter config structs would need their
// own YAML plumbing for no real benefit at this scale.
type SiteConfig struct {
	// wanted
	JobGroupID int `yaml:"job_group_id"`
	MaxPages   int `yaml:"max_pages"`

	// naver: category codes from ticking filters on
	// https://recruit.navercorp.com/rcrt/list.do and reading them back
	// out of the URL's subJobCdArr param.
	SubJobCodes []string `yaml:"sub_job_codes"`

	// line: filter values from ticking filters on
	// https://careers.linecorp.com/ko/jobs and reading them back out of
	// the URL's fi/ci/co params, respectively. Each is an allow-list
	// (OR'd within itself); empty means "don't filter on this dimension".
	JobFields []string `yaml:"job_fields"`
	Cities    []string `yaml:"cities"`
	Regions   []string `yaml:"regions"`

	// kakaobank: values from the careers URL's recruitClassNames and
	// recruitEmployeeTypes query parameters. Each list is an allow-list;
	// empty means not to filter on that dimension.
	RecruitClassNames    []string `yaml:"recruit_class_names"`
	RecruitEmployeeTypes []string `yaml:"recruit_employee_types"`

	// kakao: values from careers.kakao.com's company/part/skillSet and
	// employeeType query parameters.
	KakaoCompany      string `yaml:"kakao_company"`
	KakaoPart         string `yaml:"kakao_part"`
	KakaoSkillSet     string `yaml:"kakao_skill_set"`
	KakaoEmployeeType string `yaml:"kakao_employee_type"`

	// woowahan: values from career.woowahan.com's category/jobCodes filters.
	WoowahanJobGroupCodes []string `yaml:"woowahan_job_group_codes"`
	WoowahanJobCodes      []string `yaml:"woowahan_job_codes"`

	// coupang: exact Greenhouse location and department names. Empty means
	// no filtering on that dimension.
	CoupangLocations   []string `yaml:"coupang_locations"`
	CoupangDepartments []string `yaml:"coupang_departments"`

	// daangn: department slugs from careers.daangn.com's division query.
	DaangnDepartmentSlugs []string `yaml:"daangn_department_slugs"`

	// navercloud: listing groups such as Tech, Design, Service & Business,
	// or Corporate. Empty means every NAVER Cloud group.
	NaverCloudJobGroups []string `yaml:"navercloud_job_groups"`

	// nhn: job-group ID from careers.nhn.com/recruits?jobGroupId=... .
	NHNJobGroupID string `yaml:"nhn_job_group_id"`

	// openai: exact department/location labels from OpenAI's public Ashby
	// board. Empty lists disable the corresponding filter.
	OpenAIDepartments []string `yaml:"openai_departments"`
	OpenAILocations   []string `yaml:"openai_locations"`

	// apple: location code from jobs.apple.com/en-us/search?location=...
	AppleLocationCode string `yaml:"apple_location_code"`

	// microsoft: location/query submitted to apply.careers.microsoft.com.
	// MicrosoftDevOnly restricts results to postings that look like
	// software/engineering roles — see internal/site/microsoft's
	// devRelated. A plain location search otherwise returns mostly
	// sales/CSM/facilities postings ahead of the actual dev roles.
	MicrosoftQuery    string `yaml:"microsoft_query"`
	MicrosoftLocation string `yaml:"microsoft_location"`
	MicrosoftDevOnly  bool   `yaml:"microsoft_dev_only"`

	// amazon: category[]/location[] values from amazon.jobs/en/search.
	// Each is an allow-list; empty means don't filter on that dimension.
	AmazonCategories []string `yaml:"amazon_categories"`
	AmazonLocations  []string `yaml:"amazon_locations"`

	// netflix: exact location strings from explore.jobs.netflix.net's own
	// location filter. NetflixDevOnly restricts results to postings that
	// look like engineering roles — see internal/site/netflix's
	// devRelated for how (and why it's recall-biased, not exact).
	NetflixLocations []string `yaml:"netflix_locations"`
	NetflixDevOnly   bool     `yaml:"netflix_dev_only"`

	// meta: rendered by Chromium only; see internal/site/meta. This filters
	// the Seoul search result to software/engineering titles.
	MetaDevOnly bool `yaml:"meta_dev_only"`

	// nvidia: search query/location submitted to jobs.nvidia.com.
	NvidiaQuery    string `yaml:"nvidia_query"`
	NvidiaLocation string `yaml:"nvidia_location"`

	// GreetingHR (그리팅)-hosted sites (kakaomobility, kakaopay, ...) —
	// see internal/site/greetinghr. ListPath is the filtered listing
	// page's own path+query, copied verbatim from the company's site
	// since each one names its own filter param differently.
	GreetinghrSubdomain string `yaml:"greetinghr_subdomain"`
	// GreetinghrBaseURL overrides the default <subdomain>.career.
	// greetinghr.com origin for a company on its own custom domain
	// (e.g. Upstage's careers.upstage.ai). Leave unset otherwise.
	GreetinghrBaseURL  string `yaml:"greetinghr_base_url"`
	GreetinghrListPath string `yaml:"greetinghr_list_path"`
	GreetinghrCompany  string `yaml:"greetinghr_company"`
	// GreetinghrDevOnly filters by occupation/job/title when the ListPath
	// can't pre-filter to dev roles (see greetinghr.Adapter.DevOnly).
	GreetinghrDevOnly bool `yaml:"greetinghr_dev_only"`

	// sap: filtered search path+query on jobs.sap.com, copied verbatim
	// from the site. SAPDevOnly mirrors microsoft_dev_only — see
	// internal/site/sap's devRelated.
	SAPListPath string `yaml:"sap_list_path"`
	SAPDevOnly  bool   `yaml:"sap_dev_only"`

	// NineHire (나인하이어)-hosted sites — see internal/site/ninehire.
	// NinehireCompanyID is the UUID the site's own homepage passes as
	// `companyId`; copy it from a network request on the company's site.
	NinehireSubdomain string `yaml:"ninehire_subdomain"`
	NinehireCompanyID string `yaml:"ninehire_company_id"`
	NinehireCompany   string `yaml:"ninehire_company"`
	NinehireDevOnly   bool   `yaml:"ninehire_dev_only"`

	// ashby: company slug + location allow-list (case-insensitive
	// substring, checked against primary and secondary locations) against
	// api.ashbyhq.com's public job-board API — see internal/site/ashby.
	AshbyCompanySlug string   `yaml:"ashby_company_slug"`
	AshbyCompany     string   `yaml:"ashby_company"`
	AshbyLocations   []string `yaml:"ashby_locations"`
	AshbyDevOnly     bool     `yaml:"ashby_dev_only"`

	// lever: company slug + exact country/team allow-lists against
	// api.lever.co's public postings API — see internal/site/lever.
	LeverCompanySlug string   `yaml:"lever_company_slug"`
	LeverCompany     string   `yaml:"lever_company"`
	LeverCountries   []string `yaml:"lever_countries"`
	LeverTeams       []string `yaml:"lever_teams"`
	LeverDevOnly     bool     `yaml:"lever_dev_only"`

	// greenhouse: company slug + location allow-list against
	// boards-api.greenhouse.io's public Job Board API — see
	// internal/site/greenhouse.
	GreenhouseCompanySlug string   `yaml:"greenhouse_company_slug"`
	GreenhouseCompany     string   `yaml:"greenhouse_company"`
	GreenhouseLocations   []string `yaml:"greenhouse_locations"`
	GreenhouseDevOnly     bool     `yaml:"greenhouse_dev_only"`

	// automattic: no location filter exists (see internal/site/automattic
	// package doc) — DevOnly is its only knob.
	AutomatticDevOnly bool `yaml:"automattic_dev_only"`

	// Bespoke Korean game/commerce adapters whose only knob is DevOnly —
	// see each internal/site/<name> package for what counts as dev.
	NetmarbleDevOnly   bool `yaml:"netmarble_dev_only"`
	NcsoftDevOnly      bool `yaml:"ncsoft_dev_only"`
	BucketplaceDevOnly bool `yaml:"bucketplace_dev_only"`

	// shopify: location substring allow-list + DevOnly — see
	// internal/site/shopify.
	ShopifyLocations []string `yaml:"shopify_locations"`
	ShopifyDevOnly   bool     `yaml:"shopify_dev_only"`

	// workday: any Workday "myworkdayjobs.com" career site — see
	// internal/site/workday for where host/tenant/site come from. Any
	// sites: entry with workday_host set becomes a workday adapter named
	// after its key.
	WorkdayHost       string   `yaml:"workday_host"`
	WorkdayTenant     string   `yaml:"workday_tenant"`
	WorkdaySite       string   `yaml:"workday_site"`
	WorkdayCompany    string   `yaml:"workday_company"`
	WorkdaySearchText string   `yaml:"workday_search_text"`
	WorkdayLocations  []string `yaml:"workday_locations"`
	WorkdayDevOnly    bool     `yaml:"workday_dev_only"`
}

type Config struct {
	// Locations keeps postings whose source-reported location contains one of
	// these values, case-insensitively. Unlike optional metadata filters, a
	// configured location list is fail-closed: an unknown location must not
	// be treated as Seoul.
	Locations []string `yaml:"locations"`
	// Keywords, if non-empty, keeps only postings whose title contains at
	// least one of them.
	Keywords []string `yaml:"keywords"`
	// ExcludeKeywords always drops a posting whose title contains any of
	// them, even if it also matched Keywords.
	ExcludeKeywords []string `yaml:"exclude_keywords"`
	// EmploymentTypes/CareerLevels, if non-empty, keep only postings whose
	// site-reported value (job.Posting.EmploymentType/CareerLevel) is in
	// the list. A posting whose site doesn't report that field at all
	// always passes — the filter only applies where the data exists.
	EmploymentTypes []string `yaml:"employment_types"`
	CareerLevels    []string `yaml:"career_levels"`
	// MinYearsExperience/MaxYearsExperience bound job.Posting's own
	// (best-effort, extracted) MinYearsExperience — nil means
	// unbounded on that side. A posting with no extracted value always
	// passes, same "absent data always passes" rule as every other
	// optional-field filter here.
	MinYearsExperience *int `yaml:"min_years_experience"`
	MaxYearsExperience *int `yaml:"max_years_experience"`
	// MinimumDegrees keeps postings whose extracted minimum degree matches
	// one of these normalized values (Bachelor/Master/Doctorate, etc.).
	// Empty means no degree filter; a posting with no recognizable degree
	// requirement always passes.
	MinimumDegrees []string              `yaml:"minimum_degrees"`
	Sites          map[string]SiteConfig `yaml:"sites"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &cfg, nil
}

// MatchesKeywords reports whether title contains any configured keyword
// (case-insensitive). An empty keyword list matches everything.
func (c *Config) MatchesKeywords(title string) bool {
	return len(c.Keywords) == 0 || containsAny(title, c.Keywords)
}

// MatchesLocation reports whether a source-reported location is in scope.
// Location labels vary by source ("서울", "Seoul, South Korea"), so this is
// a case-insensitive substring comparison. An empty reported location fails
// whenever a location filter is configured, preventing accidental inclusion
// of an unknown office.
func (c *Config) MatchesLocation(location string) bool {
	if len(c.Locations) == 0 {
		return true
	}
	location = strings.ToLower(strings.TrimSpace(location))
	if location == "" {
		return false
	}
	for _, allowed := range c.Locations {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed != "" && strings.Contains(location, allowed) {
			return true
		}
	}
	return false
}

// ExcludedByKeyword reports whether title contains any exclude_keyword —
// checked separately from, and after, MatchesKeywords, so an exclude
// always wins even if the title also happens to match an include keyword.
func (c *Config) ExcludedByKeyword(title string) bool {
	return len(c.ExcludeKeywords) > 0 && containsAny(title, c.ExcludeKeywords)
}

// MatchesEmploymentType reports whether a posting's employment type
// passes the employment_types allow-list. An empty allow-list, or a
// posting whose site didn't report an employment type, always passes —
// this filter only kicks in where both the config and the data exist.
func (c *Config) MatchesEmploymentType(employmentType string) bool {
	return matchesAllowList(employmentType, c.EmploymentTypes)
}

// MatchesCareerLevel is MatchesEmploymentType's counterpart for
// career_levels.
func (c *Config) MatchesCareerLevel(careerLevel string) bool {
	return matchesAllowList(careerLevel, c.CareerLevels)
}

// MatchesYearsExperience reports whether a posting's (possibly absent)
// extracted years-of-experience requirement falls within
// [MinYearsExperience, MaxYearsExperience]. A nil years value (the
// extractor found nothing) always passes, same as an unset bound on
// either side.
func (c *Config) MatchesYearsExperience(years *int) bool {
	if years == nil {
		return true
	}
	if c.MinYearsExperience != nil && *years < *c.MinYearsExperience {
		return false
	}
	if c.MaxYearsExperience != nil && *years > *c.MaxYearsExperience {
		return false
	}
	return true
}

// MatchesMinimumDegree is the degree counterpart to MatchesYearsExperience.
// Unknown requirements pass, avoiding false exclusions from inconsistent job
// copy; configured labels are normalized so "학사" and "Bachelor" agree.
func (c *Config) MatchesMinimumDegree(degree string) bool {
	if len(c.MinimumDegrees) == 0 || degree == "" {
		return true
	}
	degree = job.NormalizeDegree(degree)
	for _, allowed := range c.MinimumDegrees {
		if degree == job.NormalizeDegree(allowed) {
			return true
		}
	}
	return false
}

func matchesAllowList(value string, allowList []string) bool {
	if len(allowList) == 0 || value == "" {
		return true
	}
	for _, allowed := range allowList {
		if strings.EqualFold(value, allowed) {
			return true
		}
	}
	return false
}

func containsAny(title string, keywords []string) bool {
	lower := strings.ToLower(title)
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
