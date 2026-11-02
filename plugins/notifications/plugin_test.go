package notifications

import "testing"

func TestSkipReason(t *testing.T) {
	tests := []struct {
		reason string
		want   bool
	}{
		{"review_requested", true},
		{"assign", true},
		{"mention", false},
		{"comment", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := skipReason(tt.reason); got != tt.want {
			t.Errorf("skipReason(%q) = %v, want %v", tt.reason, got, tt.want)
		}
	}
}
