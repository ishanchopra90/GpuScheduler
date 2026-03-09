package submitter

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// maxCRNameLength is the maximum length for a Kubernetes resource name (DNS-1123 subdomain).
const maxCRNameLength = 253

// dns1123Invalid is characters not allowed in DNS-1123 subdomain; we replace with hyphen.
var dns1123Invalid = regexp.MustCompile(`[^a-z0-9-]`)

// CRNameFromRequestID derives a Kubernetes-safe resource name from a request_id.
// It lowercases, replaces invalid characters with hyphens, trims leading/trailing hyphens,
// and truncates to 253 characters. If the result is empty, a short hash of request_id is used.
func CRNameFromRequestID(requestID string) string {
	if requestID == "" {
		return ""
	}
	s := strings.ToLower(strings.TrimSpace(requestID))
	s = dns1123Invalid.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if len(s) > maxCRNameLength {
		s = s[:maxCRNameLength]
		s = strings.TrimRight(s, "-")
	}
	if s == "" {
		h := sha256.Sum256([]byte(requestID))
		s = hex.EncodeToString(h[:])[:16]
	}
	return s
}
