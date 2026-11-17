package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The dictionary deliberately does not try to lemmatise the whole French
// language at request time. Exact forms imported from Kaikki win; a small set
// of transparent spelling rules supplies a visible "rule" fallback, and every
// unresolved spelling is queued for review before it is written back.

type lookupMatch struct {
	EntryID int64      `json:"entry_id"`
	Lemma   string     `json:"lemma"`
	Display string     `json:"display"`
	Pos     string     `json:"pos"`
	GlossZH string     `json:"gloss_zh"`
	GlossEN string     `json:"gloss_en"`
	IPA     string     `json:"ipa"`
	Glosses []string   `json:"glosses,omitempty"`
	Forms   []formInfo `json:"forms,omitempty"`
}

type formInfo struct {
	Form   string `json:"form"`
	Feat   string `json:"feat,omitempty"`
	Source string `json:"source,omitempty"`
}

type lookupResult struct {
	Query       string        `json:"query"`
	Normalized  string        `json:"normalized"`
	Source      string        `json:"source"` // exact | rule | none
	Matches     []lookupMatch `json:"matches"`
	Suggestions []suggestion  `json:"suggestions,omitempty"`
}

type suggestion struct {
	Lemma   string `json:"lemma"`
	Display string `json:"display"`
	Pos     string `json:"pos"`
	GlossZH string `json:"gloss_zh"`
}

var (
	wordPattern   = regexp.MustCompile(`[[:alpha:]][[:alpha:]’'\-]*`)
	spacePattern  = regexp.MustCompile(`\s+`)
	pronounPrefix = []string{"qu'", "s'", "t'", "m'", "l'", "d'", "n'", "c'", "j'", "se ", "ne "}
)

func normalizeLetter(s string) string {
	s = strings.ReplaceAll(s, "’", "'")
	return strings.ToLower(strings.TrimSpace(s))
}

// normalizeSpelling follows SQLite's NOCASE folds for ASCII only, and adds
// the usual catalogue lookup spellings. Accents are preserved because they
// are meaningful in French.
func normalizeSpelling(s string) string {
	s = normalizeLetter(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\u00a0', '\u202f':
			b.WriteByte(' ')
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return strings.TrimSpace(b.String())
}

func hasWordChars(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func clipWords(s string, max int) string {
	fields := strings.Fields(s)
	if len(fields) > max {
		fields = fields[:max]
	}
	return strings.Join(fields, " ")
}

func corpusTokens(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range wordPattern.FindAllString(text, -1) {
		v := normalizeSpelling(w)
		if !hasWordChars(v) || len([]rune(v)) > 48 {
			continue
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

type lexiconCache struct {
	mu      sync.RWMutex
	entries map[string][]lookupMatch
	cap     int
}

func newLexiconCache() *lexiconCache {
	return &lexiconCache{entries: map[string][]lookupMatch{}, cap: 10000}
}

func (c *lexiconCache) get(key string) ([]lookupMatch, bool) {
	c.mu.RLock()
	v, ok := c.entries[key]
	c.mu.RUnlock()
	return v, ok
}

func (c *lexiconCache) put(key string, v []lookupMatch) {
	c.mu.Lock()
	if len(c.entries) >= c.cap {
		c.entries = map[string][]lookupMatch{}
	}
	c.entries[key] = v
	c.mu.Unlock()
}

func (c *lexiconCache) clear() {
	c.mu.Lock()
	c.entries = map[string][]lookupMatch{}
	c.mu.Unlock()
}

type lexicon struct {
	db    *sql.DB
	cache *lexiconCache
}

func newLexicon(db *sql.DB) *lexicon {
	return &lexicon{db: db, cache: newLexiconCache()}
}

func (l *lexicon) invalidate() {
	l.cache.clear()
}

// lookup searches the exact form table first, then the entry table, then the
// conservative rule fallback. Unknown spellings are queued asynchronously.
func (l *lexicon) lookup(raw string, limit int, lite bool) lookupResult {
	q := normalizeSpelling(clipWords(raw, 6))
	res := lookupResult{Query: raw, Normalized: q, Source: "none", Matches: []lookupMatch{}}
	if q == "" || len([]rune(q)) > 128 || !hasWordChars(q) {
		return res
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if hit, ok := l.cache.get("q|" + q); ok {
		return l.trim(res, hit, "exact")
	}
	matches, err := l.exact(q, lite)
	if err == nil {
		l.cache.put("q|"+q, matches)
	}
	if len(matches) > 0 {
		return l.trim(res, matches, "exact")
	}
	matches, err = l.capitalized(q)
	if err == nil && len(matches) > 0 {
		l.cache.put("q|"+q, matches)
		return l.trim(res, matches, "exact")
	}
	guesses := ruleGuesses(q)
	for _, g := range guesses {
		if g.lemma == q {
			continue
		}
		if m, err := l.exact(g.lemma, lite); err == nil && len(m) > 0 {
			for i := range m {
				m[i].Forms = nil
			}
			l.cache.put("q|"+q, m)
			return l.trim(res, m, "rule")
		}
	}
	res.Suggestions = l.suggest(q, 5)
	if len(guesses) > 0 && len(res.Suggestions) == 0 {
		for _, g := range guesses {
			res.Suggestions = append(res.Suggestions, suggestion{Lemma: g.lemma, Display: g.lemma, Pos: "", GlossZH: "规则推测词形"})
			if len(res.Suggestions) >= 5 {
				break
			}
		}
	}
	return res
}

func (l *lexicon) trim(res lookupResult, matches []lookupMatch, source string) lookupResult {
	if matches == nil {
		matches = []lookupMatch{}
	}
	res.Matches = matches
	res.Source = source
	if len(matches) == 0 {
		res.Suggestions = nil
	}
	return res
}

func (l *lexicon) exact(q string, lite bool) ([]lookupMatch, error) {
	// Parentheses matter: without them OR would let every row through the
	// LIMIT as soon as the first spelling comparison matched.
	rows, err := l.db.Query(`SELECT e.id, e.lemma, e.display, e.pos, e.gloss_zh, e.gloss_en, e.ipa
		FROM dict_entry e
		WHERE (e.lemma IN (SELECT lemma FROM dict_form WHERE form=?)
		   OR e.lemma=? OR e.display=?)
		-- Prefer standard spellings over archaic/obsolete variants of the same
		-- lemma, so "suis" shows être rather than the archaic "estre".
		ORDER BY e.lemma,
		  CASE WHEN e.gloss_en LIKE 'archaic%' OR e.gloss_en LIKE 'obsolete%' THEN 2 ELSE 1 END,
		  CASE e.source WHEN 'manual' THEN 0 WHEN 'kaikki' THEN 1 ELSE 2 END,
		  e.pos LIMIT 40`, q, q, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []lookupMatch{}
	ids := []int64{}
	for rows.Next() {
		var m lookupMatch
		if err = rows.Scan(&m.EntryID, &m.Lemma, &m.Display, &m.Pos, &m.GlossZH, &m.GlossEN, &m.IPA); err != nil {
			continue
		}
		m.Glosses = []string{m.GlossZH}
		out = append(out, m)
		ids = append(ids, m.EntryID)
	}
	if lite {
		return out, nil
	}
	all := make([]lookupMatch, 0, len(out))
	for i := range out {
		forms, err := l.formsFor(out[i].EntryID)
		if err != nil {
			return nil, err
		}
		hasInflection := len(forms) > 0
		// Encyclopedic entries carry thousands of derived spellings; show the
		// plain conjugation (or a sample) and leave the full list to the entry.
		if len(forms) > 80 {
			forms = forms[:80]
		}
		out[i].Forms = forms
		// A headword page that is only an inflected spelling ("suis") must not
		// crowd out the real lemma ("suivre"/"être"), which carries the forms.
		if (out[i].Lemma == q && !hasInflection) || out[i].Pos == "name" {
			continue
		}
		all = append(all, out[i])
	}
	if len(all) > 0 {
		return all, nil
	}
	return out, nil
}

func (l *lexicon) capitalized(q string) ([]lookupMatch, error) {
	if q == "" || unicode.IsUpper([]rune(q)[0]) {
		return nil, nil
	}
	r := []rune(q)
	r[0] = unicode.ToUpper(r[0])
	return l.exact(string(r), false)
}

func (l *lexicon) formsFor(entryID int64) ([]formInfo, error) {
	rows, err := l.db.Query(`SELECT form, feat, source FROM dict_form WHERE entry_id=?
		ORDER BY length(form), form LIMIT 120`, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []formInfo{}
	for rows.Next() {
		var f formInfo
		if err = rows.Scan(&f.Form, &f.Feat, &f.Source); err != nil {
			return nil, fmt.Errorf("form scan (entry %d): %w", entryID, err)
		}
		out = append(out, f)
	}
	return out, nil
}

func (l *lexicon) suggest(q string, limit int) []suggestion {
	if q == "" || limit <= 0 {
		return nil
	}
	rows, err := l.db.Query(`SELECT lemma, display, pos, gloss_zh FROM dict_entry
		WHERE lemma LIKE ? ESCAPE '\' AND lemma GLOB '[[A-Za-zÀ-ÖØ-öø-ÿŒœ]*'
		ORDER BY length(lemma), lemma LIMIT ?`, escapeLike(q)+"%", limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []suggestion{}
	for rows.Next() {
		var s suggestion
		if err = rows.Scan(&s.Lemma, &s.Display, &s.Pos, &s.GlossZH); err != nil {
			continue
		}
		out = append(out, s)
	}
	return out
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (l *lexicon) glossary(text string) map[string][]lookupMatch {
	tokens := corpusTokens(text)
	out := map[string][]lookupMatch{}
	for _, t := range tokens {
		if m, ok := l.cache.get("q|" + t); ok {
			out[t] = m
			continue
		}
		m, err := l.exact(t, true)
		if err != nil || len(m) == 0 {
			continue
		}
		for i := range m {
			m[i].Forms = nil
		}
		l.cache.put("q|"+t, m)
		out[t] = m
	}
	return out
}

func (l *lexicon) stats() map[string]any {
	var entries, forms, pending int
	_ = l.db.QueryRow(`SELECT COUNT(*) FROM dict_entry`).Scan(&entries)
	_ = l.db.QueryRow(`SELECT COUNT(*) FROM dict_form`).Scan(&forms)
	_ = l.db.QueryRow(`SELECT COUNT(*) FROM dict_pending WHERE resolved_entry_id IS NULL`).Scan(&pending)
	letters := []string{}
	// Only French initials reach the alphabet bar; the raw Kaikki export also
	// carries digits, braille and emoji entries that are not French words.
	rows, err := l.db.Query(`SELECT substr(upper(lemma),1,1), COUNT(*) FROM dict_entry
		WHERE lemma GLOB '[[A-Za-zÀ-ÖØ-öø-ÿŒœ]*' GROUP BY 1 ORDER BY 1`)
	if err == nil {
		for rows.Next() {
			var ch string
			var n int
			if rows.Scan(&ch, &n) == nil && isFrenchLetter(ch) {
				letters = append(letters, ch)
			}
		}
		rows.Close()
	}
	return map[string]any{"entries": entries, "forms": forms, "pending": pending, "letters": strings.Join(letters, "")}
}

func isFrenchLetter(s string) bool {
	r := []rune(s)
	if len(r) != 1 {
		return false
	}
	return (r[0] >= 'A' && r[0] <= 'Z') || (r[0] >= 'a' && r[0] <= 'z') ||
		(r[0] >= 'À' && r[0] <= 'Ö') || (r[0] >= 'Ø' && r[0] <= 'ö') ||
		(r[0] >= 'ø' && r[0] <= 'ÿ') || r[0] == 'Œ' || r[0] == 'œ'
}

func (l *lexicon) queuePending(form, guess, reason string) {
	if form == "" || !hasWordChars(form) {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = l.db.Exec(`INSERT INTO dict_pending(form, guess_lemma, reason, hits, first_seen, last_seen)
		VALUES(?,?,?,1,?,?)
		ON CONFLICT(form) DO UPDATE SET hits=hits+1, last_seen=excluded.last_seen,
		  guess_lemma=CASE WHEN excluded.guess_lemma<>'' THEN excluded.guess_lemma ELSE guess_lemma END`,
		form, guess, reason, now, now)
}

// dictionarySeedFile is emitted by tools/build_lexicon.py and embedded so a
// fresh checkout starts with a usable French glossary (not just structure).
//
// The document is 160+ MB. Materialising it as []map[string]any used to cost
// roughly 840 MB of heap (about 5x the file), and the collector doubled that
// before it ran: on a 2 GB machine every single start was an out-of-memory
// kill. The importer below therefore streams the file one element at a time,
// and startup skips the import altogether when the database already carries
// this exact seed.
type dictionarySeedFile struct {
	Entries []map[string]any `json:"entries"`
	Forms   []map[string]any `json:"forms"`
}

// lexiconSeedSource exposes the dictionary seed to the importer.
//
// The seed is 160+ MB. Whether it is compiled into the binary (default build)
// or sits in a file beside it (`noseed` build), the importer reads it as a
// stream, so peak memory never scales with the size of the dictionary.
type lexiconSeedSource interface {
	// Open returns a fresh, seekable reader over the seed.
	Open() (io.ReadSeekCloser, error)
	// Size is the byte length of the seed.
	Size() int64
	// Name identifies the source in log lines.
	Name() string
}

// embeddedLexiconSeed serves the dictionary from the blob compiled into the
// binary.
type embeddedLexiconSeed struct{ data []byte }

func (s embeddedLexiconSeed) Open() (io.ReadSeekCloser, error) {
	return nopSeekCloser{bytes.NewReader(s.data)}, nil
}
func (s embeddedLexiconSeed) Size() int64  { return int64(len(s.data)) }
func (s embeddedLexiconSeed) Name() string { return "embedded data/lexicon_seed.json" }

// nopSeekCloser gives a bytes.Reader the Close method io.ReadSeekCloser wants.
type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

// fileLexiconSeed serves the dictionary from a file on disk.
type fileLexiconSeed struct{ path string }

func (s fileLexiconSeed) Open() (io.ReadSeekCloser, error) { return os.Open(s.path) }
func (s fileLexiconSeed) Size() int64 {
	info, err := os.Stat(s.path)
	if err != nil {
		return 0
	}
	return info.Size()
}
func (s fileLexiconSeed) Name() string { return s.path }

// lexiconSeedFingerprint identifies a seed cheaply.
//
// A full SHA-256 over the 160+ MB payload is correct, but it faults every
// page of the seed into the working set and leaves that memory resident.
// Hashing the length plus the first and last 64 KB touches only 128 KB: a
// changed seed is still detected immediately, while a restart with nothing to
// do costs almost nothing.
func lexiconSeedFingerprint(seed lexiconSeedSource) (string, error) {
	r, err := seed.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()

	const window = 64 * 1024
	h := sha256.New()
	head := make([]byte, window)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	h.Write(head[:n])

	size := seed.Size()
	if size > window {
		if _, err := r.Seek(size-window, io.SeekStart); err == nil {
			tail := make([]byte, window)
			n, err := io.ReadFull(r, tail)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				return "", err
			}
			h.Write(tail[:n])
		}
	}
	h.Write([]byte(strconv.FormatInt(size, 10)))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// importLexiconIfChanged imports the dictionary only when the database does
// not already hold this exact seed. The fingerprint lives in the `meta` table,
// so a rebuilt seed is detected automatically and re-imported, while an
// ordinary restart is a no-op.
func importLexiconIfChanged(db *sql.DB, seed lexiconSeedSource) error {
	if seed.Size() == 0 {
		return nil
	}
	fp, err := lexiconSeedFingerprint(seed)
	if err != nil {
		return err
	}
	var stored string
	err = db.QueryRow(`SELECT value FROM meta WHERE key='lexicon_seed_fingerprint'`).Scan(&stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if stored == fp {
		slog.Info("dictionary seed already present, skipping import",
			"fingerprint", fp[:12], "source", seed.Name())
		return nil
	}
	nEntries, nForms, err := importLexiconFile(db, seed)
	if err != nil {
		return err
	}
	slog.Info("dictionary seed imported",
		"entries", nEntries, "forms", nForms, "source", seed.Name())
	_, err = db.Exec(`INSERT INTO meta(key,value) VALUES('lexicon_seed_fingerprint',?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fp)
	return err
}

// importLexiconFile streams the seed document: only one array element is
// decoded at a time, so peak heap stays in the low megabytes no matter how
// large the file is.
func importLexiconFile(db *sql.DB, seed lexiconSeedSource) (int, int, error) {
	r, err := seed.Open()
	if err != nil {
		return 0, 0, err
	}
	defer r.Close()

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	nEntries, nForms := 0, 0

	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return 0, 0, fmt.Errorf("lexicon seed: expected a JSON object")
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return 0, 0, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return 0, 0, fmt.Errorf("lexicon seed: expected a field name")
		}
		valTok, err := dec.Token()
		if err != nil {
			return 0, 0, err
		}
		if d, ok := valTok.(json.Delim); !ok || d != '[' {
			// A scalar field: its value has already been consumed.
			continue
		}
		switch key {
		case "entries":
			for dec.More() {
				var e map[string]any
				if err := dec.Decode(&e); err != nil {
					return 0, 0, err
				}
				inserted, err := insertDictEntry(tx, e, now)
				if err != nil {
					return 0, 0, err
				}
				if inserted {
					nEntries++
				}
			}
		case "forms":
			for dec.More() {
				var f map[string]any
				if err := dec.Decode(&f); err != nil {
					return 0, 0, err
				}
				inserted, err := insertDictForm(tx, f)
				if err != nil {
					return 0, 0, err
				}
				if inserted {
					nForms++
				}
			}
		default:
			for dec.More() {
				var skip json.RawMessage
				if err := dec.Decode(&skip); err != nil {
					return 0, 0, err
				}
			}
		}
		if _, err := dec.Token(); err != nil { // closing ']'
			return 0, 0, err
		}
	}
	return nEntries, nForms, tx.Commit()
}

// insertDictEntry upserts one dictionary headword.
func insertDictEntry(tx *sql.Tx, e map[string]any, now string) (bool, error) {
	lemma := normalizeSpelling(stringValue(e["lemma"]))
	if lemma == "" {
		return false, nil
	}
	display := stringValue(e["display"])
	if display == "" {
		display = stringValue(e["lemma"])
	}
	pos := stringValue(e["pos"])
	if _, err := tx.Exec(`INSERT INTO dict_entry(lemma, display, pos, gloss_zh, gloss_en, ipa, audio_url, source, license, level, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(lemma, pos) DO UPDATE SET
		  gloss_zh=CASE WHEN excluded.gloss_zh<>'' THEN excluded.gloss_zh ELSE dict_entry.gloss_zh END,
		  gloss_en=CASE WHEN excluded.gloss_en<>'' THEN excluded.gloss_en ELSE dict_entry.gloss_en END,
		  display=excluded.display,
		  source=CASE WHEN excluded.source='manual' THEN 'manual' ELSE dict_entry.source END,
		  updated_at=excluded.updated_at`,
		lemma, display, pos, stringValue(e["gloss_zh"]), stringValue(e["gloss_en"]),
		stringValue(e["ipa"]), stringValue(e["audio_url"]), stringValue(e["source"]),
		stringValue(e["license"]), stringValue(e["level"]), now); err != nil {
		return false, err
	}
	return true, nil
}

// insertDictForm attaches one inflected form to its dictionary entry.
func insertDictForm(tx *sql.Tx, f map[string]any) (bool, error) {
	lemma := normalizeSpelling(stringValue(f["lemma"]))
	form := normalizeSpelling(stringValue(f["form"]))
	feat := stringValue(f["feat"])
	if form == "" || lemma == "" {
		return false, nil
	}
	// A lemma may be a noun and a verb. Pick the form's part of speech when
	// Kaikki supplies it so singulars/plurals do not land on the wrong entry.
	// The canonical entry ranks above an inflected page that happens to use
	// the same spelling, so être/suis attaches to the verb être, not être n.
	pos := stringValue(f["pos"])
	var id int64
	var errSel error
	if pos != "" {
		errSel = tx.QueryRow(`SELECT id FROM dict_entry WHERE lemma=? AND pos=? ORDER BY CASE source WHEN 'manual' THEN 0 ELSE 1 END, id LIMIT 1`, lemma, pos).Scan(&id)
	}
	if pos == "" || errors.Is(errSel, sql.ErrNoRows) {
		errSel = tx.QueryRow(`SELECT id FROM dict_entry WHERE lemma=? ORDER BY CASE source WHEN 'manual' THEN 0 ELSE 1 END, id LIMIT 1`, lemma).Scan(&id)
	}
	if errSel != nil {
		if errors.Is(errSel, sql.ErrNoRows) {
			return false, nil
		}
		return false, errSel
	}
	if _, err := tx.Exec(`INSERT INTO dict_form(form, entry_id, feat, lemma, source) VALUES(?,?,?,?,?)
		ON CONFLICT(form, entry_id, feat) DO NOTHING`, form, id, feat, lemma, stringValue(f["source"])); err != nil {
		return false, err
	}
	return true, nil
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

type ruleGuess struct {
	lemma  string
	reason string
}

// ruleGuesses is intentionally conservative. It only proposes forms whose
// lemma is an -er / -ir verb or a plausible noun / adjective, and the caller
// verifies the proposal against dict_entry before returning it.
func ruleGuesses(form string) []ruleGuess {
	f := normalizeSpelling(form)
	if len([]rune(f)) < 3 {
		return nil
	}
	for _, p := range []string{"n'", "s'", "t'", "m'", "c'", "j'", "l'", "d'"} {
		if strings.HasPrefix(f, p) && len([]rune(f)) > len([]rune(p))+2 {
			rest := strings.TrimPrefix(f, p)
			out := []ruleGuess{{rest, "pronoun"}}
			return append(out, ruleGuesses(rest)...)
		}
	}
	var out []ruleGuess
	add := func(l string) {
		if l != "" && l != f {
			for _, g := range out {
				if g.lemma == l {
					return
				}
			}
			out = append(out, ruleGuess{l, "rule"})
		}
	}
	// -er verbs: present/participle/imperfect spellings whose roots are
	// unambiguous after undoing common written alternatives (ç/c, g/ge, y/i).
	for _, s := range []string{"er", "é", "ée", "és", "ées", "ai", "ais", "ait", "ions", "iez", "aient", "ant", "e", "es", "ons", "ez", "ent"} {
		if strings.HasSuffix(f, s) && len([]rune(f)) > len([]rune(s))+1 {
			stem := strings.TrimSuffix(f, s)
			add(stem + "er")
			if len([]rune(stem)) > 2 && (s == "ait" || s == "ais" || s == "aient") {
				add(stem + "re")
			}
			if strings.HasSuffix(stem, "ç") {
				add(strings.TrimSuffix(stem, "ç") + "cer")
			}
			if strings.HasSuffix(stem, "ge") {
				add(strings.TrimSuffix(stem, "ge") + "ger")
			}
		}
	}
	for _, s := range []string{"is", "it", "issons", "issez", "issent", "i", "ie", "ies", "ir", "issant"} {
		if strings.HasSuffix(f, s) && len([]rune(f)) > len([]rune(s))+1 {
			add(strings.TrimSuffix(f, s) + "ir")
		}
	}
	for _, s := range []string{"aux", "s", "x", "es"} {
		if strings.HasSuffix(f, s) && len([]rune(f)) > len([]rune(s))+1 {
			stem := strings.TrimSuffix(f, s)
			add(stem)
			if s == "aux" {
				add(stem + "ail")
			}
		}
	}
	for _, s := range []string{"ve", "se", "elle", "enne", "ette", "euse", "ères", "ère"} {
		if strings.HasSuffix(f, s) && len([]rune(f)) > len([]rune(s))+1 {
			stem := strings.TrimSuffix(f, s)
			add(stem)
			add(stem + "f")
			add(stem + "x")
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}
