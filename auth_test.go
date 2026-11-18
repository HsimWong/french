package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gateHandler mirrors production wiring: auth routes and health are public,
// everything else goes through the gate.
func gateHandler(g *authGate) http.Handler {
	mux := http.NewServeMux()
	g.registerAuthRoutes(mux)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("GET /api/courses", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("courses"))
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("app"))
	})
	return g.wrap(mux)
}

func TestAuthGateProtectsEverythingButHealthAndLogin(t *testing.T) {
	t.Setenv("ACCESS_PASSCODE", "s3cret")
	g := newAuthGate()
	if !g.enabled {
		t.Fatal("gate should be enabled")
	}
	h := gateHandler(g)

	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/courses", 401},
		{"GET", "/", 200}, // login page, not the app
		{"GET", "/api/health", 200},
		{"GET", "/api/auth/status", 200},
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rr.Code, tc.want)
		}
	}

	// The page response must be the self-contained login document.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rr.Body.String(), "访问口令") {
		t.Error("unauthenticated page should be the login document")
	}
}

func TestAuthLoginSetsCookieAndUnlocksAPI(t *testing.T) {
	t.Setenv("ACCESS_PASSCODE", "s3cret")
	g := newAuthGate()
	h := gateHandler(g)

	// Wrong passcode.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"passcode":"nope"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("wrong passcode = %d, want 401", rr.Code)
	}

	// Correct passcode.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"passcode":"s3cret"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a cookie")
	}

	// The signed cookie unlocks a protected API call.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/courses", nil)
	req.AddCookie(cookies[0])
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("authenticated API = %d, want 200", rr.Code)
	}
}

func TestAuthGateDisabledWhenPasscodeEmpty(t *testing.T) {
	t.Setenv("ACCESS_PASSCODE", "")
	g := newAuthGate()
	if g.enabled {
		t.Fatal("gate should be disabled without a passcode")
	}
	rr := httptest.NewRecorder()
	gateHandler(g).ServeHTTP(rr, httptest.NewRequest("GET", "/api/courses", nil))
	if rr.Code != 200 {
		t.Fatalf("open gate = %d, want 200", rr.Code)
	}
}

func TestPasscodeRequiredDefaultsToTrue(t *testing.T) {
	t.Setenv("REQUIRE_PASSCODE", "")
	if !passcodeRequired() {
		t.Fatal("missing REQUIRE_PASSCODE must fail closed")
	}
	t.Setenv("REQUIRE_PASSCODE", "0")
	if passcodeRequired() {
		t.Fatal("REQUIRE_PASSCODE=0 must allow open access")
	}
	t.Setenv("REQUIRE_PASSCODE", "false")
	if passcodeRequired() {
		t.Fatal("REQUIRE_PASSCODE=false must allow open access")
	}
}

func TestAuthStatusJSON(t *testing.T) {
	t.Setenv("ACCESS_PASSCODE", "s3cret")
	g := newAuthGate()
	rr := httptest.NewRecorder()
	h := gateHandler(g)
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/auth/status", nil))
	var out struct {
		Enabled       bool `json:"enabled"`
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Enabled || out.Authenticated {
		t.Fatalf("status = %+v, want enabled and not authenticated", out)
	}
}
