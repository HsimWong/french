package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListenAddr(t *testing.T) {
	cases := []struct {
		name                       string
		addr, bindAddr, host, port string
		want                       string
	}{
		{"full addr", ":9090", "", "", "", ":9090"},
		{"loopback", "127.0.0.1:8082", "", "", "", "127.0.0.1:8082"},
		{"ipv6", "[::1]:8082", "", "", "", "[::1]:8082"},
		{"bare port", "9000", "", "", "", ":9000"},
		{"bind alias", "", "127.0.0.1:9001", "", "", "127.0.0.1:9001"},
		{"host+port", "", "", "127.0.0.1", "8082", "127.0.0.1:8082"},
		{"ipv6 host+port", "", "", "::1", "8082", "[::1]:8082"},
		{"port only", "", "", "", "9002", ":9002"},
		{"default", "", "", "", "", ":8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ADDR", tc.addr)
			t.Setenv("BIND_ADDR", tc.bindAddr)
			t.Setenv("BIND_HOST", tc.host)
			t.Setenv("PORT", tc.port)
			if got := listenAddr(); got != tc.want {
				t.Fatalf("listenAddr() = %q, want %q", got, tc.want)
			}
		})
	}
	if got := addrForLog(":8082"); got != "http://localhost:8082" {
		t.Fatalf("addrForLog(:8082) = %q", got)
	}
	if got := addrForLog("127.0.0.1:9000"); got != "http://127.0.0.1:9000" {
		t.Fatalf("addrForLog(127.0.0.1:9000) = %q", got)
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# comment
ADDR=127.0.0.1:9000

export PORT = 9001
QUOTED="hello world # not a comment"
SINGLE='a=b'
TRAILING=value # this is a comment
BAD LINE
9INVALID=nope
ACCESS_PASSCODE=from-file
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ADDR", "PORT", "QUOTED", "SINGLE", "TRAILING", "ACCESS_PASSCODE", "BAD LINE", "9INVALID"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("ENV_FILE", path)
	t.Setenv("TRAILING", "preexisting") // a real env var must win
	loadDotEnv()

	want := map[string]string{
		"ADDR":            "127.0.0.1:9000",
		"PORT":            "9001",
		"QUOTED":          "hello world # not a comment",
		"SINGLE":          "a=b",
		"TRAILING":        "preexisting",
		"ACCESS_PASSCODE": "from-file",
	}
	for key, value := range want {
		if got := os.Getenv(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if _, ok := os.LookupEnv("BAD LINE"); ok {
		t.Error("invalid key should be ignored")
	}
	if _, ok := os.LookupEnv("9INVALID"); ok {
		t.Error("key starting with digit should be ignored")
	}
}
