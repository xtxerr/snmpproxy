// snmpproxyd is the SNMP proxy server daemon.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/xtxerr/snmpproxy/internal/handler"
	"github.com/xtxerr/snmpproxy/internal/loader"
	"github.com/xtxerr/snmpproxy/internal/manager"
	"github.com/xtxerr/snmpproxy/internal/server"
	"github.com/xtxerr/snmpproxy/internal/store"
)

// Version is set at build time via ldflags
var Version = "dev"

func main() {
	// CLI flags
	cfgPath := flag.String("config", "config.yaml", "config file path")
	listen := flag.String("listen", "", "listen address (overrides config)")
	noTLS := flag.Bool("no-tls", false, "disable TLS")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file")
	tlsKey := flag.String("tls-key", "", "TLS key file")
	token := flag.String("token", "", "auth token (or SNMPPROXY_TOKEN env)")
	dbPath := flag.String("db", "", "database path (overrides config)")
	watch := flag.Bool("watch", false, "watch config for changes")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.Printf("snmpproxyd %s starting...", Version)

	// Load config
	cfg, err := loader.Load(*cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("No config file found, using defaults")
			cfg = &loader.Config{
				Listen: "0.0.0.0:9161",
			}
		} else {
			log.Fatalf("Load config: %v", err)
		}
	}

	// CLI overrides
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *noTLS {
		cfg.TLS.CertFile = ""
		cfg.TLS.KeyFile = ""
	}
	if *tlsCert != "" {
		cfg.TLS.CertFile = *tlsCert
	}
	if *tlsKey != "" {
		cfg.TLS.KeyFile = *tlsKey
	}
	if *dbPath != "" {
		cfg.Storage.DBPath = *dbPath
	}

	// Token from flag or env
	authToken := *token
	if authToken == "" {
		authToken = os.Getenv("SNMPPROXY_TOKEN")
	}
	if authToken != "" && len(cfg.Auth.Tokens) == 0 {
		cfg.Auth.Tokens = []loader.TokenConfig{{ID: "cli", Token: authToken}}
	}

	// Validate
	if len(cfg.Auth.Tokens) == 0 {
		log.Fatal("At least one auth token required (use -token or config)")
	}

	// Set defaults
	if cfg.Listen == "" {
		cfg.Listen = "0.0.0.0:9161"
	}
	if cfg.Storage.DBPath == "" {
		cfg.Storage.DBPath = "snmpproxy.db"
	}
	if cfg.Poller.Workers == 0 {
		cfg.Poller.Workers = 100
	}
	if cfg.Poller.QueueSize == 0 {
		cfg.Poller.QueueSize = 10000
	}
	if cfg.Session.AuthTimeoutSec == 0 {
		cfg.Session.AuthTimeoutSec = 30
	}
	if cfg.Session.ReconnectWindowSec == 0 {
		cfg.Session.ReconnectWindowSec = 600
	}

	// Create manager
	mgr, err := manager.New(&manager.Config{
		DBPath:        cfg.Storage.DBPath,
		SecretKeyPath: cfg.Storage.SecretKeyPath,
		Version:       Version,
	})
	if err != nil {
		log.Fatalf("Create manager: %v", err)
	}

	// Set server defaults in store
	mgr.Store().UpdateServerConfig(&store.ServerConfig{
		DefaultTimeoutMs:  cfg.SNMP.TimeoutMs,
		DefaultRetries:    cfg.SNMP.Retries,
		DefaultIntervalMs: cfg.SNMP.IntervalMs,
		DefaultBufferSize: cfg.SNMP.BufferSize,
	})

	// Apply config (namespaces, targets, pollers)
	if len(cfg.Namespaces) > 0 {
		result, err := loader.Apply(cfg, mgr)
		if err != nil {
			log.Printf("Warning: Apply config: %v", err)
		} else {
			log.Printf("Config applied: %d namespaces, %d targets, %d pollers",
				result.NamespacesCreated, result.TargetsCreated, result.PollersCreated)
			for _, e := range result.Errors {
				log.Printf("Warning: %s", e)
			}
		}
	}

	// Watch config for changes
	if *watch {
		watcher := loader.NewWatcher(*cfgPath, mgr, func(result *loader.ApplyResult) {
			log.Printf("Config reloaded: %d ns, %d targets, %d pollers, %d errors",
				result.NamespacesCreated, result.TargetsCreated,
				result.PollersCreated, len(result.Errors))
		})
		watcher.Start()
		defer watcher.Stop()
	}

	// Convert tokens
	tokens := make([]handler.TokenConfig, len(cfg.Auth.Tokens))
	for i, t := range cfg.Auth.Tokens {
		tokens[i] = handler.TokenConfig{
			ID:         t.ID,
			Token:      t.Token,
			Namespaces: t.Namespaces,
		}
	}

	// Create server
	srv := server.New(&server.Config{
		Manager:           mgr,
		Listen:            cfg.Listen,
		TLSCertFile:       cfg.TLS.CertFile,
		TLSKeyFile:        cfg.TLS.KeyFile,
		Tokens:            tokens,
		AuthTimeoutSec:    cfg.Session.AuthTimeoutSec,
		ReconnectWindowSec: cfg.Session.ReconnectWindowSec,
		PollerWorkers:     cfg.Poller.Workers,
		PollerQueueSize:   cfg.Poller.QueueSize,
	})

	// Handle signals
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("Shutting down...")
		srv.Shutdown()
		mgr.Stop()
	}()

	// Run
	log.Printf("Listening on %s", cfg.Listen)
	if err := srv.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
