package job

import (
	"regexp"
	"strings"
)

// CanonicalEmploymentType maps the common lowercase-hyphenated English
// employment-type labels several sites use ("full-time", "Full-Time",
// "contractor", ...) onto this project's cross-site vocabulary
// (Full-time/Part-time/Contract/Internship/Temporary — see
// internal/site/naver's employmentTypeNames for why one vocabulary
// matters). A site with a different native vocabulary (e.g. naver's
// Korean 정규/인턴/계약) needs its own mapping table instead of this.
func CanonicalEmploymentType(raw string) string {
	switch strings.ToLower(raw) {
	case "full-time", "full time":
		return "Full-time"
	case "part-time", "part time":
		return "Part-time"
	case "contractor", "contract":
		return "Contract"
	case "intern", "internship":
		return "Internship"
	case "temporary", "temp":
		return "Temporary"
	default:
		return raw
	}
}

// EmploymentTypeFromTitle reads an employment type written into a posting
// title — Korean boards commonly suffix titles with it, e.g. KRAFTON's
// "Research Engineer (2년 이상 / 계약직)". Empty when the title says nothing.
func EmploymentTypeFromTitle(title string) string {
	switch {
	case strings.Contains(title, "인턴"), internWord.MatchString(title):
		return "Internship"
	case strings.Contains(title, "계약직"), contractWord.MatchString(title):
		return "Contract"
	case strings.Contains(title, "정규직"):
		return "Full-time"
	default:
		return ""
	}
}

// CanonicalKoreanEmploymentType maps Korean employment-type labels
// (정규직/계약직/단기계약직/인턴/...) onto the cross-site vocabulary, falling
// back to CanonicalEmploymentType for English ones.
func CanonicalKoreanEmploymentType(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return ""
	case strings.Contains(raw, "인턴"):
		return "Internship"
	case strings.Contains(raw, "무기계약"), strings.Contains(raw, "정규"):
		return "Full-time"
	case strings.Contains(raw, "계약"):
		return "Contract"
	case strings.EqualFold(raw, "permanent"), strings.EqualFold(raw, "regular"):
		return "Full-time"
	case contractWord.MatchString(raw):
		// KRAFTON: "Contractor" / "Professional Contractor".
		return "Contract"
	default:
		return CanonicalEmploymentType(raw)
	}
}

var (
	// Whole words only — "Internal Tools", "International", "Smart
	// Contracts" must not read as an employment type.
	internWord   = regexp.MustCompile(`(?i)\bintern(ship)?s?\b`)
	contractWord = regexp.MustCompile(`(?i)\bcontract(or)?\b`)

	careerAnyPattern         = regexp.MustCompile(`경력\s*무관|신입\s*[/·,]\s*경력|경력\s*[/·,]\s*신입`)
	careerExperiencedPattern = regexp.MustCompile(`\d+\s*년\s*(이상|~)|\d+\s*~\s*\d+\s*년|\(\s*경력\s*\)|경력직`)
)

// CareerLevelFromTitle reads a 신입/경력/무관 marker written into a
// posting title ("(경력무관)", "(5년 이상)", "(신입)"). "N년 이하/미만" is
// left empty — it's open to new grads but isn't a 신입 posting either.
func CareerLevelFromTitle(title string) string {
	switch {
	case careerAnyPattern.MatchString(title):
		return "무관"
	case strings.Contains(title, "신입"):
		return "신입"
	case careerExperiencedPattern.MatchString(title):
		return "경력"
	default:
		return ""
	}
}
