package main

import (
	"context"
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

func webRoot() fs.FS {
	sub, _ := fs.Sub(webFS, "web")
	return sub
}

func main() {
	dataDir := flag.String("data", envOr("WH_DATA", "./data"), "directory for config.json and history.json")
	listen := flag.String("listen", envOr("WH_LISTEN", ":8080"), "listen address")
	flag.Parse()

	a := NewApp(*dataDir)
	go a.worker()
	go a.broadcaster()
	go a.scheduler()

	srv := &http.Server{Addr: *listen, Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		a.stopCurrent()
		sc, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		srv.Shutdown(sc)
	}()
	log.Printf("Wallhaven Control %s listening on %s (data: %s)", version, *listen, *dataDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
