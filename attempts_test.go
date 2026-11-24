package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// newTestDB opens a fresh seeded database per test. Seeding all chapters takes
// a couple of seconds, so the suite stays small but real.
func newTestDB(t *testing.T) *string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "attempts.db")
	return &p
}

// TestDraftResumeLifecycle follows one learner: save a draft, reopen it,
// submit, get graded once the key exists, restart, and keep the old version.
func TestDraftResumeLifecycle(t *testing.T) {
	p := *newTestDB(t)
	db, err := openDB(p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const ch = 1

	empty, err := buildDraftView(db, ch)
	if err != nil {
		t.Fatal(err)
	}
	if empty["attempt_id"] != nil {
		t.Fatalf("a chapter with no answers must not offer a resume: %v", empty)
	}

	answers := answerMap{1: {"1": "êtes", "2": "Je", "3": "viens", "4": "venez", "5": "Je", "6": "et", "7": "suis"}}
	draft, err := saveDraft(db, ch, answers)
	if err != nil {
		t.Fatal(err)
	}
	if !draft.isDraft() || draft.draftVersion() != 1 {
		t.Fatalf("unexpected draft: %+v", draft)
	}
	// Saving twice must update the same draft, never create a second one.
	// The browser always sends every widget, so an untouched blank arrives as
	// an explicit empty value and must not erase the rest.
	again, err := saveDraft(db, ch, answerMap{1: {"1": "êtes", "2": "Je", "3": "viens", "4": "venez", "5": "Je", "6": "et", "7": "suis"}})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != draft.ID {
		t.Fatalf("draft was split: %d vs %d", again.ID, draft.ID)
	}
	view, err := buildDraftView(db, ch)
	if err != nil {
		t.Fatal(err)
	}
	if got := view["answers"].(map[string]map[string]string)["1"]["1"]; got != "êtes" {
		t.Fatalf("resume lost the latest value: %q", got)
	}
	if view["answers"].(map[string]map[string]string)["1"]["3"] != "viens" {
		t.Fatal("resume lost untouched blanks")
	}

	sub, err := submitAttempt(db, draft.ID, answers, "submit")
	if err != nil {
		t.Fatal(err)
	}
	if sub["version"] != 1 || sub["grading_status"] != "pending" {
		t.Fatalf("first submit without a key must be saved, not graded: %v", sub)
	}
	hist, err := revisionList(db, ch)
	if err != nil || len(hist) != 1 || hist[0]["label"] != "v1" {
		t.Fatalf("revision history missing: %v %v", hist, err)
	}

	// Importing the key, then regrading, changes the score of the old version
	// without touching a single character of it.
	key := `{"1":["êtes"],"2":["Je"],"3":["viens"],"4":["venez"],"5":["Je"],"6":["et"],"7":["suis"]}`
	if _, err = db.Exec(`UPDATE question SET answer_json=?, answer_status='ready', explanation='être 和 venir' WHERE id=1`, key); err != nil {
		t.Fatal(err)
	}
	out, err := regradeChapter(db, ch)
	if err != nil {
		t.Fatal(err)
	}
	if out["graded"] != 1 || out["changed"] != 1 {
		t.Fatalf("regrade result: %v", out)
	}
	rev, items, err := revisionDetail(db, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rev["grading_status"] != "graded" || rev["score"].(float64) != 1 {
		t.Fatalf("version not graded: %v", rev)
	}
	// The test key covers all seven blanks: five are empty, two are right.
	if rev["total"].(float64) != 7 {
		t.Fatalf("blank denominator drifted: %v", rev)
	}
	if items[0]["blanks"].(map[string]string)["3"] != "viens" {
		t.Fatal("regrade rewrote the frozen answers")
	}

	// Editing the graded paper resumes it as a v2 draft (the same behaviour
	// as the browser auto-save), and 重新作答 preserves that draft as v2.
	if resumed, err := saveDraft(db, ch, answers); err != nil {
		t.Fatal(err)
	} else if _, err = submitAttempt(db, resumed.ID, answerMap{}, "auto"); err != nil {
		t.Fatal(err)
	}
	fresh, err := saveDraft(db, ch, answerMap{})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == draft.ID || fresh.draftVersion() != 3 {
		t.Fatalf("restart did not open a new draft: %+v", fresh)
	}
	if hist, err = revisionList(db, ch); err != nil || len(hist) != 2 {
		t.Fatalf("restart lost a version: %v %v", hist, err)
	}
	// Submitting the empty draft records v3 with an empty paper, which is
	// still a real history entry.
	sub2, err := submitAttempt(db, fresh.ID, answerMap{}, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if sub2["version"] != 3 {
		t.Fatalf("version numbering skipped after restart: %v", sub2)
	}
}

// TestFrozenVerdicts checks per-blank marks: right, wrong and blank.
func TestFrozenVerdicts(t *testing.T) {
	db, err := openDB(*newTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := `{"1":["êtes"],"2":["Je","je"],"3":["viens"]}`
	if _, err = db.Exec(`UPDATE question SET answer_json=?, answer_status='ready' WHERE id=1`, key); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO attempt(user_id, chapter_id, scope, mode, started_at, status) VALUES(1,1,'chapter','lesson','2026-01-01T00:00:00Z','in_progress')`); err != nil {
		t.Fatal(err)
	}
	var id int64
	_ = db.QueryRow(`SELECT MAX(id) FROM attempt`).Scan(&id)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	s, tot, all, err := gradeFrozen(tx, id, answerMap{1: {"1": "êtes", "2": "nous", "3": "venons"}})
	if err != nil {
		t.Fatal(err)
	}
	// Each blank is one point, so the frozen score is 1 of 3.
	if !all || s != 1.0/3.0 || tot != 3 {
		t.Fatalf("expected one of three correct, got %v/%v ready=%v", s, tot, all)
	}
	vv := verdictsFrom(tx, id, answerMap{1: {"1": "êtes", "2": "nous", "3": "venons"}})
	var marks map[string]map[string]string
	if err := json.Unmarshal([]byte(vv), &marks); err != nil {
		t.Fatal(err)
	}
	if marks["1"]["1"] != "ok" || marks["1"]["2"] != "wrong" || marks["1"]["3"] != "wrong" {
		t.Fatalf("verdict marks wrong: %s", vv)
	}
}

// TestAnswerJSONStable guards an invariant the revision dedup relies on.
func TestAnswerJSONStable(t *testing.T) {
	a := answerMap{2: {"b": "x", "a": "y"}, 1: {"1": "z"}}
	b := answerMap{1: {"1": "z"}, 2: {"a": "y", "b": "x"}}
	if answerJSON(a) != answerJSON(b) {
		t.Fatalf("map order leaked into storage: %s vs %s", answerJSON(a), answerJSON(b))
	}
	back, err := parseAnswerJSON(answerJSON(a))
	if err != nil || back[2]["a"] != "y" || back[1]["1"] != "z" {
		t.Fatalf("round trip failed: %v %v", back, err)
	}
	if !strings.Contains(answerJSON(a), `"blanks":{"a":"y","b":"x"}`) {
		t.Fatal("blank keys must be ordered")
	}
}

// A restarted paper is a fresh sheet: section submissions of the old paper
// must stop releasing their answer keys until that section is resubmitted.
func TestRestartHidesReleasedKeys(t *testing.T) {
	db, err := openDB(*newTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const ch int64 = 1
	var exID int64
	if err = db.QueryRow(`SELECT exercise_id FROM question WHERE id=1`).Scan(&exID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE question SET answer_json=?, answer_status='ready' WHERE id=1`, `{"1":["êtes"]}`); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerRoutes(mux, db)
	answers := answerMap{1: {"1": "êtes"}}
	released := func(want bool, stage string) {
		t.Helper()
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest("GET", "/api/chapters/1/exercises", nil))
		if rr.Code != 200 {
			t.Fatalf("%s: status %d: %s", stage, rr.Code, rr.Body.String())
		}
		var d struct {
			Exercises []struct {
				ID         int64          `json:"id"`
				Submission map[string]any `json:"submission"`
				Questions  []struct {
					ID     int64             `json:"id"`
					Answer map[string]string `json:"answer"`
				} `json:"questions"`
			} `json:"exercises"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		got := false
		for _, ex := range d.Exercises {
			if ex.ID != exID {
				continue
			}
			if ex.Submission != nil {
				got = true
			}
			for _, q := range ex.Questions {
				if q.ID == 1 && len(q.Answer) > 0 {
					got = true
				}
			}
		}
		if got != want {
			t.Fatalf("%s: key released = %v, want %v", stage, got, want)
		}
	}

	draft, err := saveDraft(db, ch, answers)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = submitExercise(db, ch, exID, answers, "submit"); err != nil {
		t.Fatal(err)
	}
	released(true, "after section submit")

	if _, err = submitAttempt(db, draft.ID, answerMap{}, "auto"); err != nil {
		t.Fatal(err)
	}
	if _, err = saveDraft(db, ch, answerMap{}); err != nil {
		t.Fatal(err)
	}
	released(false, "after restart")

	if _, err = submitExercise(db, ch, exID, answers, "submit"); err != nil {
		t.Fatal(err)
	}
	released(true, "after resubmitting the section")
}
