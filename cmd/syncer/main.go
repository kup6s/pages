// Syncer main entry point
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/kleinundpartner/kup6s-pages/pkg/syncer"
)

func main() {
	var sitesRoot string
	var syncInterval time.Duration
	var webhookAddr string

	flag.StringVar(&sitesRoot, "sites-root", "/sites", "Root directory for synced sites")
	flag.DurationVar(&syncInterval, "sync-interval", 5*time.Minute, "Interval between full syncs")
	flag.StringVar(&webhookAddr, "webhook-addr", ":8080", "Address for webhook HTTP server")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("syncer")

	// Kubernetes Clients
	config := ctrl.GetConfigOrDie()
	
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Error(err, "unable to create dynamic client")
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Error(err, "unable to create clientset")
		os.Exit(1)
	}

	// Sites-Verzeichnis erstellen
	if err := os.MkdirAll(sitesRoot, 0755); err != nil {
		log.Error(err, "unable to create sites directory")
		os.Exit(1)
	}

	// Syncer erstellen
	s := &syncer.Syncer{
		DynamicClient:   dynamicClient,
		ClientSet:       clientset,
		SitesRoot:       sitesRoot,
		DefaultInterval: syncInterval,
	}

	// Webhook Server erstellen
	webhookServer := &syncer.WebhookServer{
		Syncer: s,
	}

	// Context mit Cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal Handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Sync Loop in Goroutine starten
	go func() {
		log.Info("Starting sync loop", "interval", syncInterval)
		s.RunLoop(ctx)
	}()

	// Webhook Server in Goroutine starten
	go func() {
		if err := webhookServer.Start(ctx, webhookAddr); err != nil {
			log.Error(err, "webhook server failed")
		}
	}()

	log.Info("Syncer started",
		"sitesRoot", sitesRoot,
		"syncInterval", syncInterval,
		"webhookAddr", webhookAddr,
	)

	// Warten auf Signal
	<-sigChan
	log.Info("Shutting down")
	cancel()
}
