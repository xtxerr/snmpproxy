// snmpproxyd is the SNMP proxy server daemon.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/xtxerr/snmpproxy/config"
	"github.com/xtxerr/snmpproxy/internal/server"
)

func main() {
	// CLI flags
	cfgPath := flag.String("config", "config.yaml", "config file path")
	listen := flag.String("listen", "", "listen address (overrides config)")
	noTLS := flag.Bool("no-tls", false, "disable TLS (overrides config)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (overrides config)")
	tlsKey := flag.String("tls-key", "", "TLS key file (overrides config)")
	token := flag.String("token", "", "auth token (overrides config, or use SNMPPROXY_TOKEN env)")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	// Load config
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		// If no config file, use defaults
		if os.IsNotExist(err) {
			cfg = config.Default()
			log.Printf("No config file, using defaults")
		} else {
			log.Fatalf("Load config: %v", err)
		}
	}

	// CLI overrides
	if *listen != "" {
		cfg.Server.Listen = *listen
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

	// Token from flag or env
	if *token != "" {
		cfg.Auth.Tokens = []config.TokenConfig{{ID: "cli", Token: *token}}
	} else if envToken := os.Getenv("SNMPPROXY_TOKEN"); envToken != "" && len(cfg.Auth.Tokens) == 0 {
		cfg.Auth.Tokens = []config.TokenConfig{{ID: "env", Token: envToken}}
	}

	// Validate
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Config error: %v", err)
	}

	srv := server.New(cfg)

	// Handle signals
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("Shutting down...")
		srv.Shutdown()
	}()

	if err := srv.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
