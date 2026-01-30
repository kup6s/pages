// Package controller enthält die Reconciliation-Logik
package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pagesv1 "github.com/kleinundpartner/kup6s-pages/pkg/apis/v1alpha1"
)

const (
	finalizerName = "pages.kup6s.io/finalizer"
	
	// nginx Service Name im System-Namespace
	nginxServiceName = "static-sites-nginx"
	nginxNamespace   = "kup6s-pages"
)

// StaticSiteReconciler reconciled StaticSite Ressourcen
type StaticSiteReconciler struct {
	client.Client
	DynamicClient dynamic.Interface
	Recorder      record.EventRecorder
	
	// Config
	PagesDomain   string // z.B. "pages.kup6s.io"
	ClusterIssuer string // z.B. "letsencrypt-prod"
}

// GVRs für Traefik und cert-manager
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

// Reconcile ist die Haupt-Reconciliation-Schleife
// Wird aufgerufen wenn sich eine StaticSite ändert
func (r *StaticSiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	
	// 1. StaticSite laden
	site := &pagesv1.StaticSite{}
	if err := r.Get(ctx, req.NamespacedName, site); err != nil {
		if errors.IsNotFound(err) {
			// Wurde gelöscht, nichts zu tun (Finalizer hat aufgeräumt)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling StaticSite", "name", site.Name, "domain", site.Spec.Domain)

	// 2. Deletion handling
	if !site.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, site)
	}

	// 3. Finalizer hinzufügen falls nicht vorhanden
	if !controllerutil.ContainsFinalizer(site, finalizerName) {
		controllerutil.AddFinalizer(site, finalizerName)
		if err := r.Update(ctx, site); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 4. Domain bestimmen (custom oder generiert)
	domain := site.Spec.Domain
	if domain == "" {
		domain = fmt.Sprintf("%s.%s", site.Name, r.PagesDomain)
	}

	// 5. Middleware erstellen/updaten (addPrefix)
	if err := r.reconcileMiddleware(ctx, site); err != nil {
		return r.setError(ctx, site, "MiddlewareFailed", err)
	}

	// 6. IngressRoute erstellen/updaten
	if err := r.reconcileIngressRoute(ctx, site, domain); err != nil {
		return r.setError(ctx, site, "IngressFailed", err)
	}

	// 7. Certificate erstellen (wenn custom domain)
	if site.Spec.Domain != "" {
		if err := r.reconcileCertificate(ctx, site, domain); err != nil {
			return r.setError(ctx, site, "CertificateFailed", err)
		}
	}

	// 8. Status aktualisieren
	site.Status.Phase = pagesv1.PhaseReady
	site.Status.Message = "Site configured, waiting for sync"
	site.Status.URL = fmt.Sprintf("https://%s", domain)
	
	if err := r.Status().Update(ctx, site); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Event(site, "Normal", "Configured", fmt.Sprintf("Site configured at %s", site.Status.URL))

	// Requeue nach 5 Minuten für Status-Check
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// reconcileMiddleware erstellt die Traefik Middleware für addPrefix
func (r *StaticSiteReconciler) reconcileMiddleware(ctx context.Context, site *pagesv1.StaticSite) error {
	logger := log.FromContext(ctx)
	
	// Middleware Objekt bauen
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
					// Der Pfad unter dem die Site im nginx liegt
					"prefix": "/" + site.Name,
				},
			},
		},
	}

	// Create or Update
	existing, err := r.DynamicClient.Resource(middlewareGVR).Namespace(site.Namespace).Get(ctx, middleware.GetName(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Middleware", "name", middleware.GetName())
			_, err = r.DynamicClient.Resource(middlewareGVR).Namespace(site.Namespace).Create(ctx, middleware, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// Update existing
	middleware.SetResourceVersion(existing.GetResourceVersion())
	logger.Info("Updating Middleware", "name", middleware.GetName())
	_, err = r.DynamicClient.Resource(middlewareGVR).Namespace(site.Namespace).Update(ctx, middleware, metav1.UpdateOptions{})
	return err
}

// reconcileIngressRoute erstellt die Traefik IngressRoute
func (r *StaticSiteReconciler) reconcileIngressRoute(ctx context.Context, site *pagesv1.StaticSite, domain string) error {
	logger := log.FromContext(ctx)

	// TLS Config - bei custom domain eigenes Cert, sonst Wildcard
	tlsConfig := map[string]interface{}{}
	if site.Spec.Domain != "" {
		tlsConfig["secretName"] = site.Name + "-tls"
	} else {
		// Wildcard cert für *.pages.kup6s.io
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
						"match": fmt.Sprintf("Host(`%s`)", domain),
						"kind":  "Rule",
						"middlewares": []interface{}{
							map[string]interface{}{
								"name":      site.Name + "-prefix",
								"namespace": site.Namespace,
							},
						},
						"services": []interface{}{
							map[string]interface{}{
								"name":      nginxServiceName,
								"namespace": nginxNamespace,
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
			logger.Info("Creating IngressRoute", "name", ingressRoute.GetName(), "domain", domain)
			_, err = r.DynamicClient.Resource(ingressRouteGVR).Namespace(site.Namespace).Create(ctx, ingressRoute, metav1.CreateOptions{})
			return err
		}
		return err
	}

	ingressRoute.SetResourceVersion(existing.GetResourceVersion())
	logger.Info("Updating IngressRoute", "name", ingressRoute.GetName(), "domain", domain)
	_, err = r.DynamicClient.Resource(ingressRouteGVR).Namespace(site.Namespace).Update(ctx, ingressRoute, metav1.UpdateOptions{})
	return err
}

// reconcileCertificate erstellt ein cert-manager Certificate
func (r *StaticSiteReconciler) reconcileCertificate(ctx context.Context, site *pagesv1.StaticSite, domain string) error {
	logger := log.FromContext(ctx)

	certificate := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]interface{}{
				"name":      site.Name + "-tls",
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
				"secretName": site.Name + "-tls",
				"dnsNames":   []interface{}{domain},
				"issuerRef": map[string]interface{}{
					"name": r.ClusterIssuer,
					"kind": "ClusterIssuer",
				},
			},
		},
	}

	existing, err := r.DynamicClient.Resource(certificateGVR).Namespace(site.Namespace).Get(ctx, certificate.GetName(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Creating Certificate", "name", certificate.GetName(), "domain", domain)
			_, err = r.DynamicClient.Resource(certificateGVR).Namespace(site.Namespace).Create(ctx, certificate, metav1.CreateOptions{})
			return err
		}
		return err
	}

	certificate.SetResourceVersion(existing.GetResourceVersion())
	logger.Info("Updating Certificate", "name", certificate.GetName())
	_, err = r.DynamicClient.Resource(certificateGVR).Namespace(site.Namespace).Update(ctx, certificate, metav1.UpdateOptions{})
	return err
}

// handleDeletion räumt bei Löschung auf
func (r *StaticSiteReconciler) handleDeletion(ctx context.Context, site *pagesv1.StaticSite) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if controllerutil.ContainsFinalizer(site, finalizerName) {
		logger.Info("Cleaning up StaticSite", "name", site.Name)

		// Owned Resources werden automatisch gelöscht (ownerReferences)
		// Hier könnten wir den Syncer triggern um /sites/<n>/ zu löschen

		// Finalizer entfernen
		controllerutil.RemoveFinalizer(site, finalizerName)
		if err := r.Update(ctx, site); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// setError setzt den Error-Status und returned ein Result
func (r *StaticSiteReconciler) setError(ctx context.Context, site *pagesv1.StaticSite, reason string, err error) (ctrl.Result, error) {
	site.Status.Phase = pagesv1.PhaseError
	site.Status.Message = err.Error()
	
	r.Recorder.Event(site, "Warning", reason, err.Error())
	
	if updateErr := r.Status().Update(ctx, site); updateErr != nil {
		return ctrl.Result{}, updateErr
	}
	
	// Retry nach 30 Sekunden
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// SetupWithManager registriert den Controller
func (r *StaticSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pagesv1.StaticSite{}).
		Complete(r)
}
