package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func speakingTestDB(t *testing.T) (*sql.DB, *http.ServeMux) {
	t.Helper()
	t.Setenv("SPEAKING_VIDEO_DIR", filepath.Join(t.TempDir(), "videos"))
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "speaking.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerSpeakingRoutes(mux, db)
	t.Cleanup(func() { db.Close() })
	return db, mux
}

func speakingRequest(t *testing.T, mux http.Handler, method, path string, body []byte, contentType string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func TestSpeakingPracticeRoundTrip(t *testing.T) {
	db, mux := speakingTestDB(t)

	payload := []byte(`{"episode":7,"date":"2026-11-24","title":"我的周末","source":"上个星期六，天气特别好。","focus":"passé composé / imparfait","level":"A2-B1"}`)
	code, body := speakingRequest(t, mux, "POST", "/api/speaking/practices", payload, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create practice: %d %s", code, body)
	}
	var created struct {
		ID      int64 `json:"id"`
		Episode int   `json:"episode"`
		Updated bool  `json:"updated"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.ID <= 0 || created.Episode != 7 || created.Updated {
		t.Fatalf("unexpected create result: %+v", created)
	}

	// Upsert same episode: no duplicate row, updated flag flips.
	code, body = speakingRequest(t, mux, "POST", "/api/speaking/practices", payload, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"updated":true`) {
		t.Fatalf("upsert practice: %d %s", code, body)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM speaking_practice`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("practice count = %d, err = %v", count, err)
	}

	rev := []byte(`{"kind":"draft","text":"Bonjour, je voudrais parler de mon week-end.","note":"试试看"}`)
	code, body = speakingRequest(t, mux, "POST", "/api/speaking/practices/1/revisions", rev, "application/json")
	if code != http.StatusCreated || !strings.Contains(string(body), `"version":1`) {
		t.Fatalf("create revision: %d %s", code, body)
	}
	code, body = speakingRequest(t, mux, "POST", "/api/speaking/practices/1/revisions", rev, "application/json")
	if code != http.StatusCreated || !strings.Contains(string(body), `"version":2`) {
		t.Fatalf("second revision: %d %s", code, body)
	}

	code, body = speakingRequest(t, mux, "GET", "/api/speaking/practices", nil, "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var listed struct {
		Practices []struct {
			Episode   int `json:"episode"`
			Revisions []struct {
				Version int `json:"version"`
			} `json:"revisions"`
		} `json:"practices"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Practices) != 1 || len(listed.Practices[0].Revisions) != 2 {
		t.Fatalf("unexpected list: %s", body)
	}
	if listed.Practices[0].Revisions[0].Version != 2 {
		t.Fatalf("revisions not newest-first: %s", body)
	}
}

func TestSpeakingVideoUploadAndServe(t *testing.T) {
	_, mux := speakingTestDB(t)
	payload := []byte(`{"episode":1,"date":"2026-11-25","title":"旅行","source":"中文原稿"}`)
	if code, body := speakingRequest(t, mux, "POST", "/api/speaking/practices", payload, "application/json"); code != http.StatusOK {
		t.Fatalf("create practice: %d %s", code, body)
	}

	clip := make([]byte, 2048)
	copy(clip, []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("video", "my clip.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(clip); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	code, body := speakingRequest(t, mux, "POST", "/api/speaking/practices/1/videos", buf.Bytes(), mw.FormDataContentType())
	if code != http.StatusCreated {
		t.Fatalf("upload: %d %s", code, body)
	}
	var up struct {
		ID   int64  `json:"id"`
		URL  string `json:"url"`
		Mime string `json:"mime"`
	}
	if err := json.Unmarshal(body, &up); err != nil {
		t.Fatal(err)
	}
	if up.ID <= 0 || up.Mime != "video/mp4" || !strings.HasPrefix(up.URL, "api/speaking/videos/") {
		t.Fatalf("unexpected upload result: %+v", up)
	}

	code, body = speakingRequest(t, mux, "GET", "/"+up.URL, nil, "")
	if code != http.StatusOK {
		t.Fatalf("serve video: %d %s", code, body)
	}
	if !bytes.Equal(body, clip) {
		t.Fatalf("served bytes differ: got %d bytes", len(body))
	}

	// Range requests must work so the browser can seek in longer recordings.
	req := httptest.NewRequest("GET", "/"+up.URL, nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || len(rec.Body.Bytes()) != 10 {
		t.Fatalf("range: %d %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestSpeakingIngressUsesDedicatedToken(t *testing.T) {
	t.Setenv("ACCESS_PASSCODE", "site-passcode")
	t.Setenv("SPEAKING_INGEST_TOKEN", "ingest-token-123")
	_, mux := speakingTestDB(t)
	h := newAuthGate().wrap(mux)

	payload := []byte(`{"episode":3,"date":"2026-11-26","title":"测试","source":"中文原稿"}`)

	// The normal page endpoint still needs a site session.
	if code, body := speakingRequest(t, h, "POST", "/api/speaking/practices", payload, "application/json"); code != http.StatusUnauthorized {
		t.Fatalf("session endpoint without cookie = %d %s", code, body)
	}

	// The ingress bypasses the session gate but validates its own token.
	if code, body := speakingRequest(t, h, "POST", "/api/speaking/ingest", payload, "application/json"); code != http.StatusUnauthorized {
		t.Fatalf("ingest without token = %d %s", code, body)
	}

	bad := httptest.NewRequest("POST", "/api/speaking/ingest", bytes.NewReader(payload))
	bad.Header.Set("Content-Type", "application/json")
	bad.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bad)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ingest with wrong token = %d %s", rec.Code, rec.Body.String())
	}

	good := httptest.NewRequest("POST", "/api/speaking/ingest", bytes.NewReader(payload))
	good.Header.Set("Content-Type", "application/json")
	good.Header.Set("Authorization", "Bearer ingest-token-123")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, good)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest with valid token = %d %s", rec.Code, rec.Body.String())
	}

	// The token must not unlock any other API.
	if code, body := speakingRequest(t, h, "GET", "/api/speaking/practices", nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("token leaked to read endpoint: %d %s", code, body)
	}
}

func TestSpeakingIngressDisabledWithoutToken(t *testing.T) {
	t.Setenv("SPEAKING_INGEST_TOKEN", "")
	_, mux := speakingTestDB(t)
	payload := []byte(`{"episode":1,"date":"2026-11-27","title":"测试","source":"中文原稿"}`)
	if code, body := speakingRequest(t, mux, "POST", "/api/speaking/ingest", payload, "application/json"); code != http.StatusServiceUnavailable {
		t.Fatalf("disabled ingest = %d %s", code, body)
	}
}
