// Syncer main entry point
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/kup6s/pages/pkg/syncer"
)

func main() {
	var sitesRoot string
	var syncInterval time.Duration
	var webhookAddr string
	var webhookSecret string
	var allowedHosts string

	flag.StringVar(&sitesRoot, "sites-root", "/sites", "Root directory for synced sites")
	flag.DurationVar(&syncInterval, "sync-interval", 5*time.Minute, "Interval between full syncs")
	flag.StringVar(&webhookAddr, "webhook-addr", ":8080", "Address for webhook HTTP server")
	flag.StringVar(&webhookSecret, "webhook-secret", "", "Secret for webhook signature validation")
	flag.StringVar(&allowedHosts, "allowed-hosts", "", "Comma-separated list of allowed Git hosts (SSRF protection)")

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

	// Create sites directory
	if err := os.MkdirAll(sitesRoot, 0755); err != nil {
		log.Error(err, "unable to create sites directory")
		os.Exit(1)
	}

	// Parse allowed hosts
	var hosts []string
	if allowedHosts != "" {
		for _, h := range strings.Split(allowedHosts, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				hosts = append(hosts, h)
			}
		}
	}

	// Create Syncer
	s := &syncer.Syncer{
		DynamicClient:   dynamicClient,
		ClientSet:       clientset,
		SitesRoot:       sitesRoot,
		DefaultInterval: syncInterval,
		AllowedHosts:    hosts,
	}

	// Create Webhook Server
	webhookServer := &syncer.WebhookServer{
		Syncer:        s,
		WebhookSecret: webhookSecret,
	}

	// Context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal Handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start Sync Loop in goroutine
	go func() {
		log.Info("Starting sync loop", "interval", syncInterval)
		s.RunLoop(ctx)
	}()

	// Start Webhook Server in goroutine
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

	// Wait for signal
	<-sigChan
	log.Info("Shutting down")
	cancel()
}
