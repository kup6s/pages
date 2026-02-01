// Package controller contains the reconciliation logic
package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1alpha1"
)

const (
	finalizerName = "pages.kup6s.com/finalizer"

	// nginxProxyServiceName is the name of the ExternalName service created
	// in each StaticSite's namespace to enable cross-namespace access to nginx
	nginxProxyServiceName = "pages-nginx-proxy"
)

// StaticSiteReconciler reconciles StaticSite resources
type StaticSiteReconciler struct {
	client.Client
	DynamicClient dynamic.Interface
	Recorder      record.EventRecorder

	// Config
	PagesDomain      string // e.g. "pages.kup6s.com"
	ClusterIssuer    string // e.g. "letsencrypt-prod"
	NginxNamespace   string // namespace where nginx service runs
	NginxServiceName string // name of the nginx service (e.g. "kup6s-pages-nginx")
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
	serviceGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "services",
	}
)

// sanitizeDomainForResourceName converts a domain to a valid Kubernetes resource name
// e.g., "www.example.com" -> "www-example-com"
func sanitizeDomainForResourceName(domain string) string {
	name := strings.ReplaceAll(domain, ".", "-")
	name = strings.ToLower(name)
	// Truncate to 63 characters (Kubernetes name limit)
	if len(name) > 63 {
		name = name[:63]
	}
	name = strings.TrimRight(name, "-")
	return name
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

	// 1. Load StaticSite
	site := &pagesv1.StaticSite{}
	if err := r.Get(ctx, req.NamespacedName, site); err != nil {
		if errors.IsNotFound(err) {
			// Was deleted, nothing to do (Finalizer cleaned up)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling StaticSite", "name", site.Name, "domain", site.Spec.Domain)

	// 2. Deletion handling
	if !site.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, site)
	}

	// 3. Add finalizer if not present
	if !controllerutil.ContainsFinalizer(site, finalizerName) {
		controllerutil.AddFinalizer(site, finalizerName)
		if err := r.Update(ctx, site); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 4. Validate pathPrefix
	if err := validatePathPrefix(site); err != nil {
		return r.setError(ctx, site, "ValidationFailed", err)
	}

	// 5. Create/update nginx proxy service (for cross-namespace access)
	if err := r.reconcileNginxProxyService(ctx, site); err != nil {
		return r.setError(ctx, site, "NginxProxyFailed", err)
	}

	// 6. Determine domain (custom or generated)
	domain := site.Spec.Domain
	if domain == "" {
		domain = fmt.Sprintf("%s.%s", site.Name, r.PagesDomain)
	}

	// 7. Create/update Middlewares (stripPrefix + addPrefix)
	if err := r.reconcileMiddleware(ctx, site); err != nil {
		return r.setError(ctx, site, "MiddlewareFailed", err)
	}

	// 8. Create/update IngressRoute
	if err := r.reconcileIngressRoute(ctx, site, domain); err != nil {
		return r.setError(ctx, site, "IngressFailed", err)
	}

	// 9. Create Certificate (if custom domain)
	if site.Spec.Domain != "" {
		if err := r.reconcileCertificate(ctx, site, domain); err != nil {
			return r.setError(ctx, site, "CertificateFailed", err)
		}
	}

	// 10. Update status
	site.Status.Phase = pagesv1.PhaseReady
	site.Status.Message = "Site configured, waiting for sync"
	if site.Spec.PathPrefix != "" {
		site.Status.URL = fmt.Sprintf("https://%s%s", domain, site.Spec.PathPrefix)
	} else {
		site.Status.URL = fmt.Sprintf("https://%s", domain)
	}
	
	if err := r.Status().Update(ctx, site); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Event(site, "Normal", "Configured", fmt.Sprintf("Site configured at %s", site.Status.URL))

	// Requeue after 5 minutes for status check
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
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
	}

	return nil
}

// createAddPrefixMiddleware creates the middleware that adds /<sitename> prefix for nginx routing
func (r *StaticSiteReconciler) createAddPrefixMiddleware(ctx context.Context, site *pagesv1.StaticSite) error {
	middleware := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]interface{}{
				"name":      site.Name + "-prefix",
				"namespace": site.Namespace,
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         pagesv1.GroupVersion.String(),
						"kind":               "StaticSite",
						"name":               site.Name,
						"uid":                string(site.UID),
						"controller":         true,
						"blockOwnerDeletion": true,
					},
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
func (r *StaticSiteReconciler) createStripPrefixMiddleware(ctx context.Context, site *pagesv1.StaticSite) error {
	middleware := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "Middleware",
			"metadata": map[string]interface{}{
				"name":      site.Name + "-strip",
				"namespace": site.Namespace,
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         pagesv1.GroupVersion.String(),
						"kind":               "StaticSite",
						"name":               site.Name,
						"uid":                string(site.UID),
						"controller":         true,
						"blockOwnerDeletion": true,
					},
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

// reconcileIngressRoute creates the Traefik IngressRoute
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
	var middlewares []interface{}
	if site.Spec.PathPrefix != "" {
		// Strip first, then add - ORDER MATTERS
		middlewares = []interface{}{
			map[string]interface{}{
				"name":      site.Name + "-strip",
				"namespace": site.Namespace,
			},
			map[string]interface{}{
				"name":      site.Name + "-prefix",
				"namespace": site.Namespace,
			},
		}
	} else {
		middlewares = []interface{}{
			map[string]interface{}{
				"name":      site.Name + "-prefix",
				"namespace": site.Namespace,
			},
		}
	}

	// TLS Config - use domain-based naming for certificate sharing
	tlsConfig := map[string]interface{}{}
	if site.Spec.Domain != "" {
		tlsConfig["secretName"] = sanitizeDomainForResourceName(domain) + "-tls"
	} else {
		// Wildcard cert for *.pages.kup6s.com
		tlsConfig["secretName"] = "pages-wildcard-tls"
	}

	ingressRoute := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "traefik.io/v1alpha1",
			"kind":       "IngressRoute",
			"metadata": map[string]interface{}{
				"name":      site.Name,
				"namespace": site.Namespace,
				"ownerReferences": []interface{}{
					map[string]interface{}{
						"apiVersion":         pagesv1.GroupVersion.String(),
						"kind":               "StaticSite",
						"name":               site.Name,
						"uid":                string(site.UID),
						"controller":         true,
						"blockOwnerDeletion": true,
					},
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
								"name":      nginxProxyServiceName,
								"namespace": site.Namespace,
								"port":      80,
							},
						},
					},
				},
				"tls": tlsConfig,
			},
		},
	}

	// Create or Update
	existing, err := r.DynamicClient.Resource(ingressRouteGVR).Namespace(site.Namespace).Get(ctx, ingressRoute.GetName(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating IngressRoute", "name", ingressRoute.GetName(), "match", matchRule)
			_, err = r.DynamicClient.Resource(ingressRouteGVR).Namespace(site.Namespace).Create(ctx, ingressRoute, metav1.CreateOptions{})
			return err
		}
		return err
	}

	ingressRoute.SetResourceVersion(existing.GetResourceVersion())
	logger.Info("Updating IngressRoute", "name", ingressRoute.GetName(), "match", matchRule)
	_, err = r.DynamicClient.Resource(ingressRouteGVR).Namespace(site.Namespace).Update(ctx, ingressRoute, metav1.UpdateOptions{})
	return err
}

// reconcileCertificate creates a cert-manager Certificate
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
				"namespace": site.Namespace,
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

	_, err := r.DynamicClient.Resource(certificateGVR).Namespace(site.Namespace).Get(ctx, certName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Certificate", "name", certName, "domain", domain)
			_, err = r.DynamicClient.Resource(certificateGVR).Namespace(site.Namespace).Create(ctx, certificate, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// Certificate already exists - no update needed (it's shared)
	logger.V(1).Info("Certificate already exists", "name", certName)
	return nil
}

// reconcileNginxProxyService creates an ExternalName Service in the StaticSite's namespace
// that points to the actual nginx service in the system namespace.
// This enables cross-namespace service access for Traefik IngressRoutes.
func (r *StaticSiteReconciler) reconcileNginxProxyService(ctx context.Context, site *pagesv1.StaticSite) error {
	logger := log.FromContext(ctx)

	// Build the full DNS name for the nginx service
	// Format: <service-name>.<namespace>.svc.cluster.local
	externalName := fmt.Sprintf("%s.%s.svc.cluster.local", r.NginxServiceName, r.NginxNamespace)

	service := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      nginxProxyServiceName,
				"namespace": site.Namespace,
				// No ownerReferences - service is shared across StaticSites in the namespace
				"labels": map[string]interface{}{
					"pages.kup6s.com/managed": "true",
					"pages.kup6s.com/type":    "nginx-proxy",
				},
			},
			"spec": map[string]interface{}{
				"type":         "ExternalName",
				"externalName": externalName,
			},
		},
	}

	existing, err := r.DynamicClient.Resource(serviceGVR).Namespace(site.Namespace).Get(ctx, nginxProxyServiceName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating nginx proxy Service", "name", nginxProxyServiceName, "externalName", externalName)
			_, err = r.DynamicClient.Resource(serviceGVR).Namespace(site.Namespace).Create(ctx, service, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// Check if externalName needs updating (in case config changed)
	spec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	existingExternalName, _, _ := unstructured.NestedString(spec, "externalName")
	if existingExternalName != externalName {
		service.SetResourceVersion(existing.GetResourceVersion())
		logger.Info("Updating nginx proxy Service", "name", nginxProxyServiceName, "externalName", externalName)
		_, err = r.DynamicClient.Resource(serviceGVR).Namespace(site.Namespace).Update(ctx, service, metav1.UpdateOptions{})
		return err
	}

	logger.V(1).Info("nginx proxy Service already exists", "name", nginxProxyServiceName)
	return nil
}

// handleDeletion cleans up on deletion
func (r *StaticSiteReconciler) handleDeletion(ctx context.Context, site *pagesv1.StaticSite) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(site, finalizerName) {
		logger.Info("Cleaning up StaticSite", "name", site.Name)

		// Owned resources (middlewares, IngressRoute) are automatically deleted via ownerReferences

		// Nginx proxy service is shared across sites in the namespace - cleanup if last site
		if err := r.cleanupOrphanedNginxProxyService(ctx, site); err != nil {
			logger.Error(err, "Failed to cleanup nginx proxy service")
			// Don't block deletion for this
		}

		// Certificates are shared and need explicit cleanup
		if site.Spec.Domain != "" {
			if err := r.cleanupOrphanedCertificate(ctx, site); err != nil {
				logger.Error(err, "Failed to cleanup certificate")
				// Don't block deletion for this
			}
		}

		// Remove finalizer
		controllerutil.RemoveFinalizer(site, finalizerName)
		if err := r.Update(ctx, site); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// cleanupOrphanedCertificate removes the certificate if no other sites use this domain
func (r *StaticSiteReconciler) cleanupOrphanedCertificate(ctx context.Context, deletingSite *pagesv1.StaticSite) error {
	logger := log.FromContext(ctx)
	domain := deletingSite.Spec.Domain

	// List all StaticSites
	siteList := &pagesv1.StaticSiteList{}
	if err := r.List(ctx, siteList); err != nil {
		return err
	}

	// Check if any other site uses this domain
	for _, site := range siteList.Items {
		if site.Name == deletingSite.Name && site.Namespace == deletingSite.Namespace {
			continue // Skip the site being deleted
		}
		if site.Spec.Domain == domain {
			// Another site uses this domain, keep the certificate
			logger.V(1).Info("Certificate still in use", "domain", domain, "usedBy", site.Name)
			return nil
		}
	}

	// No other sites use this domain, delete the certificate
	certName := sanitizeDomainForResourceName(domain) + "-tls"
	logger.Info("Deleting orphaned certificate", "name", certName, "domain", domain)

	err := r.DynamicClient.Resource(certificateGVR).Namespace(deletingSite.Namespace).Delete(ctx, certName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	return nil
}

// cleanupOrphanedNginxProxyService removes the nginx proxy service if no other
// StaticSites exist in the namespace
func (r *StaticSiteReconciler) cleanupOrphanedNginxProxyService(ctx context.Context, deletingSite *pagesv1.StaticSite) error {
	logger := log.FromContext(ctx)

	// List all StaticSites in this namespace
	siteList := &pagesv1.StaticSiteList{}
	if err := r.List(ctx, siteList, client.InNamespace(deletingSite.Namespace)); err != nil {
		return err
	}

	// Check if any other site exists in this namespace
	otherSites := 0
	for _, site := range siteList.Items {
		if site.Name == deletingSite.Name {
			continue
		}
		otherSites++
	}

	if otherSites > 0 {
		logger.V(1).Info("nginx proxy Service still in use", "namespace", deletingSite.Namespace, "remainingSites", otherSites)
		return nil
	}

	// No other sites in namespace, delete the proxy service
	logger.Info("Deleting orphaned nginx proxy Service", "namespace", deletingSite.Namespace)
	err := r.DynamicClient.Resource(serviceGVR).Namespace(deletingSite.Namespace).Delete(ctx, nginxProxyServiceName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	return nil
}

// setError sets the error status and returns a Result
func (r *StaticSiteReconciler) setError(ctx context.Context, site *pagesv1.StaticSite, reason string, err error) (ctrl.Result, error) {
	site.Status.Phase = pagesv1.PhaseError
	site.Status.Message = err.Error()
	
	r.Recorder.Event(site, "Warning", reason, err.Error())
	
	if updateErr := r.Status().Update(ctx, site); updateErr != nil {
		return ctrl.Result{}, updateErr
	}
	
	// Retry after 30 seconds
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// SetupWithManager registers the controller
func (r *StaticSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pagesv1.StaticSite{}).
		Complete(r)
}
