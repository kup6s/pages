// Package controller contains the reconciliation logic
package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1beta1"
)

// randReader is the source of randomness for token generation (injectable for testing)
var randReader io.Reader = rand.Reader

const (
	finalizerName         = "pages.kup6s.com/finalizer"
	maxK8sResourceNameLen = 63

	// defaultTokenLength is the length of generated sync tokens
	defaultTokenLength = 32

	// statusCheckInterval is how often to requeue for status updates
	statusCheckInterval = 5 * time.Minute

	// errorRetryInterval is how long to wait before retrying after an error
	errorRetryInterval = 30 * time.Second

	// immediateRequeueDelay is a short delay for requeuing after state changes
	immediateRequeueDelay = 100 * time.Millisecond
)

// truncateK8sName truncates a name to the Kubernetes resource name length limit
// and removes any trailing hyphens that could result from the truncation.
func truncateK8sName(name string) string {
	if len(name) > maxK8sResourceNameLen {
		name = name[:maxK8sResourceNameLen]
	}
	return strings.TrimRight(name, "-")
}

// TLS mode constants
const (
	// TlsModeIndividual creates individual certificates per site using HTTP-01 challenge
	TlsModeIndividual = "individual"
	// TlsModeWildcard references a pre-existing wildcard certificate
	TlsModeWildcard = "wildcard"
)

// StaticSiteReconciler reconciles StaticSite resources
type StaticSiteReconciler struct {
	client.Client
	DynamicClient dynamic.Interface
	Recorder      events.EventRecorder

	// Config
	PagesDomain          string // e.g. "pages.kup6s.com"
	ClusterIssuer        string // e.g. "letsencrypt-prod"
	NginxNamespace       string // namespace where nginx service runs
	NginxServiceName     string // name of the nginx service (e.g. "kup6s-pages-nginx")
	PagesTlsMode         string // "individual" or "wildcard" for auto-generated domains
	PagesWildcardSecret  string // secret name when using wildcard mode (e.g. "pages-wildcard-tls")
}

// GVRs for Traefik, cert-manager, and core resources
var (
	ingressRouteGVR = schema.GroupVersionResource{
		Group:    "traefik.io",
		Version:  "v1alpha1",
		Resource: "ingressroutes",
	}
	middlewareGVR = schema.GroupVersionResource{
		Group:    "traefik.io",
		Version:  "v1alpha1",
		Resource: "middlewares",
	}
	certificateGVR = schema.GroupVersionResource{
		Group:    "cert-manager.io",
		Version:  "v1",
		Resource: "certificates",
	}
)

// sanitizeDomainForResourceName converts a domain to a valid Kubernetes resource name
// e.g., "www.example.com" -> "www-example-com"
func sanitizeDomainForResourceName(domain string) string {
	name := strings.ReplaceAll(domain, ".", "-")
	name = strings.ToLower(name)
	return truncateK8sName(name)
}

// resourceName generates a resource name prefixed with the StaticSite's namespace
// to avoid collisions when resources are created in the system namespace.
// Format: {namespace}--{name}, e.g., "customer-ns--my-site"
func resourceName(site *pagesv1.StaticSite) string {
	name := fmt.Sprintf("%s--%s", site.Namespace, site.Name)
	return truncateK8sName(name)
}

// resourceNameWithSuffix generates a resource name with namespace prefix and suffix
// Format: {namespace}--{name}-{suffix}, e.g., "customer-ns--my-site-prefix"
func resourceNameWithSuffix(site *pagesv1.StaticSite, suffix string) string {
	name := fmt.Sprintf("%s--%s-%s", site.Namespace, site.Name, suffix)
	return truncateK8sName(name)
}

// generateSecureToken creates a cryptographically secure random token
func generateSecureToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := randReader.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b)[:length], nil
}

// validatePathPrefix checks if pathPrefix configuration is valid
func validatePathPrefix(site *pagesv1.StaticSite) error {
	prefix := site.Spec.PathPrefix
	if prefix == "" {
		return nil
	}

	// PathPrefix requires a custom domain
	if site.Spec.Domain == "" {
		return fmt.Errorf("pathPrefix requires a custom domain (cannot use with auto-generated subdomain)")
	}

	// Must start with /
	if !strings.HasPrefix(prefix, "/") {
		return fmt.Errorf("pathPrefix must start with /")
	}

	// Cannot be just /
	if prefix == "/" {
		return fmt.Errorf("pathPrefix cannot be just '/' - omit the field for root path")
	}

	return nil
}

// Reconcile is the main reconciliation loop
// Called when a StaticSite changes
func (r *StaticSiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	site := &pagesv1.StaticSite{}
	if err := r.Get(ctx, req.NamespacedName, site); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling StaticSite", "name", site.Name, "domain", site.Spec.Domain)

	if !site.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, site)
	}

	if result, err := r.ensureFinalizerAndToken(ctx, site); result != nil || err != nil {
		return *result, err
	}

	if site.IsPrivate() {
		return r.reconcilePrivate(ctx, site)
	}

	if err := validatePathPrefix(site); err != nil {
		return r.setError(ctx, site, "ValidationFailed", err)
	}

	domain, err := r.determineDomain(site)
	if err != nil {
		return r.setError(ctx, site, "ValidationFailed", err)
	}

	if err := r.reconcileNetworking(ctx, site, domain); err != nil {
		return r.setError(ctx, site, "NetworkingFailed", err)
	}

	if err := r.reconcileTLS(ctx, site, domain); err != nil {
		return r.setError(ctx, site, "TLSFailed", err)
	}

	// A domain change leaves the Certificate of the old domain behind.
	if previous := statusDomain(site); previous != "" && previous != domain {
		if err := r.releaseCertificate(ctx, site, previous); err != nil {
			logger.Error(err, "Failed to release certificate of previous domain", "domain", previous)
		}
	}

	return r.updateFinalStatus(ctx, site, domain)
}

// reconcilePrivate removes everything that made the site public and records
// its in-cluster address. The syncer keeps the content in sync either way.
func (r *StaticSiteReconciler) reconcilePrivate(ctx context.Context, site *pagesv1.StaticSite) (ctrl.Result, error) {
	if site.Spec.Domain != "" || site.Spec.PathPrefix != "" {
		return r.setError(ctx, site, "ValidationFailed", fmt.Errorf("a Private site cannot have domain or pathPrefix"))
	}

	if err := r.removePublicResources(ctx, site); err != nil {
		return r.setError(ctx, site, "CleanupFailed", err)
	}

	site.Status.Phase, site.Status.Message = syncPhase(site)
	site.Status.URL = ""
	site.Status.InternalURL = r.internalURL(site)
	site.Status.Resources = nil
	meta.RemoveStatusCondition(&site.Status.Conditions, pagesv1.ConditionCertificateReady)

	if err := r.Status().Update(ctx, site); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(site, nil, "Normal", "Configured", "ConfigurePrivate", "Private site served in-cluster at %s", site.Status.InternalURL)

	return ctrl.Result{RequeueAfter: statusCheckInterval}, nil
}

// internalURL is the in-cluster address of the site behind nginx.
func (r *StaticSiteReconciler) internalURL(site *pagesv1.StaticSite) string {
	return fmt.Sprintf("http://%s.%s.svc/%s/", r.NginxServiceName, r.NginxNamespace, site.Name)
}

// ensureFinalizerAndToken adds the finalizer and generates a sync token if needed.
// Returns a non-nil result if a requeue is needed, otherwise returns nil for both.
func (r *StaticSiteReconciler) ensureFinalizerAndToken(ctx context.Context, site *pagesv1.StaticSite) (*ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(site, finalizerName) {
		controllerutil.AddFinalizer(site, finalizerName)
		if err := r.Update(ctx, site); err != nil {
			return &ctrl.Result{}, err
		}
		return &ctrl.Result{RequeueAfter: immediateRequeueDelay}, nil
	}

	if site.Status.SyncToken == "" {
		token, err := generateSecureToken(defaultTokenLength)
		if err != nil {
			result, setErr := r.setError(ctx, site, "TokenGenerationFailed", err)
			return &result, setErr
		}
		site.Status.SyncToken = token
		if err := r.Status().Update(ctx, site); err != nil {
			return &ctrl.Result{}, err
		}
		logger.Info("Generated sync token", "name", site.Name)
		return &ctrl.Result{RequeueAfter: immediateRequeueDelay}, nil
	}

	return nil, nil
}

// determineDomain returns the domain for the site (custom or auto-generated).
// Returns an error if no custom domain is set and PagesDomain is not configured.
func (r *StaticSiteReconciler) determineDomain(site *pagesv1.StaticSite) (string, error) {
	if site.Spec.Domain != "" {
		return site.Spec.Domain, nil
	}
	if r.PagesDomain == "" {
		return "", fmt.Errorf("no custom domain specified and --pages-domain not configured")
	}
	return fmt.Sprintf("%s.%s", site.Name, r.PagesDomain), nil
}

// reconcileNetworking creates or updates the Traefik Middleware and IngressRoute.
func (r *StaticSiteReconciler) reconcileNetworking(ctx context.Context, site *pagesv1.StaticSite, domain string) error {
	if err := r.reconcileMiddleware(ctx, site); err != nil {
		return err
	}
	return r.reconcileIngressRoute(ctx, site, domain)
}

// reconcileTLS creates the Certificate (if needed) and updates conditions.
// Certificates are created for:
// - Custom domains (always)
// - Auto-generated domains when PagesTlsMode is "individual" or empty (default)
func (r *StaticSiteReconciler) reconcileTLS(ctx context.Context, site *pagesv1.StaticSite, domain string) error {
	needsCertificate := site.Spec.Domain != "" || r.PagesTlsMode != TlsModeWildcard
	if needsCertificate {
		if err := r.reconcileCertificate(ctx, site, domain); err != nil {
			return err
		}
	}
	r.updateCertificateCondition(ctx, site, domain)
	return nil
}

// updateFinalStatus sets the final status fields and emits an event.
func (r *StaticSiteReconciler) updateFinalStatus(ctx context.Context, site *pagesv1.StaticSite, domain string) (ctrl.Result, error) {
	site.Status.Phase, site.Status.Message = syncPhase(site)
	site.Status.InternalURL = r.internalURL(site)
	if site.Spec.PathPrefix != "" {
		site.Status.URL = fmt.Sprintf("https://%s%s", domain, site.Spec.PathPrefix)
	} else {
		site.Status.URL = fmt.Sprintf("https://%s", domain)
	}

	site.Status.Resources = &pagesv1.ManagedResources{
		IngressRoute: fmt.Sprintf("%s/%s", r.NginxNamespace, resourceName(site)),
		Middleware:   fmt.Sprintf("%s/%s", r.NginxNamespace, resourceNameWithSuffix(site, "prefix")),
	}
	if site.Spec.PathPrefix != "" {
		site.Status.Resources.StripMiddleware = fmt.Sprintf("%s/%s", r.NginxNamespace, resourceNameWithSuffix(site, "strip"))
	}
	// Certificate resource is set when using custom domain or individual TLS mode
	hasManagedCert := site.Spec.Domain != "" || r.PagesTlsMode != TlsModeWildcard
	if hasManagedCert {
		certName := sanitizeDomainForResourceName(domain) + "-tls"
		site.Status.Resources.Certificate = fmt.Sprintf("%s/%s", r.NginxNamespace, certName)
	}

	if err := r.Status().Update(ctx, site); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(site, nil, "Normal", "Configured", "ConfigureIngress", "Site configured at %s", site.Status.URL)

	return ctrl.Result{RequeueAfter: statusCheckInterval}, nil
}

// reconcileMiddleware creates the Traefik Middlewares
func (r *StaticSiteReconciler) reconcileMiddleware(ctx context.Context, site *pagesv1.StaticSite) error {
	// Always create the addPrefix middleware
	if err := r.createAddPrefixMiddleware(ctx, site); err != nil {
		return err
	}

	// If pathPrefix is set, also create stripPrefix middleware
	if site.Spec.PathPrefix != "" {
		if err := r.createStripPrefixMiddleware(ctx, site); err != nil {
			return err
		}
	} else if site.Status.Resources != nil && site.Status.Resources.StripMiddleware != "" {
		// The prefix was removed, so its stripPrefix middleware goes too
		if err := r.deleteIfExists(ctx, middlewareGVR, "Middleware", resourceNameWithSuffix(site, "strip")); err != nil {
			return err
		}
	}

	return nil
}

// createAddPrefixMiddleware creates the middleware that adds /<sitename> prefix for nginx routing
// Created in the system namespace with namespace-prefixed name for isolation
func (r *StaticSiteReconciler) createAddPrefixMiddleware(ctx context.Context, site *pagesv1.StaticSite) error {
	middleware := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]interface{}{
				"name":      resourceNameWithSuffix(site, "prefix"),
				"namespace": r.NginxNamespace,
				"labels": map[string]interface{}{
					"pages.kup6s.com/managed":        "true",
					"pages.kup6s.com/site-name":      site.Name,
					"pages.kup6s.com/site-namespace": site.Namespace,
				},
			},
			"spec": map[string]interface{}{
				"addPrefix": map[string]interface{}{
					"prefix": "/" + site.Name,
				},
			},
		},
	}

	return r.createOrUpdateMiddleware(ctx, middleware)
}

// createStripPrefixMiddleware creates the middleware that strips the pathPrefix from requests
// Created in the system namespace with namespace-prefixed name for isolation
func (r *StaticSiteReconciler) createStripPrefixMiddleware(ctx context.Context, site *pagesv1.StaticSite) error {
	middleware := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]interface{}{
				"name":      resourceNameWithSuffix(site, "strip"),
				"namespace": r.NginxNamespace,
				"labels": map[string]interface{}{
					"pages.kup6s.com/managed":        "true",
					"pages.kup6s.com/site-name":      site.Name,
					"pages.kup6s.com/site-namespace": site.Namespace,
				},
			},
			"spec": map[string]interface{}{
				"stripPrefix": map[string]interface{}{
					"prefixes": []interface{}{site.Spec.PathPrefix},
				},
			},
		},
	}

	return r.createOrUpdateMiddleware(ctx, middleware)
}

// createOrUpdateMiddleware is a helper to create or update a Traefik Middleware
func (r *StaticSiteReconciler) createOrUpdateMiddleware(ctx context.Context, middleware *unstructured.Unstructured) error {
	logger := log.FromContext(ctx)

	existing, err := r.DynamicClient.Resource(middlewareGVR).Namespace(middleware.GetNamespace()).Get(ctx, middleware.GetName(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Middleware", "name", middleware.GetName())
			_, err = r.DynamicClient.Resource(middlewareGVR).Namespace(middleware.GetNamespace()).Create(ctx, middleware, metav1.CreateOptions{})
			return err
		}
		return err
	}

	middleware.SetResourceVersion(existing.GetResourceVersion())
	logger.Info("Updating Middleware", "name", middleware.GetName())
	_, err = r.DynamicClient.Resource(middlewareGVR).Namespace(middleware.GetNamespace()).Update(ctx, middleware, metav1.UpdateOptions{})
	return err
}

// reconcileIngressRoute creates the Traefik IngressRoute in the system namespace
func (r *StaticSiteReconciler) reconcileIngressRoute(ctx context.Context, site *pagesv1.StaticSite, domain string) error {
	logger := log.FromContext(ctx)

	// Build match rule - include PathPrefix when set
	var matchRule string
	if site.Spec.PathPrefix != "" {
		matchRule = fmt.Sprintf("Host(`%s`) && PathPrefix(`%s`)", domain, site.Spec.PathPrefix)
	} else {
		matchRule = fmt.Sprintf("Host(`%s`)", domain)
	}

	// Build middlewares list - chain strip + prefix when pathPrefix is set
	// All middlewares are in the system namespace now
	var middlewares []interface{}
	if site.Spec.PathPrefix != "" {
		// Strip first, then add - ORDER MATTERS
		middlewares = []interface{}{
			map[string]interface{}{
				"name":      resourceNameWithSuffix(site, "strip"),
				"namespace": r.NginxNamespace,
			},
			map[string]interface{}{
				"name":      resourceNameWithSuffix(site, "prefix"),
				"namespace": r.NginxNamespace,
			},
		}
	} else {
		middlewares = []interface{}{
			map[string]interface{}{
				"name":      resourceNameWithSuffix(site, "prefix"),
				"namespace": r.NginxNamespace,
			},
		}
	}

	// TLS Config - use domain-based naming for custom domains, configurable for auto-generated
	tlsConfig := map[string]interface{}{}
	if site.Spec.Domain != "" {
		// Custom domain - always individual cert
		tlsConfig["secretName"] = sanitizeDomainForResourceName(domain) + "-tls"
	} else {
		// Auto-generated domain - configurable TLS mode
		if r.PagesTlsMode == TlsModeWildcard {
			// Use pre-existing wildcard certificate
			tlsConfig["secretName"] = r.PagesWildcardSecret
		} else {
			// Individual cert for {site}.pages.kup6s.com (default behavior)
			tlsConfig["secretName"] = sanitizeDomainForResourceName(domain) + "-tls"
		}
	}

	irName := resourceName(site)
	ingressRoute := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "IngressRoute",
			"metadata": map[string]interface{}{
				"name":      irName,
				"namespace": r.NginxNamespace,
				"labels": map[string]interface{}{
					"pages.kup6s.com/managed":        "true",
					"pages.kup6s.com/site-name":      site.Name,
					"pages.kup6s.com/site-namespace": site.Namespace,
				},
			},
			"spec": map[string]interface{}{
				"entryPoints": []interface{}{"websecure"},
				"routes": []interface{}{
					map[string]interface{}{
						"match":       matchRule,
						"kind":        "Rule",
						"middlewares": middlewares,
						"services": []interface{}{
							map[string]interface{}{
								// Reference nginx directly - same namespace now
								"name":      r.NginxServiceName,
								"namespace": r.NginxNamespace,
								"port":      int64(80),
							},
						},
					},
				},
				"tls": tlsConfig,
			},
		},
	}

	// Create or Update
	existing, err := r.DynamicClient.Resource(ingressRouteGVR).Namespace(r.NginxNamespace).Get(ctx, irName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating IngressRoute", "name", irName, "match", matchRule)
			_, err = r.DynamicClient.Resource(ingressRouteGVR).Namespace(r.NginxNamespace).Create(ctx, ingressRoute, metav1.CreateOptions{})
			return err
		}
		return err
	}

	ingressRoute.SetResourceVersion(existing.GetResourceVersion())
	logger.Info("Updating IngressRoute", "name", irName, "match", matchRule)
	_, err = r.DynamicClient.Resource(ingressRouteGVR).Namespace(r.NginxNamespace).Update(ctx, ingressRoute, metav1.UpdateOptions{})
	return err
}

// reconcileCertificate creates a cert-manager Certificate in the system namespace
// Certificates are named by domain so multiple sites can share them
func (r *StaticSiteReconciler) reconcileCertificate(ctx context.Context, site *pagesv1.StaticSite, domain string) error {
	logger := log.FromContext(ctx)

	// Certificate name is based on domain, not site name (for sharing)
	certName := sanitizeDomainForResourceName(domain) + "-tls"

	certificate := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]interface{}{
				"name":      certName,
				"namespace": r.NginxNamespace,
				// No ownerReferences - certificate is shared across sites
				"labels": map[string]interface{}{
					"pages.kup6s.com/managed": "true",
					"pages.kup6s.com/domain":  domain,
				},
			},
			"spec": map[string]interface{}{
				"secretName": certName,
				"dnsNames":   []interface{}{domain},
				"issuerRef": map[string]interface{}{
					"name": r.ClusterIssuer,
					"kind": "ClusterIssuer",
				},
			},
		},
	}

	_, err := r.DynamicClient.Resource(certificateGVR).Namespace(r.NginxNamespace).Get(ctx, certName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Certificate", "name", certName, "domain", domain)
			_, err = r.DynamicClient.Resource(certificateGVR).Namespace(r.NginxNamespace).Create(ctx, certificate, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// Certificate already exists - no update needed (it's shared)
	logger.V(1).Info("Certificate already exists", "name", certName)
	return nil
}

// updateCertificateCondition reads the Certificate status and updates the StaticSite condition
func (r *StaticSiteReconciler) updateCertificateCondition(ctx context.Context, site *pagesv1.StaticSite, domain string) {
	logger := log.FromContext(ctx)

	// Check if this site has a managed certificate
	hasManagedCert := site.Spec.Domain != "" || r.PagesTlsMode != TlsModeWildcard
	if !hasManagedCert {
		// Wildcard mode with auto-generated domain - no certificate to check
		meta.RemoveStatusCondition(&site.Status.Conditions, pagesv1.ConditionCertificateReady)
		return
	}

	certName := sanitizeDomainForResourceName(domain) + "-tls"

	cert, err := r.DynamicClient.Resource(certificateGVR).Namespace(r.NginxNamespace).Get(ctx, certName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			meta.SetStatusCondition(&site.Status.Conditions, metav1.Condition{
				Type:               pagesv1.ConditionCertificateReady,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: site.Generation,
				Reason:             "CertificateNotFound",
				Message:            fmt.Sprintf("Certificate %s not found", certName),
			})
			return
		}
		logger.Error(err, "Failed to get Certificate", "name", certName)
		meta.SetStatusCondition(&site.Status.Conditions, metav1.Condition{
			Type:               pagesv1.ConditionCertificateReady,
			Status:             metav1.ConditionUnknown,
			ObservedGeneration: site.Generation,
			Reason:             "CertificateFetchError",
			Message:            err.Error(),
		})
		return
	}

	// Extract conditions from Certificate status
	status, found, err := unstructured.NestedMap(cert.Object, "status")
	if err != nil || !found {
		meta.SetStatusCondition(&site.Status.Conditions, metav1.Condition{
			Type:               pagesv1.ConditionCertificateReady,
			Status:             metav1.ConditionUnknown,
			ObservedGeneration: site.Generation,
			Reason:             "StatusNotAvailable",
			Message:            "Certificate status not available yet",
		})
		return
	}

	conditions, found, err := unstructured.NestedSlice(status, "conditions")
	if err != nil || !found {
		meta.SetStatusCondition(&site.Status.Conditions, metav1.Condition{
			Type:               pagesv1.ConditionCertificateReady,
			Status:             metav1.ConditionUnknown,
			ObservedGeneration: site.Generation,
			Reason:             "ConditionsNotAvailable",
			Message:            "Certificate conditions not available yet",
		})
		return
	}

	// Find the Ready condition in the Certificate
	for _, c := range conditions {
		cond, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		condType, _, _ := unstructured.NestedString(cond, "type")
		if condType != "Ready" {
			continue
		}

		condStatus, _, _ := unstructured.NestedString(cond, "status")
		condReason, _, _ := unstructured.NestedString(cond, "reason")
		condMessage, _, _ := unstructured.NestedString(cond, "message")

		var status metav1.ConditionStatus
		switch condStatus {
		case "True":
			status = metav1.ConditionTrue
		case "False":
			status = metav1.ConditionFalse
		default:
			status = metav1.ConditionUnknown
		}

		meta.SetStatusCondition(&site.Status.Conditions, metav1.Condition{
			Type:               pagesv1.ConditionCertificateReady,
			Status:             status,
			ObservedGeneration: site.Generation,
			Reason:             condReason,
			Message:            condMessage,
		})
		return
	}

	// Ready condition not found
	meta.SetStatusCondition(&site.Status.Conditions, metav1.Condition{
		Type:               pagesv1.ConditionCertificateReady,
		Status:             metav1.ConditionUnknown,
		ObservedGeneration: site.Generation,
		Reason:             "ReadyConditionNotFound",
		Message:            "Certificate Ready condition not found",
	})
}

// handleDeletion cleans up on deletion
// Since resources are in the system namespace, we must explicitly delete them
func (r *StaticSiteReconciler) handleDeletion(ctx context.Context, site *pagesv1.StaticSite) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(site, finalizerName) {
		logger.Info("Cleaning up StaticSite", "name", site.Name)

		if err := r.removePublicResources(ctx, site); err != nil {
			// Don't block deletion for this
			logger.Error(err, "Failed to remove public resources")
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(site, finalizerName)
		if err := r.Update(ctx, site); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// removePublicResources deletes the IngressRoute, both Middlewares and the
// Certificate of the domain the site was published at. It goes by resource
// names and status rather than by the spec, because the spec may no longer
// say what was created (a removed pathPrefix, a site that turned private).
func (r *StaticSiteReconciler) removePublicResources(ctx context.Context, site *pagesv1.StaticSite) error {
	var errs []error

	if err := r.deleteIfExists(ctx, ingressRouteGVR, "IngressRoute", resourceName(site)); err != nil {
		errs = append(errs, err)
	}
	for _, suffix := range []string{"prefix", "strip"} {
		if err := r.deleteIfExists(ctx, middlewareGVR, "Middleware", resourceNameWithSuffix(site, suffix)); err != nil {
			errs = append(errs, err)
		}
	}

	// Certificates are shared - cleanup only if no other sites use this domain
	if domain := r.publishedDomain(site); domain != "" {
		if err := r.releaseCertificate(ctx, site, domain); err != nil {
			errs = append(errs, err)
		}
	}

	return stderrors.Join(errs...)
}

// deleteIfExists deletes a resource in the system namespace; a missing one counts as deleted.
func (r *StaticSiteReconciler) deleteIfExists(ctx context.Context, gvr schema.GroupVersionResource, kind, name string) error {
	err := r.DynamicClient.Resource(gvr).Namespace(r.NginxNamespace).Delete(ctx, name, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete %s %s: %w", kind, name, err)
	}
	log.FromContext(ctx).Info("Deleted "+kind, "name", name)
	return nil
}

// statusDomain returns the host the site was last published at, or "".
func statusDomain(site *pagesv1.StaticSite) string {
	u, err := url.Parse(site.Status.URL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// publishedDomain returns the domain the site was last published at. A
// public site that never got that far may still have created resources
// for the domain its spec asks for.
func (r *StaticSiteReconciler) publishedDomain(site *pagesv1.StaticSite) string {
	if domain := statusDomain(site); domain != "" {
		return domain
	}
	if site.IsPrivate() {
		return ""
	}
	domain, _ := r.determineDomain(site)
	return domain
}

// certificateDomain returns the domain of the Certificate the site needs, or
// "" if it needs none: private sites, and auto-generated domains in wildcard mode.
func (r *StaticSiteReconciler) certificateDomain(site *pagesv1.StaticSite) string {
	switch {
	case site.IsPrivate():
		return ""
	case site.Spec.Domain != "":
		return site.Spec.Domain
	case r.PagesTlsMode == TlsModeWildcard || r.PagesDomain == "":
		return ""
	default:
		return fmt.Sprintf("%s.%s", site.Name, r.PagesDomain)
	}
}

// releaseCertificate deletes the Certificate for domain unless another site still needs it
func (r *StaticSiteReconciler) releaseCertificate(ctx context.Context, releasing *pagesv1.StaticSite, domain string) error {
	logger := log.FromContext(ctx)

	siteList := &pagesv1.StaticSiteList{}
	if err := r.List(ctx, siteList); err != nil {
		return err
	}

	for i := range siteList.Items {
		site := &siteList.Items[i]
		if site.Name == releasing.Name && site.Namespace == releasing.Namespace {
			continue
		}
		if !site.DeletionTimestamp.IsZero() {
			continue
		}
		if r.certificateDomain(site) == domain {
			logger.V(1).Info("Certificate still in use", "domain", domain, "usedBy", site.Name)
			return nil
		}
	}

	return r.deleteIfExists(ctx, certificateGVR, "Certificate", sanitizeDomainForResourceName(domain)+"-tls")
}

// setError sets the error status and returns a Result
func (r *StaticSiteReconciler) setError(ctx context.Context, site *pagesv1.StaticSite, reason string, err error) (ctrl.Result, error) {
	site.Status.Phase = pagesv1.PhaseError
	site.Status.Message = err.Error()
	
	r.Recorder.Eventf(site, nil, "Warning", reason, "ReconcileError", "%s", err.Error())
	
	if updateErr := r.Status().Update(ctx, site); updateErr != nil {
		return ctrl.Result{}, updateErr
	}
	
	return ctrl.Result{RequeueAfter: errorRetryInterval}, nil
}

// SetupWithManager registers the controller
func (r *StaticSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create an unstructured object for Certificate watching
	cert := &unstructured.Unstructured{}
	cert.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&pagesv1.StaticSite{}).
		WatchesRawSource(source.Kind(mgr.GetCache(), cert,
			handler.TypedEnqueueRequestsFromMapFunc(r.mapCertificateToStaticSites))).
		Complete(r)
}

// mapCertificateToStaticSites maps a Certificate to the StaticSite(s) that use it
func (r *StaticSiteReconciler) mapCertificateToStaticSites(ctx context.Context, cert *unstructured.Unstructured) []reconcile.Request {
	logger := log.FromContext(ctx)

	// Only process certificates in our namespace with our label
	if cert.GetNamespace() != r.NginxNamespace {
		return nil
	}

	labels := cert.GetLabels()
	if labels == nil || labels["pages.kup6s.com/managed"] != "true" {
		return nil
	}

	domain := labels["pages.kup6s.com/domain"]
	if domain == "" {
		return nil
	}

	// Find all StaticSites using this domain
	siteList := &pagesv1.StaticSiteList{}
	if err := r.List(ctx, siteList); err != nil {
		logger.Error(err, "Failed to list StaticSites")
		return nil
	}

	var requests []reconcile.Request
	for _, site := range siteList.Items {
		if r.certificateDomain(&site) == domain {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      site.Name,
					Namespace: site.Namespace,
				},
			})
		}
	}

	if len(requests) > 0 {
		logger.V(1).Info("Certificate changed, requeueing StaticSites", "domain", domain, "count", len(requests))
	}

	return requests
}

// syncPhase derives phase and message from the syncer-owned Synced condition.
func syncPhase(site *pagesv1.StaticSite) (pagesv1.Phase, string) {
	synced := meta.FindStatusCondition(site.Status.Conditions, pagesv1.ConditionSynced)
	switch {
	case synced == nil:
		return pagesv1.PhasePending, "Waiting for first sync"
	case synced.Status == metav1.ConditionTrue:
		return pagesv1.PhaseReady, "Synced " + site.Status.LastCommit
	case synced.Status == metav1.ConditionFalse:
		return pagesv1.PhaseError, fmt.Sprintf("Sync failed: %s: %s", synced.Reason, synced.Message)
	default:
		return pagesv1.PhaseSyncing, "Syncing since " + synced.LastTransitionTime.UTC().Format(time.RFC3339)
	}
}
