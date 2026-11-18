package main

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// loadDotEnv reads a .env file into the process environment. It is deliberately
// dependency-free: the whole app is a single Go binary, and pulling in a
// third-party dotenv library just for KEY=VALUE lines is not worth it.
//
// Rules:
//   - values already present in the real environment win (systemd, shell,
//     docker -e, ...); .env only fills the gaps. This means an operator can
//     always override a config file value without editing it.
//   - blank lines and lines starting with '#' are comments.
//   - "export KEY=value" is accepted.
//   - quoted values ("..." or '...') keep their '#' and spaces; unquoted
//     values are stripped of a trailing " # comment".
//
// The file is looked up at $ENV_FILE if set, else ./.env, else .env next to
// the executable (so a wrong WorkingDirectory cannot silently drop config).
func loadDotEnv() {
	path := strings.TrimSpace(os.Getenv("ENV_FILE"))
	if path == "" {
		path = ".env"
	}
	if _, err := os.Stat(path); os.IsNotExist(err) && !strings.Contains(path, string(filepath.Separator)) {
		if exe, e := os.Executable(); e == nil {
			candidate := filepath.Join(filepath.Dir(exe), ".env")
			if _, e := os.Stat(candidate); e == nil {
				path = candidate
			}
		}
	}

	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("cannot read .env", "path", path, "error", err)
		}
		return
	}
	defer f.Close()

	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if err := os.Setenv(key, value); err != nil {
			slog.Warn("ignoring .env entry", "key", key, "error", err)
			continue
		}
		n++
	}
	if err := sc.Err(); err != nil {
		slog.Warn("reading .env", "path", path, "error", err)
	}
	if n > 0 {
		slog.Info("loaded .env", "path", path, "entries", n)
	}
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// listenAddr resolves the listen address from configuration:
//
//	ADDR       full listen address, e.g. ":8082" / "127.0.0.1:8082" / "[::1]:8082"
//	BIND_ADDR  alias for ADDR
//	BIND_HOST  host part when ADDR is empty (default: all interfaces)
//	PORT       port when ADDR is empty (default: 8080)
//
// A bare number in ADDR is treated as a port for convenience.
func listenAddr() string {
	addr := strings.TrimSpace(os.Getenv("ADDR"))
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("BIND_ADDR"))
	}
	if addr != "" {
		if !strings.Contains(addr, ":") {
			return ":" + addr
		}
		return addr
	}

	host := strings.TrimSpace(os.Getenv("BIND_HOST"))
	if host == "" {
		host = strings.TrimSpace(os.Getenv("HOST"))
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	if host != "" && strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host + ":" + port
}

// addrForLog turns a listen address into something readable in startup logs.
func addrForLog(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
