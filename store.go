package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed data/chapter01.json
var seedJSON []byte

//go:embed data/vocabulaire01.json
var vocabSeedJSON []byte

// One file per chapter for chapters 2+ of each course. Every one is a
// verbatim transcription of the printed book pages; ID assignment is done
// here so content files never carry database identifiers.
//
//go:embed data/chapters
var chapterFS embed.FS

//go:embed web
var webFS embed.FS

func webAssets() (fs.FS, error) { return fs.Sub(webFS, "web") }

type seedCourse struct {
	Code       string `json:"code"`
	TitleFR    string `json:"title_fr"`
	Level      string `json:"level"`
	SourceBook string `json:"source_book"`
}

type seedChapter struct {
	Seq       int    `json:"seq"`
	TitleFR   string `json:"title_fr"`
	PageStart int    `json:"page_start"`
}

type seedSection struct {
	TitleFR string `json:"title_fr"`
	PageNo  int    `json:"page_no"`
}

type seedBlock struct {
	ID       int    `json:"id"`
	PageNo   int    `json:"page_no"`
	Seq      int    `json:"seq"`
	Type     string `json:"type"`
	BodyHTML string `json:"body_html"`
}

type seedQuestion struct {
	ID       int    `json:"id"`
	Seq      int    `json:"seq"`
	Type     string `json:"type"`
	StemHTML string `json:"stem_html"`
}

type seedExercise struct {
	ID            int            `json:"id"`
	PageNo        int            `json:"page_no"`
	Seq           int            `json:"seq"`
	InstructionFR string         `json:"instruction_fr"`
	Label         string         `json:"label"`
	NoteHTML      string         `json:"note_html"`
	Questions     []seedQuestion `json:"questions"`
}

type seedChapterDetail struct {
	PageStart int            `json:"page_start"`
	PageEnd   int            `json:"page_end"`
	PDFStart  int            `json:"pdf_start"`
	PDFEnd    int            `json:"pdf_end"`
	Sections  []seedSection  `json:"sections"`
	Blocks    []seedBlock    `json:"blocks"`
	Exercises []seedExercise `json:"exercises"`
}

type seedFile struct {
	Course   seedCourse        `json:"course"`
	Chapters []seedChapter     `json:"chapters"`
	Chapter1 seedChapterDetail `json:"chapter1"`
}

// New-format per-chapter file. IDs are derived deterministically from
// course + seq, so adding a chapter never disturbs existing notes/attempts.
type chapterSeed struct {
	Course    string `json:"course"`
	Seq       int    `json:"seq"`
	TitleFR   string `json:"title_fr"`
	PageStart int    `json:"page_start"`
	PageEnd   int    `json:"page_end"`
	PDFStart  int    `json:"pdf_start"`
	PDFEnd    int    `json:"pdf_end"`

	Sections []struct {
		TitleFR string `json:"title_fr"`
		PageNo  int    `json:"page_no"`
	} `json:"sections"`

	Blocks []struct {
		PageNo   int    `json:"page_no"`
		Seq      int    `json:"seq"`
		Type     string `json:"type"`
		BodyHTML string `json:"body_html"`
	} `json:"blocks"`

	Exercises []struct {
		PageNo        int    `json:"page_no"`
		Seq           int    `json:"seq"`
		InstructionFR string `json:"instruction_fr"`
		Label         string `json:"label"`
		NoteHTML      string `json:"note_html"`
		HasImage      bool   `json:"has_image"`
		ScanPages     []int  `json:"scan_pages"`
		Questions     []struct {
			Seq      int    `json:"seq"`
			Type     string `json:"type"`
			StemHTML string `json:"stem_html"`
		} `json:"questions"`
	} `json:"exercises"`
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma: %w", err)
	}
	if _, err = db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err = seed(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("seed: %w", err)
	}
	return db, nil
}

// migrate keeps older database files usable when new columns are introduced.
func migrate(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(exercise)`)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err = rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	for _, col := range []struct{ name, ddl string }{
		{"has_image", "ALTER TABLE exercise ADD COLUMN has_image INTEGER NOT NULL DEFAULT 0"},
		{"scan_pages", "ALTER TABLE exercise ADD COLUMN scan_pages TEXT NOT NULL DEFAULT ''"},
	} {
		if !have[col.name] {
			if _, err = db.Exec(col.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

// seed is idempotent: all content rows carry stable explicit IDs, so re-running
// never invalidates existing notes or attempts. Each course owns a disjoint ID
// range (grammar 1..999, vocabulary 1000+), so courses never collide.
func seed(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err = seedOne(tx, seedJSON, 1, 0, 0); err != nil {
		return fmt.Errorf("grammaire: %w", err)
	}
	if err = seedOne(tx, vocabSeedJSON, 2, 1000, 2000); err != nil {
		return fmt.Errorf("vocabulaire: %w", err)
	}

	names, err := fs.Glob(chapterFS, "data/chapters/*.json")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := chapterFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err = seedChapterFile(tx, raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return tx.Commit()
}

func seedOne(tx *sql.Tx, raw []byte, courseID, chapterBase, sectionBase int) error {
	var data seedFile
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	detailChapterID := chapterBase + 1

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO course(id, code, title_fr, level, source_book) VALUES(?,?,?,?,?)`,
		courseID, data.Course.Code, data.Course.TitleFR, data.Course.Level, data.Course.SourceBook,
	); err != nil {
		return err
	}

	for _, c := range data.Chapters {
		status := "pending"
		var pageEnd, pdfStart, pdfEnd any
		if c.Seq == 1 {
			status = "ready"
			pageEnd = data.Chapter1.PageEnd
			pdfStart = data.Chapter1.PDFStart
			pdfEnd = data.Chapter1.PDFEnd
		}
		if _, err := tx.Exec(
			`INSERT INTO chapter(id, course_id, seq, title_fr, page_start, page_end, pdf_start, pdf_end, status)
			 VALUES(?,?,?,?,?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET title_fr=excluded.title_fr, page_start=excluded.page_start,
			   page_end=excluded.page_end, pdf_start=excluded.pdf_start, pdf_end=excluded.pdf_end, status=excluded.status`,
			chapterBase+c.Seq, courseID, c.Seq, c.TitleFR, c.PageStart, pageEnd, pdfStart, pdfEnd, status,
		); err != nil {
			return err
		}
	}

	for i, s := range data.Chapter1.Sections {
		if _, err := tx.Exec(
			`INSERT INTO chapter_section(id, chapter_id, seq, title_fr, page_no) VALUES(?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET title_fr=excluded.title_fr, page_no=excluded.page_no`,
			sectionBase+i+1, detailChapterID, i+1, s.TitleFR, s.PageNo,
		); err != nil {
			return err
		}
	}

	for _, b := range data.Chapter1.Blocks {
		ref := fmt.Sprintf("p. %d", b.PageNo)
		if _, err := tx.Exec(
			`INSERT INTO content_block(id, chapter_id, page_no, seq, type, body_html, source_ref) VALUES(?,?,?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET body_html=excluded.body_html, type=excluded.type, seq=excluded.seq, page_no=excluded.page_no`,
			b.ID, detailChapterID, b.PageNo, b.Seq, b.Type, b.BodyHTML, ref,
		); err != nil {
			return err
		}
	}

	for _, e := range data.Chapter1.Exercises {
		ref := fmt.Sprintf("p. %d, ex. %d", e.PageNo, e.Seq)
		if _, err := tx.Exec(upsertExerciseSQL(),
			e.ID, detailChapterID, e.PageNo, e.Seq, e.InstructionFR, e.Label, e.NoteHTML, ref, 0, "",
		); err != nil {
			return err
		}
		for _, q := range e.Questions {
			qref := fmt.Sprintf("%s, q. %d", ref, q.Seq)
			if _, err := tx.Exec(
				`INSERT INTO question(id, exercise_id, seq, type, stem_html, source_ref, answer_json, answer_status, tolerance, grading_mode)
				 VALUES(?,?,?,?,?,?,NULL,'pending','normal','auto')
				 ON CONFLICT(id) DO UPDATE SET seq=excluded.seq, type=excluded.type,
				   stem_html=excluded.stem_html, source_ref=excluded.source_ref`,
				q.ID, e.ID, q.Seq, q.Type, q.StemHTML, qref,
			); err != nil {
				return err
			}
		}
	}

	slog.Info("course seeded", "code", data.Course.Code,
		"chapters", len(data.Chapters), "blocks", len(data.Chapter1.Blocks),
		"exercises", len(data.Chapter1.Exercises))
	return nil
}

func upsertExerciseSQL() string {
	return `INSERT INTO exercise(id, chapter_id, page_no, seq, instruction_fr, label, note_html, source_ref, has_image, scan_pages)
		 VALUES(?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET instruction_fr=excluded.instruction_fr, label=excluded.label,
		   note_html=excluded.note_html, source_ref=excluded.source_ref, page_no=excluded.page_no,
		   seq=excluded.seq, has_image=excluded.has_image, scan_pages=excluded.scan_pages`
}

// idBand returns disjoint ID bases for (course, entity). Each chapter gets a
// 1000-wide slot, far more than any chapter needs for questions or blocks.
func idBand(course string, seq int) (chapterID, sectionID, blockID, exerciseID, questionID int) {
	slot := seq * 1000
	if course == "grammaire" {
		return seq, 1_000_000 + slot, 2_000_000 + slot, 3_000_000 + slot, 4_000_000 + slot
	}
	return 1000 + seq, 5_000_000 + slot, 6_000_000 + slot, 7_000_000 + slot, 8_000_000 + slot
}

func seedChapterFile(tx *sql.Tx, raw []byte) error {
	var d chapterSeed
	if err := json.Unmarshal(raw, &d); err != nil {
		return err
	}
	if d.Seq < 2 || (d.Course != "grammaire" && d.Course != "vocabulaire") {
		return fmt.Errorf("bad course/seq: %s/%d", d.Course, d.Seq)
	}
	courseID := 1
	if d.Course == "vocabulaire" {
		courseID = 2
	}
	chapterID, sectionID, blockID, exerciseID, questionID := idBand(d.Course, d.Seq)

	if _, err := tx.Exec(
		`UPDATE chapter SET title_fr=?, page_start=?, page_end=?, pdf_start=?, pdf_end=?, status='ready'
		 WHERE id=? AND course_id=?`,
		d.TitleFR, d.PageStart, d.PageEnd, d.PDFStart, d.PDFEnd, chapterID, courseID,
	); err != nil {
		return err
	}

	for i, s := range d.Sections {
		if _, err := tx.Exec(
			`INSERT INTO chapter_section(id, chapter_id, seq, title_fr, page_no) VALUES(?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET title_fr=excluded.title_fr, page_no=excluded.page_no`,
			sectionID+i+1, chapterID, i+1, s.TitleFR, s.PageNo,
		); err != nil {
			return err
		}
	}
	for i, b := range d.Blocks {
		if _, err := tx.Exec(
			`INSERT INTO content_block(id, chapter_id, page_no, seq, type, body_html, source_ref) VALUES(?,?,?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET body_html=excluded.body_html, type=excluded.type, seq=excluded.seq, page_no=excluded.page_no`,
			blockID+i+1, chapterID, b.PageNo, b.Seq, b.Type, b.BodyHTML,
			fmt.Sprintf("p. %d", b.PageNo),
		); err != nil {
			return err
		}
	}

	qIndex := 0
	for i, e := range d.Exercises {
		eid := exerciseID + i + 1
		ref := fmt.Sprintf("p. %d, ex. %d", e.PageNo, e.Seq)
		hasImage := 0
		if e.HasImage {
			hasImage = 1
		}
		parts := make([]string, 0, len(e.ScanPages))
		for _, p := range e.ScanPages {
			parts = append(parts, strconv.Itoa(p))
		}
		if _, err := tx.Exec(upsertExerciseSQL(),
			eid, chapterID, e.PageNo, e.Seq, e.InstructionFR, e.Label, e.NoteHTML, ref,
			hasImage, strings.Join(parts, ","),
		); err != nil {
			return err
		}
		for j, q := range e.Questions {
			qIndex++
			qref := fmt.Sprintf("%s, q. %d", ref, q.Seq)
			if _, err := tx.Exec(
				`INSERT INTO question(id, exercise_id, seq, type, stem_html, source_ref, answer_json, answer_status, tolerance, grading_mode)
				 VALUES(?,?,?,?,?,?,NULL,'pending','normal','auto')
				 ON CONFLICT(id) DO UPDATE SET seq=excluded.seq, type=excluded.type,
				   stem_html=excluded.stem_html, source_ref=excluded.source_ref`,
				questionID+qIndex, eid, q.Seq, q.Type, q.StemHTML, qref,
			); err != nil {
				return err
			}
			_ = j
		}
	}
	slog.Info("chapter seeded", "course", d.Course, "seq", d.Seq,
		"blocks", len(d.Blocks), "exercises", len(d.Exercises), "questions", qIndex)
	return nil
}
