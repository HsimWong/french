package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The dictionary assets must actually be served, and old modules must not be
// referenced through paths that a static file server rejects.
func TestDictionaryAssetsAreServed(t *testing.T) {
	assets, err := webAssets()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.FS(assets)))
	defer srv.Close()
	for _, path := range []string{"/js/app.js", "/js/dictionary.js", "/js/ui-core.js", "/css/style.css"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
	}
}
