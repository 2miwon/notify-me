package job

import (
	"strings"
	"unicode"
)

// devTitleKeywords are title fragments that reliably mark a software/
// engineering role in English or Korean job titles.
var devTitleKeywords = []string{
	"engineer", "developer", "swe", "software", "sre", "devops",
	"architect", "programmer", "scientist", "backend", "back-end",
	"frontend", "front-end", "full-stack", "fullstack", "tech lead",
	"server develop", "security research", "machine learning",
	"researcher", "forward deployed", "data pipeline",
	"security analyst", "system security", "it security",
	"엔지니어", "개발자", "프로그래머", "과학자", "연구원", "전문연구요원",
	"백엔드", "프론트엔드", "풀스택",
}

// weakDevTitleKeywords mark a dev role only when nothing in the title
// says otherwise: "서버 개발" is dev, "개발 3본부 PM" is not.
var weakDevTitleKeywords = []string{"개발"}

// nonDevTitlePhrases are removed before keyword matching so that e.g.
// "UX Researcher" doesn't count as "researcher".
var nonDevTitlePhrases = []string{"사업개발", "business develop", "ux research", "user research"}

// nonDevTitleHints mark a title as a non-engineering role even when its
// category is technical — policy/compliance/privacy work filed under a
// security team, analysts under a data team, PMs under engineering.
var nonDevTitleHints = []string{
	"compliance", "컴플라이언스", "정책", "개인정보", "privacy", "audit", "감사",
	"analyst", "번역", "translat", "assistant", "어시스턴트",
	"product manager", "program manager", "project manager", "product owner", "기획",
}

// devTitleWords only count as whole words ("dba" inside another word is
// unlikely, but "ml" inside "html"/"xml" isn't).
var devTitleWords = map[string]bool{"dba": true, "ml": true, "fde": true}

// IsDevTitle reports whether a job title reads like a software/engineering
// role. It's a recall-biased heuristic for sites whose own category data
// is too coarse or inconsistent to filter on alone — pair it with a
// site-specific category check (either signal passing keeps a posting)
// rather than using it as the only filter where better data exists.
func IsDevTitle(title string) bool {
	lower := normalizeTitle(title)
	for _, kw := range devTitleKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	for _, word := range words(lower) {
		if devTitleWords[word] {
			return true
		}
	}
	if IsNonDevTitle(title) {
		return false
	}
	for _, kw := range weakDevTitleKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// IsNonDevTitle reports whether a title reads like a non-engineering role
// (PM, policy, compliance, analyst, ...) — used to overrule a technical
// category, never a title that IsDevTitle already accepts.
func IsNonDevTitle(title string) bool {
	lower := normalizeTitle(title)
	for _, hint := range nonDevTitleHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	for _, word := range words(lower) {
		if word == "pm" {
			return true
		}
	}
	return false
}

// LooksDev is the shared dev-role decision for adapters whose site gives
// a category (team, department, occupation, job group): a dev-looking
// title always wins; otherwise a technical category wins unless the
// title itself says it's a non-engineering role.
func LooksDev(title string, categories ...string) bool {
	if IsDevTitle(title) {
		return true
	}
	if IsNonDevTitle(title) {
		return false
	}
	for _, c := range categories {
		if IsDevCategory(c) {
			return true
		}
	}
	return false
}

func normalizeTitle(title string) string {
	lower := strings.ToLower(title)
	for _, phrase := range nonDevTitlePhrases {
		lower = strings.ReplaceAll(lower, phrase, " ")
	}
	return lower
}

func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
}

// devCategoryHints mark a site's own category/department/occupation name
// as technical when they appear anywhere in it.
var devCategoryHints = []string{
	"engineering", "software", "develop", "devops", "infra", "data",
	"security", "machine learning", "platform", "programming",
	"개발", "인프라", "데이터", "보안", "기술", "엔지니어링", "프로그래밍",
}

// devCategoryWords only count as whole words — "ai" as a substring would
// otherwise match "Retail", "Maintenance", "Chain", ...; "tech" would
// match "Technical Art" (NC's technical-artist job).
var devCategoryWords = map[string]bool{"ai": true, "ml": true, "it": true, "dba": true, "tech": true}

// nonDevCategoryHints override a match: "사업개발" (business development)
// contains "개발" but is a sales/BD function.
var nonDevCategoryHints = []string{"사업개발", "business develop"}

// IsDevCategory reports whether a site's own category label (department,
// team, occupation, job group) looks technical. Deliberately broad — it's
// meant to be OR'd with IsDevTitle, not used alone.
func IsDevCategory(category string) bool {
	lower := strings.ToLower(strings.TrimSpace(category))
	if lower == "" {
		return false
	}
	for _, hint := range nonDevCategoryHints {
		if strings.Contains(lower, hint) {
			return false
		}
	}
	for _, hint := range devCategoryHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	for _, word := range words(lower) {
		if devCategoryWords[word] {
			return true
		}
	}
	return false
}
