package main

// Optional access passcode. Set ACCESS_PASSCODE in the environment to enable;
// an empty value keeps the old fully-open behaviour (useful for local checks).
//
// The session is a stateless HMAC-signed cookie, so no server-side session
// store is needed and it survives process restarts. The signing key is derived
// from the passcode itself: changing the passcode invalidates old cookies.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	authCookie   = "fp_auth"
	authLifetime = 30 * 24 * time.Hour
)

type authGate struct {
	enabled   bool
	passcode  string
	secret    []byte
	loginHTML []byte
}

// passcodeRequired makes the gate fail closed. An empty ACCESS_PASSCODE used
// to mean "run wide open", which is exactly how a missing .env silently
// disabled protection in production. Open access must now be explicit:
// REQUIRE_PASSCODE=0.
func passcodeRequired() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("REQUIRE_PASSCODE"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func newAuthGate() *authGate {
	passcode := strings.TrimSpace(os.Getenv("ACCESS_PASSCODE"))
	g := &authGate{loginHTML: []byte(loginPage)}
	if passcode == "" {
		return g
	}
	sum := sha256.Sum256([]byte("francais-passcode-v1|" + passcode))
	g.enabled, g.passcode, g.secret = true, passcode, sum[:]
	return g
}

func (g *authGate) sign(payload string) string {
	mac := hmac.New(sha256.New, g.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (g *authGate) issue() string {
	exp := strconv.FormatInt(time.Now().Add(authLifetime).Unix(), 10)
	return exp + "." + g.sign(exp)
}

func (g *authGate) valid(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || exp < time.Now().Unix() {
		return false
	}
	want := g.sign(parts[0])
	return subtle.ConstantTimeCompare([]byte(want), []byte(parts[1])) == 1
}

func (g *authGate) checkPasscode(given string) bool {
	if !g.enabled {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(g.passcode)) == 1
}

func (g *authGate) setCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     authCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// registerAuthRoutes is called before the gate is installed. These three
// endpoints must stay reachable without a valid session.
func (g *authGate) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		authed := !g.enabled
		if g.enabled {
			if c, err := r.Cookie(authCookie); err == nil {
				authed = g.valid(c.Value)
			}
		}
		writeJSON(w, 200, map[string]any{"enabled": g.enabled, "authenticated": authed})
	})

	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Passcode string `json:"passcode"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		if !g.checkPasscode(in.Passcode) {
			writeErr(w, 401, "口令不正确")
			return
		}
		if g.enabled {
			g.setCookie(w, r, g.issue(), int(authLifetime.Seconds()))
		}
		writeJSON(w, 200, map[string]any{"ok": true, "enabled": g.enabled})
	})

	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		g.setCookie(w, r, "", -1)
		writeJSON(w, 200, map[string]any{"ok": true})
	})
}

// wrap protects everything except the auth endpoints, the health probe and
// the token-protected speaking-practice ingress.
// Unauthenticated API calls get a JSON 401 that the frontend turns into a
// login screen; page requests are served the self-contained login document
// directly, which keeps it working under any nginx sub-path prefix.
func (g *authGate) wrap(next http.Handler) http.Handler {
	if !g.enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		// /api/speaking/ingest is the only non-auth endpoint outside the gate:
		// it carries its own scoped token (SPEAKING_INGEST_TOKEN) and is used
		// by the daily automation. It cannot read or modify anything else.
		if p == "/api/health" || p == "/api/speaking/ingest" || strings.HasPrefix(p, "/api/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(authCookie); err == nil && g.valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 401, map[string]any{"error": "需要访问口令", "auth_required": true})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write(g.loginHTML)
	})
}

// Self-contained on purpose: no CSS/JS files to load, so it renders correctly
// at /, /french/, or any other mount point without path rewriting.
const loginPage = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Français progressif · 访问口令</title>
<style>
:root{--blue:#185FA5;--blue-dark:#0C447C;--ink:#20242B;--muted:#6B7280;--line:#E3E7ED;--page:#F6F8FB}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
  font:15px/1.6 "Segoe UI","Microsoft YaHei",sans-serif;color:var(--ink);
  background:linear-gradient(150deg,#0C447C,#185FA5 55%,#F6F8FB 55%,#F6F8FB)}
.card{width:min(420px,calc(100vw - 32px));background:#fff;border:1px solid var(--line);
  border-radius:14px;padding:34px 30px;box-shadow:0 18px 50px rgba(12,68,124,.22)}
.mark{width:46px;height:46px;border-radius:11px;background:var(--blue);color:#fff;
  display:flex;align-items:center;justify-content:center;font-size:26px;font-weight:700;margin-bottom:18px}
h1{margin:0 0 6px;font-size:21px}
p{margin:0 0 24px;color:var(--muted);font-size:13px}
label{display:block;font-size:13px;font-weight:600;margin-bottom:8px}
input{width:100%;padding:11px 13px;border:1px solid var(--line);border-radius:8px;font-size:15px;outline:none}
input:focus{border-color:var(--blue);box-shadow:0 0 0 3px rgba(24,95,165,.12)}
button{width:100%;margin-top:16px;padding:11px;border:0;border-radius:8px;background:var(--blue);
  color:#fff;font-size:15px;font-weight:600;cursor:pointer}
button:hover{background:var(--blue-dark)}
button:disabled{opacity:.6;cursor:wait}
#err{margin-top:12px;color:#C0392B;font-size:13px;min-height:20px}
.tip{margin-top:18px;padding-top:14px;border-top:1px solid var(--line);color:var(--muted);font-size:12px}
</style></head><body>
<div class="card">
  <div class="mark">F</div>
  <h1>Français progressif</h1>
  <p>学习台 · 请输入访问口令以继续</p>
  <form id="f">
    <label for="p">访问口令</label>
    <input id="p" name="p" type="password" autocomplete="current-password" autofocus required placeholder="访问口令">
    <button type="submit" id="b">进入学习台</button>
  </form>
  <div id="err"></div>
  <div class="tip">口令由服务端环境变量配置；验证通过后 30 天内无需重复输入。</div>
</div>
<script>
const f=document.getElementById('f'),p=document.getElementById('p'),
      b=document.getElementById('b'),err=document.getElementById('err');
f.addEventListener('submit',async e=>{
  e.preventDefault();err.textContent='';b.disabled=true;b.textContent='验证中…';
  try{
    const r=await fetch('api/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify({passcode:p.value})});
    const d=await r.json().catch(()=>({}));
    if(!r.ok)throw new Error(d.error||('HTTP '+r.status));
    location.replace('./');
  }catch(ex){err.textContent=ex.message;b.disabled=false;b.textContent='进入学习台';p.focus();p.select();}
});
</script></body></html>`
