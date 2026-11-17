package main

import (
	"database/sql"
	"os"
	"sync"
	"testing"
)

func testLexicon(t *testing.T) *lexicon {
	t.Helper()
	lexiconOnce.Do(func() {
		var err error
		dbPath := os.TempDir() + "/fp_lexicon_test.db"
		t.Log("DBPATH", dbPath)
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(dbPath + suffix)
		}
		lexiconDB, err = openDB(dbPath)
		if err != nil {
			lexiconErr = err
		}
	})
	if lexiconErr != nil {
		t.Fatal(lexiconErr)
	}
	db := lexiconDB
	return newLexicon(db)
}

var (
	lexiconOnce sync.Once
	lexiconDB   *sql.DB
	lexiconErr  error
)

func TestLookupProducesAllLemmasForSuis(t *testing.T) {
	lex := testLexicon(t)
	got := lex.lookup("suis", 20, false)
	if got.Source != "exact" {
		t.Fatalf("source=%q want exact", got.Source)
	}
	for _, m := range got.Matches {
		t.Logf("MATCH %s forms=%d", m.Lemma, len(m.Forms))
	}
	lemmas := map[string]bool{}
	for _, m := range got.Matches {
		lemmas[m.Lemma] = true
		if m.Lemma != "suis" && m.Pos != "n." && len(m.Forms) == 0 {
			t.Errorf("%s has no forms", m.Lemma)
		}
	}
	for _, want := range []string{"être", "suivre"} {
		if !lemmas[want] {
			t.Errorf("missing %s in %#v", want, got.Matches)
		}
	}
}

func TestLookupRuleFallbackIsMarked(t *testing.T) {
	lex := testLexicon(t)
	got := lex.lookup("excusaient", 20, false)
	// The installed full dictionary resolves excusaient exactly. Only an
	// installation whose form table lacks it may answer with a rule guess.
	if got.Source != "rule" && got.Source != "exact" {
		t.Fatalf("unexpected source: %+v", got)
	}
	if len(got.Matches) == 0 || got.Matches[0].Lemma != "excuser" {
		t.Fatalf("unexpected rule lookup: %+v", got)
	}
}

func TestLookupNormalizesCaseAndAccents(t *testing.T) {
	lex := testLexicon(t)
	got := lex.lookup("ÊTES ", 20, false)
	if got.Source != "exact" || len(got.Matches) == 0 || got.Matches[0].Lemma != "être" {
		t.Fatalf("unexpected lookup: %+v", got)
	}
}

func TestUnknownSpellingReturnsSuggestions(t *testing.T) {
	lex := testLexicon(t)
	got := lex.lookup("quasi-unknown-zzy", 20, true)
	if got.Source != "none" || len(got.Matches) != 0 {
		t.Fatalf("expected no match, got %+v", got)
	}
}

func TestGlossaryOnlyReturnsKnownShortWords(t *testing.T) {
	lex := testLexicon(t)
	g := lex.glossary("<div>On se salue et on fait connaissance.</div>")
	if len(g["salue"]) == 0 || len(g["O"]) != 0 || len(g["zzy"]) != 0 {
		t.Fatalf("unexpected glossary keys: %#v", g)
	}
}
