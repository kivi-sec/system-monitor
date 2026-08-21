// Command server is the single-binary entrypoint for the System Monitor
// dashboard: it wires together the metrics collector, the websocket hub,
// the REST API, and the embedded frontend, then serves everything over
// one HTTP listener bound to localhost by default.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"system-monitor/internal/api"
	"system-monitor/internal/collector"
	"system-monitor/internal/websocket"
	webassets "system-monitor/web"
)

type config struct {
	Host           string
	Port           string
	UpdateInterval time.Duration
	DiskPath       string
}

func loadConfig() (config, error) {
	cfg := config{
		Host:           getEnv("HOST", "127.0.0.1"),
		Port:           getEnv("PORT", "8080"),
		UpdateInterval: getEnvDuration("UPDATE_INTERVAL", time.Second),
		DiskPath:       getEnv("DISK_PATH", "/"),
	}

	if _, err := strconv.Atoi(cfg.Port); err != nil {
		return cfg, fmt.Errorf("invalid PORT %q: must be numeric: %w", cfg.Port, err)
	}
	if info, err := os.Stat(cfg.DiskPath); err != nil {
		return cfg, fmt.Errorf("invalid DISK_PATH %q: %w", cfg.DiskPath, err)
	} else if !info.IsDir() {
		return cfg, fmt.Errorf("invalid DISK_PATH %q: not a directory", cfg.DiskPath)
	}
	if cfg.UpdateInterval <= 0 {
		return cfg, fmt.Errorf("invalid UPDATE_INTERVAL: must be positive, got %s", cfg.UpdateInterval)
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	// Also accept a bare integer number of seconds for convenience.
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	log.Printf("warning: could not parse %s=%q, using default %s", key, v, fallback)
	return fallback
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery()) // never let a handler panic take the server down
	router.Use(requestLogger())

	hub := websocket.NewHub()
	metricsCollector := collector.New(cfg.UpdateInterval, cfg.DiskPath)

	server := api.NewServer(metricsCollector, hub)
	server.RegisterRoutes(router)

	// Serve the embedded frontend at "/". Falls back to index.html for
	// unknown paths is unnecessary here since this is a flat static site,
	// not a client-side-routed SPA.
	staticFS, err := fs.Sub(webassets.Assets, ".")
	if err != nil {
		log.Fatalf("failed to init embedded frontend: %v", err)
	}
	router.StaticFS("/static", http.FS(staticFS))
	router.GET("/", func(c *gin.Context) { serveEmbedded(c, staticFS, "index.html") })
	router.GET("/app.js", func(c *gin.Context) { serveEmbedded(c, staticFS, "app.js") })
	router.GET("/styles.css", func(c *gin.Context) { serveEmbedded(c, staticFS, "styles.css") })

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The collector runs in its own goroutine and pushes every sample
	// straight to the websocket hub for broadcast; it never blocks HTTP
	// handling. wg lets main wait for it to actually finish before exiting.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		metricsCollector.Run(ctx, func(m collector.Metrics) {
			hub.Broadcast(m)
		})
	}()

	addr := cfg.Host + ":" + cfg.Port
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("System Monitor listening on http://%s (update interval: %s)", addr, cfg.UpdateInterval)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down...")
	case err := <-serverErr:
		if err != nil {
			log.Printf("server error: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}

	stop() // ensure the collector's context is cancelled even if we got here via serverErr
	wg.Wait()

	log.Println("shutdown complete")
}

func serveEmbedded(c *gin.Context, fsys fs.FS, name string) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	contentType := "text/plain; charset=utf-8"
	switch {
	case len(name) > 5 && name[len(name)-5:] == ".html":
		contentType = "text/html; charset=utf-8"
	case len(name) > 3 && name[len(name)-3:] == ".js":
		contentType = "application/javascript; charset=utf-8"
	case len(name) > 4 && name[len(name)-4:] == ".css":
		contentType = "text/css; charset=utf-8"
	}
	c.Data(http.StatusOK, contentType, data)
}

// requestLogger is a minimal, low-overhead access log - deliberately not
// using gin's default logger middleware so we can keep formatting
// consistent with the rest of the app's log lines.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		c.Next()
		if path == "/ws" {
			return // avoid noisy logs for the long-lived websocket connection
		}
		log.Printf("%s %s %d %s", c.Request.Method, path, c.Writer.Status(), time.Since(start))
	}
}