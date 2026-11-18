package main

import (
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

func main() {
	applyRuntimeLimits()

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
	registerDictionaryRoutes(mux, db, newLexicon(db))
	registerStatsRoutes(mux, db)

	gate := newAuthGate()
	gate.registerAuthRoutes(mux)
	if gate.enabled {
		slog.Info("access passcode enabled")
	} else {
		slog.Info("access passcode not set — server is open")
	}

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
		Handler:           withLog(gate.wrap(mux)),
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

// applyRuntimeLimits honours the two knobs that keep the process inside a
// small box on a cheap VPS:
//
//	MEM_LIMIT_MB  soft ceiling on the Go heap. The collector then starts
//	              working much earlier instead of letting the heap grow to
//	              twice its live size, which is what used to exhaust a 2 GB
//	              machine on every restart.
//	GOMAXPROCS    how many OS threads may run Go code at once. This is the
//	              real CPU limiter for a Go program; the OS scheduler can
//	              still hand other cores to syscalls.
//
// Both are optional: unset means "no limit", i.e. the Go defaults.
func applyRuntimeLimits() {
	if raw := strings.TrimSpace(os.Getenv("MEM_LIMIT_MB")); raw != "" {
		mb, err := strconv.Atoi(raw)
		if err != nil || mb <= 0 {
			slog.Warn("ignoring invalid MEM_LIMIT_MB", "value", raw)
		} else {
			debug.SetMemoryLimit(int64(mb) << 20)
			slog.Info("go heap soft limit set", "limit_mb", mb)
		}
	}
	if raw := strings.TrimSpace(os.Getenv("GOMAXPROCS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			slog.Warn("ignoring invalid GOMAXPROCS", "value", raw)
		} else {
			runtime.GOMAXPROCS(n)
			slog.Info("go maxprocs set", "procs", n)
		}
	}
}
