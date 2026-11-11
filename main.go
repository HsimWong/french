package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "french.db"
	}
	db, err := openDB(dbPath)
	if err != nil {
		slog.Error("database init failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	mux := http.NewServeMux()
	registerRoutes(mux, db)

	assets, err := webAssets()
	if err != nil {
		slog.Error("web assets", "error", err)
		os.Exit(1)
	}
	mux.Handle("GET /", http.FileServer(http.FS(assets)))

	addr := os.Getenv("PORT")
	if addr == "" {
		addr = "8080"
	}
	addr = ":" + addr
	srv := &http.Server{
		Addr:              addr,
		Handler:           withLog(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("français progressif démarré", "addr", "http://localhost"+addr, "db", dbPath)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func withLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("request", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start).String())
	})
}
