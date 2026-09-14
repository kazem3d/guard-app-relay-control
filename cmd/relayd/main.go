// Command relayd is the RaspberryRelay daemon: it drives relay outputs over
// GPIO, serves the HTTP API and embedded web UI, and (eventually) applies
// network settings through NetworkManager.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rmto/raspberryrelay/internal/config"
	"github.com/rmto/raspberryrelay/internal/gpio"
	"github.com/rmto/raspberryrelay/internal/httpapi"
	"github.com/rmto/raspberryrelay/internal/netcfg"
	"github.com/rmto/raspberryrelay/internal/relay"
	"github.com/rmto/raspberryrelay/web"
)

// version is stamped at build time: -ldflags "-X main.version=1.0.3"
var version = "dev"

func main() {
	configPath := flag.String("config", "/etc/relayd/config.json", "path to config.json")
	listen := flag.String("listen", "", "override http.listen from config (e.g. :8080)")
	gpioMode := flag.String("gpio", "", "gpio backend: auto (default), cdev, or mock")
	flag.Parse()

	if v := os.Getenv("RELAYD_GPIO"); v != "" && *gpioMode == "" {
		*gpioMode = v
	}

	if err := run(*configPath, *listen, *gpioMode); err != nil {
		log.Fatalf("relayd: %v", err)
	}
}

func run(configPath, listenOverride, gpioMode string) error {
	store := config.NewStore(configPath)
	cfg, err := store.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	backend, err := gpio.Open(gpioMode, cfg.GPIO.Chip)
	if err != nil {
		return fmt.Errorf("opening gpio backend: %w", err)
	}

	controller, err := relay.New(backend, cfg.Relays)
	if err != nil {
		_ = backend.Close()
		return fmt.Errorf("initialising relay controller: %w", err)
	}
	defer controller.Shutdown()

	// Network configuration degrades gracefully: if NetworkManager isn't
	// reachable (no D-Bus system bus, as on this dev machine's sandbox, or
	// NetworkManager simply isn't running), the daemon still serves relays,
	// auth, and device info — only the Network tab's endpoints report 503.
	var nc httpapi.NetworkConfigurator
	if m, err := netcfg.New(); err != nil {
		log.Printf("relayd: network configuration unavailable, /api/network will report 503: %v", err)
	} else {
		nc = m
	}

	assets, err := fs.Sub(web.FS, ".")
	if err != nil {
		return fmt.Errorf("loading web assets: %w", err)
	}

	server := httpapi.New(store, cfg, controller, nc, assets, version)

	listen := cfg.HTTP.Listen
	if listenOverride != "" {
		listen = listenOverride
	}

	httpServer := &http.Server{
		Addr:              listen,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("relayd: listening on %s (version %s)", listen, version)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Printf("relayd: shutting down")
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("relayd: http shutdown: %v", err)
	}

	// controller.Shutdown() (deferred above) de-energises every relay and
	// releases GPIO lines before the process exits.
	return nil
}
