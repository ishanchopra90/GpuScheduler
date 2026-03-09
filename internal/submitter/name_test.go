package submitter

import (
	"testing"
)

func TestCRNameFromRequestID(t *testing.T) {
	tests := []struct {
		requestID string
		want      string
	}{
		{"req-1", "req-1"},
		{"REQ-2", "req-2"},
		{"my.request.id", "my-request-id"},
		{"", ""},
		{"a", "a"},
		{"x_y_z", "x-y-z"},
	}
	for _, tt := range tests {
		t.Run(tt.requestID, func(t *testing.T) {
			got := CRNameFromRequestID(tt.requestID)
			if got != tt.want {
				t.Errorf("CRNameFromRequestID(%q) = %q, want %q", tt.requestID, got, tt.want)
			}
		})
	}
	// Empty after sanitize should yield hash
	got := CRNameFromRequestID("...")
	if got == "" || len(got) > 253 {
		t.Errorf("CRNameFromRequestID(\"...\") = %q (len=%d), want non-empty and <= 253 chars", got, len(got))
	}
}
