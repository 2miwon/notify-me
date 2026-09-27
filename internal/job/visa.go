package job

import "regexp"

// VisaSponsorship describes only what a posting explicitly says about
// employment-visa support.  NotStated is deliberately the default: a role's
// country, company, or seniority must never be used to guess sponsorship.
const (
	VisaSponsorshipSupported    = "Supported"
	VisaSponsorshipNotSupported = "Not supported"
	VisaSponsorshipNotStated    = "Not stated"
)

var visaNotSupportedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:we|the company)\s+(?:do not|does not|will not|cannot|can['’]t|are unable to)\s+(?:offer|provide|support)?\s*(?:visa|immigration)\s+sponsorship\b`),
	regexp.MustCompile(`(?i)\b(?:visa|immigration)\s+sponsorship\s+(?:is\s+)?not\s+(?:available|offered|provided)\b`),
	regexp.MustCompile(`(?i)\bmust\s+(?:already\s+)?(?:be\s+)?(?:legally\s+)?(?:authorized|eligible)\s+to\s+work\b`),
	regexp.MustCompile(`비자\s*(?:스폰서(?:십)?|지원)\s*(?:이|은)?\s*(?:불가|불가능|제공되지\s*않|지원하지\s*않)`),
	// "we do not sponsor visas", "unable to sponsor work visas", "not able to sponsor"
	regexp.MustCompile(`(?i)\b(?:do not|does not|don['’]t|will not|cannot|can['’]t|unable to|not able to|not in a position to)\s+(?:offer\s+|provide\s+)?sponsor\w*\b`),
	regexp.MustCompile(`(?i)\bno\s+(?:visa|immigration|work\s+permit)\s+sponsorship\b`),
	regexp.MustCompile(`(?i)\bwithout\s+(?:the\s+need\s+for\s+)?(?:visa\s+|employer\s+)?sponsorship\b`),
}

var visaSupportedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:visa|immigration)\s+sponsorship\s+(?:is\s+)?(?:available|provided|offered)\b`),
	regexp.MustCompile(`(?i)\b(?:we|the company)\s+(?:will|can)\s+(?:offer|provide|support)?\s*(?:visa|immigration)\s+sponsorship\b`),
	regexp.MustCompile(`(?i)\b(?:relocation\s+(?:and|&)\s+)?immigration\s+(?:support|assistance)\b`),
	regexp.MustCompile(`비자\s*(?:스폰서(?:십)?|지원)\s*(?:이|은)?\s*(?:가능|제공|지원)`),
	// "We do sponsor visas!", "we sponsor work visas", "will sponsor", "can sponsor"
	regexp.MustCompile(`(?i)\b(?:we|the company)\s+(?:do\s+|can\s+|will\s+|are\s+able\s+to\s+|are\s+happy\s+to\s+)?sponsor\w*\s+(?:work\s+|employment\s+)?(?:visas?|immigration)`),
	regexp.MustCompile(`(?i)\bvisa\s+sponsorship\s*(?:\(|:|-|–)\s*(?:yes|available|provided|we\s+do\s+sponsor)`),
	regexp.MustCompile(`(?i)\b(?:sponsorship|relocation)\s+(?:is\s+)?(?:available|provided|offered)\b`),
}

// ExtractVisaSponsorship classifies explicit source text only.  A clear
// refusal wins when contradictory boilerplate appears in the same posting;
// applicants should verify the detail page before treating any result as a
// promise from the employer.
func ExtractVisaSponsorship(text string) string {
	for _, re := range visaNotSupportedPatterns {
		if re.MatchString(text) {
			return VisaSponsorshipNotSupported
		}
	}
	for _, re := range visaSupportedPatterns {
		if re.MatchString(text) {
			return VisaSponsorshipSupported
		}
	}
	return VisaSponsorshipNotStated
}
