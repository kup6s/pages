package v1beta1

import (
	"regexp"
	"strings"
	"testing"
)

// domainPattern mirrors the kubebuilder validation pattern for the Domain field
var domainPattern = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

func TestDomainValidation(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		isValid bool
	}{
		// Valid domains
		{name: "simple domain", domain: "example.com", isValid: true},
		{name: "subdomain", domain: "www.example.com", isValid: true},
		{name: "multi-level subdomain", domain: "sub.domain.example.com", isValid: true},
		{name: "short TLD", domain: "example.io", isValid: true},
		{name: "hyphen in label", domain: "my-site.example.com", isValid: true},
		{name: "numbers in label", domain: "site123.example.com", isValid: true},
		{name: "long TLD", domain: "example.technology", isValid: true},
		{name: "single char labels", domain: "a.b.example.com", isValid: true},
		{name: "punycode IDN", domain: "xn--nxasmq5b.com", isValid: true},

		// Invalid domains
		{name: "no TLD", domain: "example", isValid: false},
		{name: "starts with dot", domain: ".example.com", isValid: false},
		{name: "ends with dot", domain: "example.com.", isValid: false},
		{name: "starts with hyphen", domain: "-example.com", isValid: false},
		{name: "ends with hyphen", domain: "example-.com", isValid: false},
		{name: "label starts with hyphen", domain: "www.-example.com", isValid: false},
		{name: "label ends with hyphen", domain: "www.example-.com", isValid: false},
		{name: "underscore in domain", domain: "exa_mple.com", isValid: false},
		{name: "single char TLD", domain: "example.c", isValid: false},
		{name: "double dot", domain: "example..com", isValid: false},
		{name: "space in domain", domain: "exam ple.com", isValid: false},
		{name: "protocol prefix", domain: "https://example.com", isValid: false},
		{name: "path suffix", domain: "example.com/path", isValid: false},
		{name: "port suffix", domain: "example.com:8080", isValid: false},
		{name: "empty string", domain: "", isValid: false},
		{name: "only dots", domain: "...", isValid: false},
		{name: "numeric TLD", domain: "example.123", isValid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := domainPattern.MatchString(tt.domain)
			if matches != tt.isValid {
				t.Errorf("domain %q: got valid=%v, want valid=%v", tt.domain, matches, tt.isValid)
			}
		})
	}
}

func TestDomainMaxLength(t *testing.T) {
	// DNS names have a maximum length of 253 characters
	const maxDomainLength = 253

	// Create a domain that is exactly at the max length
	// Each label can be up to 63 chars, separated by dots
	labels := make([]string, 0)
	totalLen := 0
	for totalLen < maxDomainLength-10 { // leave room for TLD
		label := strings.Repeat("a", 63)
		if totalLen+64 > maxDomainLength-10 {
			remaining := maxDomainLength - 10 - totalLen - 1 // -1 for dot
			if remaining > 0 {
				label = strings.Repeat("a", remaining)
			} else {
				break
			}
		}
		labels = append(labels, label)
		totalLen += len(label) + 1 // +1 for dot
	}
	labels = append(labels, "com")

	longDomain := strings.Join(labels, ".")

	if len(longDomain) > maxDomainLength {
		t.Errorf("test setup error: domain length %d exceeds max %d", len(longDomain), maxDomainLength)
	}

	// Valid: at or below max length
	if !domainPattern.MatchString(longDomain) {
		t.Errorf("domain at max length should be valid: %d chars", len(longDomain))
	}

	// Create a domain that exceeds max length
	tooLong := strings.Repeat("a", 63) + "." + longDomain
	if len(tooLong) <= maxDomainLength {
		t.Skip("could not create domain exceeding max length for test")
	}
	// Note: The regex doesn't enforce max length, that's done by kubebuilder:validation:MaxLength
	// This test just documents the expected max length constraint
}
