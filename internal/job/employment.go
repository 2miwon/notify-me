package job

import "strings"

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
