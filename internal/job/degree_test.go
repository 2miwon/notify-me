package job

import "testing"

func TestExtractMinimumDegree(t *testing.T) {
	tests := []struct{ text, want string }{
		{"관련 분야 학사 학위 이상 보유하신 분", DegreeBachelor},
		{"대졸 이상이신 분", DegreeBachelor},
		{"석사 또는 박사 학위 필수", DegreeMaster},
		{"Master's degree or higher required", DegreeMaster},
		{"Bachelor’s degree in Computer Science or equivalent practical experience", DegreeBachelor},
		{"B.S. or M.S. degree in a related field", DegreeBachelor},
		{"Ph.D. in computer science, machine learning, or a related field", DegreeDoctorate},
		{"Master of Science in Computer Science or a related field", DegreeMaster},
		{"Doctor of Philosophy in a quantitative discipline", DegreeDoctorate},
		{"An advanced degree is preferred", DegreeMaster},
		{"학력 무관", ""},
	}
	for _, test := range tests {
		if got := ExtractMinimumDegree(test.text); got != test.want {
			t.Errorf("ExtractMinimumDegree(%q) = %q, want %q", test.text, got, test.want)
		}
	}
}
