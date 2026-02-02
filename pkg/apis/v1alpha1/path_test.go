package v1alpha1

import (
	"regexp"
	"testing"
)

// pathPattern mirrors the kubebuilder validation pattern for the Path field
// Each segment must contain at least one non-dot character to prevent ".." traversal
var pathPattern = regexp.MustCompile(`^(/[a-zA-Z0-9._-]*[a-zA-Z0-9_-][a-zA-Z0-9._-]*)*/?$`)

func TestPathValidation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		isValid bool
	}{
		// Valid paths
		{name: "root path", path: "/", isValid: true},
		{name: "empty string", path: "", isValid: true},
		{name: "simple subpath", path: "/dist", isValid: true},
		{name: "nested subpath", path: "/public/html", isValid: true},
		{name: "deeply nested", path: "/a/b/c/d/e", isValid: true},
		{name: "path with dots", path: "/dist.out", isValid: true},
		{name: "path with underscore", path: "/my_dist", isValid: true},
		{name: "path with hyphen", path: "/my-dist", isValid: true},
		{name: "path with numbers", path: "/dist123", isValid: true},
		{name: "trailing slash", path: "/dist/", isValid: true},
		{name: "nested trailing slash", path: "/public/html/", isValid: true},
		{name: "hidden directory", path: "/.vuepress", isValid: true},
		{name: "hidden nested", path: "/.next/static", isValid: true},
		{name: "dot in middle", path: "/dist.d/out", isValid: true},

		// Invalid paths - directory traversal
		{name: "parent traversal", path: "/..", isValid: false},
		{name: "traversal at start", path: "/../etc", isValid: false},
		{name: "traversal in middle", path: "/foo/../bar", isValid: false},
		{name: "traversal at end", path: "/foo/..", isValid: false},
		{name: "double dot only", path: "..", isValid: false},
		{name: "relative traversal", path: "../etc", isValid: false},
		{name: "single dot segment", path: "/./foo", isValid: false},
		{name: "dot only segment", path: "/.", isValid: false},

		// Invalid paths - other issues
		{name: "relative path", path: "dist", isValid: false},
		{name: "double slash", path: "//dist", isValid: false},
		{name: "space in path", path: "/my dist", isValid: false},
		{name: "special chars", path: "/dist@1.0", isValid: false},
		{name: "backslash", path: "/dist\\sub", isValid: false},
		{name: "colon", path: "/dist:latest", isValid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := pathPattern.MatchString(tt.path)
			if matches != tt.isValid {
				t.Errorf("path %q: got valid=%v, want valid=%v", tt.path, matches, tt.isValid)
			}
		})
	}
}
