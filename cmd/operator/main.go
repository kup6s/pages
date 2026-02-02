// Operator main entry point
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	pagesv1 "github.com/kup6s/pages/pkg/apis/v1alpha1"
	"github.com/kup6s/pages/pkg/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(pagesv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var pagesDomain string
	var clusterIssuer string
	var nginxNamespace string
	var nginxServiceName string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address for metrics endpoint")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address for health probes")
	flag.StringVar(&pagesDomain, "pages-domain", "pages.kup6s.com", "Base domain for auto-generated URLs")
	flag.StringVar(&clusterIssuer, "cluster-issuer", "letsencrypt-prod", "cert-manager ClusterIssuer name")
	flag.StringVar(&nginxNamespace, "nginx-namespace", "kup6s-pages", "Namespace where nginx service runs")
	flag.StringVar(&nginxServiceName, "nginx-service-name", "kup6s-pages-nginx", "Name of the nginx service in the system namespace")
	
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Get kubeconfig once and reuse
	config := ctrl.GetConfigOrDie()

	// Configure namespace-scoped cache for Certificates.
	// The operator only manages Certificates in the nginx namespace,
	// but RBAC grants namespace-scoped permissions (Role, not ClusterRole).
	// Without this, controller-runtime's default cluster-wide cache would
	// require cluster-scope list/watch permissions.
	certObj := &unstructured.Unstructured{}
	certObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cert-manager.io",
		Version: "v1",
		Kind:    "Certificate",
	})

	// Create manager
	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         true,
		LeaderElectionID:       "kup6s-pages-operator",
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				certObj: {
					Namespaces: map[string]cache.Config{
						nginxNamespace: {},
					},
				},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Dynamic Client for Traefik/cert-manager CRDs
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		setupLog.Error(err, "unable to create dynamic client")
		os.Exit(1)
	}

	// Register controller
	if err = (&controller.StaticSiteReconciler{
		Client:           mgr.GetClient(),
		DynamicClient:    dynamicClient,
		Recorder:         mgr.GetEventRecorder("staticsite-controller"),
		PagesDomain:      pagesDomain,
		ClusterIssuer:    clusterIssuer,
		NginxNamespace:   nginxNamespace,
		NginxServiceName: nginxServiceName,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "StaticSite")
		os.Exit(1)
	}

	// Health Checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
