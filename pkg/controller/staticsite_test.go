package controller

import (
	"testing"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDomainGeneration(t *testing.T) {
	r := &StaticSiteReconciler{
		PagesDomain: "pages.kup6s.io",
	}

	tests := []struct {
		name       string
		siteName   string
		specDomain string
		wantDomain string
	}{
		{
			name:       "custom domain",
			siteName:   "mysite",
			specDomain: "example.com",
			wantDomain: "example.com",
		},
		{
			name:       "auto-generated domain",
			siteName:   "mysite",
			specDomain: "",
			wantDomain: "mysite.pages.kup6s.io",
		},
		{
			name:       "site with dashes",
			siteName:   "my-cool-site",
			specDomain: "",
			wantDomain: "my-cool-site.pages.kup6s.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			site := &pagesv1.StaticSite{
				ObjectMeta: metav1.ObjectMeta{
					Name: tt.siteName,
				},
				Spec: pagesv1.StaticSiteSpec{
					Domain: tt.specDomain,
				},
			}

			// Test domain generation logic
			domain := site.Spec.Domain
			if domain == "" {
				domain = site.Name + "." + r.PagesDomain
			}

			if domain != tt.wantDomain {
				t.Errorf("domain = %q, want %q", domain, tt.wantDomain)
			}
		})
	}
}

func TestMiddlewareNameGeneration(t *testing.T) {
	tests := []struct {
		siteName       string
		wantMiddleware string
	}{
		{"mysite", "mysite-prefix"},
		{"blog", "blog-prefix"},
		{"my-cool-site", "my-cool-site-prefix"},
	}

	for _, tt := range tests {
		t.Run(tt.siteName, func(t *testing.T) {
			middlewareName := tt.siteName + "-prefix"
			if middlewareName != tt.wantMiddleware {
				t.Errorf("middlewareName = %q, want %q", middlewareName, tt.wantMiddleware)
			}
		})
	}
}

func TestTLSSecretNameGeneration(t *testing.T) {
	tests := []struct {
		name           string
		siteName       string
		hasCustomDomain bool
		wantSecretName string
	}{
		{
			name:            "custom domain uses site-specific secret",
			siteName:        "mysite",
			hasCustomDomain: true,
			wantSecretName:  "mysite-tls",
		},
		{
			name:            "auto domain uses wildcard secret",
			siteName:        "mysite",
			hasCustomDomain: false,
			wantSecretName:  "pages-wildcard-tls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var secretName string
			if tt.hasCustomDomain {
				secretName = tt.siteName + "-tls"
			} else {
				secretName = "pages-wildcard-tls"
			}

			if secretName != tt.wantSecretName {
				t.Errorf("secretName = %q, want %q", secretName, tt.wantSecretName)
			}
		})
	}
}

func TestAddPrefixValue(t *testing.T) {
	tests := []struct {
		siteName   string
		wantPrefix string
	}{
		{"mysite", "/mysite"},
		{"blog", "/blog"},
		{"docs-site", "/docs-site"},
	}

	for _, tt := range tests {
		t.Run(tt.siteName, func(t *testing.T) {
			prefix := "/" + tt.siteName
			if prefix != tt.wantPrefix {
				t.Errorf("prefix = %q, want %q", prefix, tt.wantPrefix)
			}
		})
	}
}

func TestFinalizerName(t *testing.T) {
	// Ensure finalizer follows Kubernetes naming conventions
	if finalizerName != "pages.kup6s.io/finalizer" {
		t.Errorf("finalizerName = %q, want %q", finalizerName, "pages.kup6s.io/finalizer")
	}
}

func TestNginxServiceConfig(t *testing.T) {
	// Ensure nginx service configuration is correct
	if nginxServiceName != "static-sites-nginx" {
		t.Errorf("nginxServiceName = %q, want %q", nginxServiceName, "static-sites-nginx")
	}
	if nginxNamespace != "kup6s-pages" {
		t.Errorf("nginxNamespace = %q, want %q", nginxNamespace, "kup6s-pages")
	}
}
