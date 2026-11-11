package main

import (
	"path/filepath"
	"testing"
)

// Guards the content pipeline: every embedded chapter file must load without
// SQL errors, and re-seeding must be idempotent.
func TestSeedAllChapters(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.db")
	for round := 1; round <= 2; round++ {
		db, err := openDB(p)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		var chapters, ready, blocks, exercises, questions, imageEx int
		_ = db.QueryRow(`SELECT COUNT(*) FROM chapter`).Scan(&chapters)
		_ = db.QueryRow(`SELECT COUNT(*) FROM chapter WHERE status='ready'`).Scan(&ready)
		_ = db.QueryRow(`SELECT COUNT(*) FROM content_block`).Scan(&blocks)
		_ = db.QueryRow(`SELECT COUNT(*) FROM exercise`).Scan(&exercises)
		_ = db.QueryRow(`SELECT COUNT(*) FROM question`).Scan(&questions)
		_ = db.QueryRow(`SELECT COUNT(*) FROM exercise WHERE has_image=1`).Scan(&imageEx)
		db.Close()
		t.Logf("round %d: chapters=%d ready=%d blocks=%d exercises=%d questions=%d image_exercises=%d",
			round, chapters, ready, blocks, exercises, questions, imageEx)
		if chapters != 77 {
			t.Fatalf("expected 77 chapters, got %d", chapters)
		}
		if questions == 0 || blocks == 0 {
			t.Fatal("no content seeded")
		}
	}
}
