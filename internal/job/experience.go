package job

import (
	"regexp"
	"strconv"
)

// careerYearsPatterns identify an applicant's overall career requirement.
// These take priority over a generic "N년 이상" match: a posting can ask for
// five years' career while merely mentioning that one service was operated
// for one year, and the latter must not lower the displayed requirement.
var careerYearsPatterns = []*regexp.Regexp{
	// "경력 5년", "경력 최소 10년", and 경험-first phrasing such as
	// "소프트웨어 개발 경험 10년 이상".
	regexp.MustCompile(`(?:총\s*)?(?:경력|경험)(?:이|은)?\s*(?:최소\s*)?(\d{1,2})\s*년`),
	// NCSoft (and several Korean ATS templates) render a structured range
	// as "경력 : 2년 ~ 10년" rather than prose such as "경력 2년 이상".
	// The first number is the minimum requirement.
	regexp.MustCompile(`(?:관련\s*)?경력\s*[:：]\s*(\d{1,2})\s*년`),
	regexp.MustCompile(`(\d{1,2})\s*년\s*(?:이상\s*)?(?:의\s*)?(?:관련\s*)?(?:실무\s*)?경력`),
	// "7년 이상의 백엔드 개발 경험", "3년 이상 10년 이하의 정보보안 업무 경험":
	// 경험 phrased requirements, with a short same-line gap before the noun.
	regexp.MustCompile(`(\d{1,2})\s*년\s*이상(?:의)?[^\n]{0,30}?(?:경력|경험)`),
	regexp.MustCompile(`(?i)(\d{1,2})\s*(?:-|–|—|to)\s*\d{1,2}\+?\s*years?(?:['’]s?)?\s*(?:of\s*)?experience`),
	regexp.MustCompile(`(?i)(?:at\s+least|minimum(?:\s+of)?|more\s+than|over)\s*(\d{1,2})\s*years?(?:['’]s?)?\s+(?:of\s+)?(?:(?:relevant|related|industry|professional|work)\s+)?experience`),
	// "10+ years of professional backend development experience": up to a
	// few qualifier words may sit between "years of" and "experience".
	regexp.MustCompile(`(?i)(\d{1,2})\+?\s*years?(?:['’]s?)?\s+(?:of\s+)?(?:[\w/&-]+\s+){0,4}?experience`),
	regexp.MustCompile(`(?i)experience\s+of\s+(\d{1,2})\s*years?`),
}

// yearsPatterns are a fallback for postings without an explicit career
// phrase. Each pattern's first capture group is the number of years.
var yearsPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(\d{1,2})\s*년\s*이상`),                                                                // "10년 이상"
	regexp.MustCompile(`경력\s*(\d{1,2})\s*년`),                                                                // "경력 5년", "경력 3년~5년" (takes the first number)
	regexp.MustCompile(`(\d{1,2})\s*년\s*차\s*이상`),                                                            // "7년차 이상"
	regexp.MustCompile(`(\d{1,2})\+\s*years?`),                                                              // "5+ years"
	regexp.MustCompile(`(?i)(\d{1,2})\s*(?:-|–|—|to)\s*\d{1,2}\s*years?(?:['’]s?)?\s*(?:of\s*)?experience`), // "3-5 years of experience"
	regexp.MustCompile(`(?i)(?:at\s+least|minimum(?:\s+of)?|more\s+than|over)\s*(\d{1,2})\s*years?(?:['’]s?)?(?:\s+of)?\s+experience`),
	regexp.MustCompile(`(?i)(\d{1,2})\s*years?(?:['’]s?)?\s*(?:of\s*)?experience`), // "5 years of experience", "5 years' experience"
	regexp.MustCompile(`(?i)experience\s+of\s+(\d{1,2})\s*years?`),
}

// ExtractMinYearsExperience scans free text (title + description) for an
// overall career requirement. Explicit career phrases win over generic
// duration phrases. Among career phrases the earliest one wins: postings
// lead 자격요건 with the overall requirement ("7년 이상의 백엔드 개발 경험")
// and follow with narrower ones ("1년 이상의 커머스 도메인 경력") that must
// not lower it. The fallback keeps the smallest match for combined
// multi-role postings. This remains a best-effort heuristic for free text.
func ExtractMinYearsExperience(text string) *int {
	if years := firstMatch(text, careerYearsPatterns); years != nil {
		return years
	}
	return minimumMatch(text, yearsPatterns)
}

// firstMatch returns the years captured by whichever pattern matches
// earliest in text.
func firstMatch(text string, patterns []*regexp.Regexp) *int {
	var first *int
	firstAt := -1
	for _, re := range patterns {
		loc := re.FindStringSubmatchIndex(text)
		if loc == nil || (firstAt >= 0 && loc[0] >= firstAt) {
			continue
		}
		years, err := strconv.Atoi(text[loc[2]:loc[3]])
		if err != nil {
			continue
		}
		first, firstAt = &years, loc[0]
	}
	return first
}

func minimumMatch(text string, patterns []*regexp.Regexp) *int {
	var min *int
	for _, re := range patterns {
		for _, match := range re.FindAllStringSubmatch(text, -1) {
			years, err := strconv.Atoi(match[1])
			if err != nil {
				continue
			}
			if min == nil || years < *min {
				min = &years
			}
		}
	}
	return min
}
