// Command relayd is the BLE-over-IP bridge relay server: a dumb broker that
// authenticates host/client sessions and forwards opaque GATT-protocol
// frames between them. See the project plan for the full wire protocol.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	blerelay "github.com/overkill-cc/ble-relay-server"
	"github.com/overkill-cc/ble-relay-server/internal/wsserver"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	certFile := flag.String("tls-cert", "", "path to TLS certificate (required unless -insecure-http)")
	keyFile := flag.String("tls-key", "", "path to TLS private key (required unless -insecure-http)")
	insecureHTTP := flag.Bool("insecure-http", false, "serve plain HTTP instead of HTTPS/WSS (local dev only, never for a public deployment)")
	flag.Parse()

	if !*insecureHTTP && (*certFile == "" || *keyFile == "") {
		log.Fatal("relayd: -tls-cert and -tls-key are required (or pass -insecure-http for local dev only)")
	}

	srv := wsserver.NewServer(blerelay.PrivacyPolicyHTML)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go srv.StartReaper(ctx)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	var err error
	if *insecureHTTP {
		log.Printf("relayd: listening on %s (plain HTTP — local dev only)", *addr)
		err = httpServer.ListenAndServe()
	} else {
		log.Printf("relayd: listening on %s (TLS)", *addr)
		err = httpServer.ListenAndServeTLS(*certFile, *keyFile)
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("relayd: server error: %v", err)
	}
}
