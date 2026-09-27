package job

import "testing"

func TestExtractVisaSponsorship(t *testing.T) {
	tests := []struct{ text, want string }{
		{"Visa sponsorship is available for qualified candidates.", VisaSponsorshipSupported},
		{"We provide relocation and immigration support.", VisaSponsorshipSupported},
		{"Applicants must already be legally authorized to work in the United States.", VisaSponsorshipNotSupported},
		{"We do not offer visa sponsorship for this position.", VisaSponsorshipNotSupported},
		{"비자 스폰서십 지원 가능", VisaSponsorshipSupported},
		{"비자 지원은 제공되지 않습니다.", VisaSponsorshipNotSupported},
		{"This role may require visa sponsorship.", VisaSponsorshipNotStated},
		{"Visa sponsorship:  We do sponsor visas! However, we aren't able to successfully sponsor visas for every role.", VisaSponsorshipSupported},
		{"We are unable to sponsor work visas for this role.", VisaSponsorshipNotSupported},
		{"We do not sponsor visas.", VisaSponsorshipNotSupported},
		{"No visa sponsorship is available.", VisaSponsorshipNotSupported},
		{"We sponsor work visas for international hires.", VisaSponsorshipSupported},
		{"Sponsorship is available.", VisaSponsorshipSupported},
		{"Please note this is not a sponsorship role for anyone else.", VisaSponsorshipNotStated},
	}
	for _, tt := range tests {
		if got := ExtractVisaSponsorship(tt.text); got != tt.want {
			t.Errorf("ExtractVisaSponsorship(%q) = %q, want %q", tt.text, got, tt.want)
		}
	}
}
