// Command hostekd is the hostek service daemon: it samples live system metrics and
// exposes them (plus OS power configuration) under /api/services/hostek/, validating
// the shared holistic session. It runs unprivileged and escalates only via narrow
// sudo wrappers for configuration writes.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hostek/internal/api"
	"hostek/internal/auth"
	"hostek/internal/gpu"
	"hostek/internal/hardware"
	"hostek/internal/metrics"
	"hostek/internal/netmon"
	"hostek/internal/powermon"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8771", "address to listen on")
	interval := flag.Duration("interval", 2*time.Second, "metric sampling interval")
	flag.Parse()

	secret, err := auth.LoadSecret()
	if err != nil {
		log.Fatalf("hostekd: %v", err)
	}
	v := auth.NewVerifier(secret, os.Getenv("HOSTEK_ADMIN_GROUP"))

	// External samplers (NVIDIA GPU, per-process network) and the hardware inventory run
	// independently; each no-ops cleanly when its tooling/privileges are unavailable.
	gpuS := gpu.New(*interval)
	gpuS.Start()
	netS := netmon.New()
	netS.Start()
	pwrS := powermon.New()
	pwrS.Start()
	hw := hardware.New()
	hw.Start()

	col := metrics.New(*interval, gpuS, netS, pwrS)
	col.Start()

	srv := &http.Server{
		Handler:           api.New(v, col, hw).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Bind synchronously so an "address in use" surfaces here, not in a goroutine.
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("hostekd: listen %s: %v", *listen, err)
	}
	go func() {
		log.Printf("hostekd listening on %s (sampling every %s)", *listen, *interval)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("hostekd: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Print("hostekd stopped")
}
