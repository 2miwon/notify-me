package job

import (
	"regexp"
	"strings"
)

// Canonical academic-degree values. These intentionally describe the minimum
// degree a posting asks for, rather than an applicant's highest qualification.
const (
	DegreeHighSchool = "High School"
	DegreeAssociate  = "Associate"
	DegreeBachelor   = "Bachelor"
	DegreeMaster     = "Master"
	DegreeDoctorate  = "Doctorate"
)

type degreePattern struct {
	degree string
	re     *regexp.Regexp
}

// Match only degree language used as a qualification ("학사 이상", "Master's
// degree required"), not every incidental mention of a degree. Like the
// experience parser, this is deliberately a best-effort heuristic.
var degreePatterns = []degreePattern{
	{DegreeHighSchool, regexp.MustCompile(`(?:고졸|고등학교)\s*(?:졸업|이상|학력|필수)`)},
	{DegreeAssociate, regexp.MustCompile(`(?:전문학사|전문대)\s*(?:졸업|이상|학위|필수)`)},
	{DegreeBachelor, regexp.MustCompile(`(?:학사|대졸)\s*(?:학위|졸업|이상|또는|필수|학력)`)},
	{DegreeMaster, regexp.MustCompile(`석사\s*(?:학위|졸업|이상|또는|필수|학력)`)},
	{DegreeDoctorate, regexp.MustCompile(`박사\s*(?:학위|졸업|이상|또는|필수|학력)`)},
	{DegreeBachelor, regexp.MustCompile(`(?i)bachelor(?:['’]s)?\s+(?:degree|required|or\s+higher|or\s+above)`)},
	{DegreeMaster, regexp.MustCompile(`(?i)master(?:['’]s)?\s+(?:degree|required|or\s+higher|or\s+above)`)},
	{DegreeDoctorate, regexp.MustCompile(`(?i)(?:doctorate|ph\.?d\.?)\s*(?:degree|required|or\s+higher|or\s+above)`)},
	{DegreeBachelor, regexp.MustCompile(`(?i)bachelor(?:['’]s)?\s+of\s+(?:science|arts)`)},
	{DegreeMaster, regexp.MustCompile(`(?i)master(?:['’]s)?\s+of\s+(?:science|arts)`)},
	{DegreeDoctorate, regexp.MustCompile(`(?i)doctor\s+of\s+philosophy`)},
	{DegreeBachelor, regexp.MustCompile(`(?i)\b(?:b\.?a\.?|b\.?s\.?|b\.sc\.?)\s*(?:degree|in\b|or\b|/|,|$)`)},
	{DegreeMaster, regexp.MustCompile(`(?i)\b(?:m\.?a\.?|m\.?s\.?|m\.sc\.?)\s*(?:degree|in\b|or\b|/|,|$)`)},
	{DegreeDoctorate, regexp.MustCompile(`(?i)\bph\.?d\.?\s*(?:in\b|or\b|/|,|$)`)},
	{DegreeBachelor, regexp.MustCompile(`(?i)undergraduate\s+degree`)},
	{DegreeMaster, regexp.MustCompile(`(?i)(?:graduate|advanced)\s+degree`)},
}

var degreeRank = map[string]int{
	DegreeHighSchool: 0,
	DegreeAssociate:  1,
	DegreeBachelor:   2,
	DegreeMaster:     3,
	DegreeDoctorate:  4,
}

// ExtractMinimumDegree returns the least restrictive recognized degree in a
// posting. "학사 또는 석사" therefore returns Bachelor, which is the useful
// value when filtering roles by their minimum requirement. It returns an
// empty string when the posting gives no clear degree requirement.
func ExtractMinimumDegree(text string) string {
	minimum := ""
	for _, pattern := range degreePatterns {
		if !pattern.re.MatchString(text) {
			continue
		}
		if minimum == "" || degreeRank[pattern.degree] < degreeRank[minimum] {
			minimum = pattern.degree
		}
	}
	return minimum
}

// NormalizeDegree makes the config accept the canonical English labels as
// well as common Korean shorthand without leaking source-specific labels into
// filter configuration.
func NormalizeDegree(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "high school", "고졸", "고등학교":
		return DegreeHighSchool
	case "associate", "전문학사", "전문대":
		return DegreeAssociate
	case "bachelor", "학사":
		return DegreeBachelor
	case "master", "석사":
		return DegreeMaster
	case "doctorate", "phd", "ph.d.", "박사":
		return DegreeDoctorate
	default:
		return strings.TrimSpace(value)
	}
}
