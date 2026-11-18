package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func pathID(r *http.Request, name string) (int64, error) {
	s := r.PathValue(name)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("invalid id")
	}
	return n, nil
}

func registerRoutes(mux *http.ServeMux, db *sql.DB) {
	handleAttemptRoutes(mux, db)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "time": time.Now().Format(time.RFC3339)})
	})

	// List every course with its own counters. Course IDs are stable (1 = grammaire,
	// 2 = vocabulaire) and chapter IDs never overlap between courses.
	mux.HandleFunc("GET /api/courses", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`SELECT c.id, c.code, c.title_fr, c.level, c.source_book,
			(SELECT COUNT(*) FROM chapter ch WHERE ch.course_id=c.id),
			(SELECT COUNT(*) FROM chapter ch WHERE ch.course_id=c.id AND ch.status='ready'),
			(SELECT COUNT(*) FROM content_block b JOIN chapter ch ON ch.id=b.chapter_id WHERE ch.course_id=c.id),
			(SELECT COUNT(*) FROM exercise e JOIN chapter ch ON ch.id=e.chapter_id WHERE ch.course_id=c.id),
			(SELECT COUNT(*) FROM note n JOIN chapter ch ON ch.id=n.chapter_id WHERE ch.course_id=c.id),
			(SELECT MIN(ch.id) FROM chapter ch WHERE ch.course_id=c.id AND ch.status='ready')
			FROM course c ORDER BY c.id`)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var id, total, ready, blocks, exercises, notes int
			var code, title, level, src string
			var first sql.NullInt64
			if err = rows.Scan(&id, &code, &title, &level, &src, &total, &ready, &blocks, &exercises, &notes, &first); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			out = append(out, map[string]any{
				"id": id, "code": code, "title_fr": title, "level": level, "source_book": src,
				"chapter_count": total, "ready_count": ready, "block_count": blocks,
				"exercise_count": exercises, "note_count": notes, "first_chapter_id": nullInt(first),
				"scan_base": scanBase(code),
			})
		}
		writeJSON(w, 200, map[string]any{"courses": out})
	})

	mux.HandleFunc("GET /api/course", func(w http.ResponseWriter, r *http.Request) {
		courseID := 1
		if s := r.URL.Query().Get("course_id"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				writeErr(w, 400, "invalid course_id")
				return
			}
			courseID = n
		}
		var code, title, level, src string
		if err := db.QueryRow(`SELECT code, title_fr, level, source_book FROM course WHERE id=?`, courseID).
			Scan(&code, &title, &level, &src); err != nil {
			writeErr(w, 404, "course not found")
			return
		}
		course := map[string]any{
			"id": courseID, "code": code, "title_fr": title, "level": level,
			"source_book": src, "scan_base": scanBase(code),
		}

		rows, err := db.Query(`SELECT c.id, c.seq, c.title_fr, c.page_start, c.page_end, c.status,
			(SELECT COUNT(*) FROM content_block b WHERE b.chapter_id=c.id),
			(SELECT COUNT(*) FROM exercise e WHERE e.chapter_id=c.id),
			(SELECT COUNT(*) FROM note n WHERE n.chapter_id=c.id)
			FROM chapter c WHERE c.course_id=? ORDER BY c.seq`, courseID)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		chapters := []map[string]any{}
		for rows.Next() {
			var id, seq int
			var titleFr, status string
			var pageStart, pageEnd sql.NullInt64
			var blocks, exercises, notes int
			if err = rows.Scan(&id, &seq, &titleFr, &pageStart, &pageEnd, &status, &blocks, &exercises, &notes); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			chapters = append(chapters, map[string]any{
				"id": id, "seq": seq, "title_fr": titleFr,
				"page_start": nullInt(pageStart), "page_end": nullInt(pageEnd),
				"status": status, "block_count": blocks, "exercise_count": exercises, "note_count": notes,
			})
		}

		var notesCount, attemptsCount, qTotal, qReady int
		var chapterAttempts int
		_ = db.QueryRow(`SELECT COUNT(*) FROM note n JOIN chapter c ON c.id=n.chapter_id WHERE c.course_id=?`, courseID).Scan(&notesCount)
		_ = db.QueryRow(`SELECT COUNT(*) FROM attempt WHERE status='submitted' AND chapter_id IN (SELECT id FROM chapter WHERE course_id=?)`, courseID).Scan(&attemptsCount)
		_ = db.QueryRow(`SELECT COUNT(DISTINCT chapter_id) FROM attempt WHERE user_id=1 AND chapter_id IN (SELECT id FROM chapter WHERE course_id=?)`, courseID).Scan(&chapterAttempts)
		_ = db.QueryRow(`SELECT COUNT(*) FROM question q JOIN exercise e ON e.id=q.exercise_id JOIN chapter c ON c.id=e.chapter_id WHERE c.course_id=?`, courseID).Scan(&qTotal)
		_ = db.QueryRow(`SELECT COUNT(*) FROM question q JOIN exercise e ON e.id=q.exercise_id JOIN chapter c ON c.id=e.chapter_id WHERE c.course_id=? AND q.answer_status='ready'`, courseID).Scan(&qReady)

		writeJSON(w, 200, map[string]any{
			"course": course, "chapters": chapters,
			"stats": map[string]any{
				"notes": notesCount, "attempts": attemptsCount,
				"questions": qTotal, "answers_ready": qReady, "chapters_with_attempts": chapterAttempts,
			},
		})
	})

	// The learning page's prominent 继续上次答题 card: the most recently
	// touched chapter that still has a draft, or else the last submission.
	mux.HandleFunc("GET /api/resume", func(w http.ResponseWriter, r *http.Request) {
		courseID := 0
		if s := r.URL.Query().Get("course_id"); s != "" {
			courseID, _ = strconv.Atoi(s)
		}
		query := `SELECT a.id, a.chapter_id, a.status, a.updated_at, a.started_at,
			a.submitted_version, a.score, a.total,
			c.title_fr, c.seq, co.id, co.code,
			(SELECT COUNT(*) FROM attempt_answer aa WHERE aa.attempt_id=a.id),
			(SELECT COUNT(*) FROM question q JOIN exercise e ON e.id=q.exercise_id WHERE e.chapter_id=a.chapter_id)
			FROM attempt a JOIN chapter c ON c.id=a.chapter_id JOIN course co ON co.id=c.course_id
			WHERE a.user_id=1 AND ((a.status='in_progress' AND EXISTS (SELECT 1 FROM attempt_answer aa WHERE aa.attempt_id=a.id))
			  OR (a.status='submitted' AND a.discarded_at IS NULL))`
		args := []any{}
		if courseID > 0 {
			query += ` AND c.course_id=?`
			args = append(args, courseID)
		}
		query += ` ORDER BY CASE WHEN a.status='in_progress' AND EXISTS (SELECT 1 FROM attempt_answer aa WHERE aa.attempt_id=a.id)
			THEN 0 ELSE 1 END, a.updated_at DESC, a.id DESC LIMIT 1`
		var attID, chID int64
		var version sql.NullInt64
		var status, updated, started, title, code string
		var seq, course, answered, qTotal int64
		var score, total sql.NullFloat64
		err := db.QueryRow(query, args...).Scan(&attID, &chID, &status, &updated, &started, &version,
			&score, &total, &title, &seq, &course, &code, &answered, &qTotal)
		if err != nil {
			writeJSON(w, 200, map[string]any{"resume": nil})
			return
		}
		writeJSON(w, 200, map[string]any{"resume": map[string]any{
			"attempt_id": attID, "chapter_id": chID, "chapter_seq": seq, "chapter_title": title,
			"course_id": course, "course_code": code, "status": status,
			"draft_version": version.Int64 + 1, "updated_at": updated, "started_at": started,
			"answered": answered, "question_count": qTotal,
			"score": nullFloat(score), "total": nullFloat(total),
		}})
	})

	mux.HandleFunc("GET /api/chapters/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var seq, courseID int
		var title, status, courseCode, courseTitle string
		var ps, pe, pdfs, pdfe sql.NullInt64
		if err = db.QueryRow(`SELECT c.seq, c.title_fr, c.page_start, c.page_end, c.pdf_start, c.pdf_end, c.status,
			co.id, co.code, co.title_fr
			FROM chapter c JOIN course co ON co.id=c.course_id WHERE c.id=?`, id).
			Scan(&seq, &title, &ps, &pe, &pdfs, &pdfe, &status, &courseID, &courseCode, &courseTitle); err != nil {
			writeErr(w, 404, "chapter not found")
			return
		}
		sections := []map[string]any{}
		srows, err := db.Query(`SELECT id, seq, title_fr, page_no FROM chapter_section WHERE chapter_id=? ORDER BY seq`, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for srows.Next() {
			var sid, sseq int
			var st string
			var sp sql.NullInt64
			_ = srows.Scan(&sid, &sseq, &st, &sp)
			sections = append(sections, map[string]any{"id": sid, "seq": sseq, "title_fr": st, "page_no": nullInt(sp)})
		}
		srows.Close()

		blocks := []map[string]any{}
		brows, err := db.Query(`SELECT id, page_no, seq, type, body_html, source_ref FROM content_block WHERE chapter_id=? ORDER BY page_no, seq`, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for brows.Next() {
			var bid, page, bseq int
			var btype, html, ref string
			_ = brows.Scan(&bid, &page, &bseq, &btype, &html, &ref)
			blocks = append(blocks, map[string]any{
				"id": bid, "page_no": page, "seq": bseq, "type": btype,
				"body_html": html, "source_ref": ref,
			})
		}
		brows.Close()

		notes := []map[string]any{}
		nrows, err := db.Query(`SELECT id, block_id, start_offset, end_offset, quote, text FROM note WHERE chapter_id=? ORDER BY id`, id)
		if err == nil {
			for nrows.Next() {
				var nid int
				var blockID sql.NullInt64
				var start, end int
				var quote, text string
				_ = nrows.Scan(&nid, &blockID, &start, &end, &quote, &text)
				notes = append(notes, map[string]any{
					"id": nid, "block_id": nullInt(blockID), "start_offset": start,
					"end_offset": end, "quote": quote, "text": text,
				})
			}
			nrows.Close()
		}

		var exCount int
		_ = db.QueryRow(`SELECT COUNT(*) FROM exercise WHERE chapter_id=?`, id).Scan(&exCount)
		writeJSON(w, 200, map[string]any{
			"chapter": map[string]any{
				"id": id, "seq": seq, "title_fr": title,
				"page_start": nullInt(ps), "page_end": nullInt(pe),
				"pdf_start": nullInt(pdfs), "pdf_end": nullInt(pdfe), "status": status,
				"course_id": courseID, "course_code": courseCode,
				"course_title_fr": courseTitle, "scan_base": scanBase(courseCode),
			},
			"sections": sections, "blocks": blocks, "notes": notes, "exercise_count": exCount,
		})
	})

	mux.HandleFunc("GET /api/chapters/{id}/exercises", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var courseID int
		var chapterSeq int
		var courseCode, chapterTitle string
		if err = db.QueryRow(`SELECT co.id, co.code, c.title_fr, c.seq FROM chapter c JOIN course co ON co.id=c.course_id WHERE c.id=?`, id).
			Scan(&courseID, &courseCode, &chapterTitle, &chapterSeq); err != nil {
			writeErr(w, 404, "chapter not found")
			return
		}
		erows, err := db.Query(`SELECT id, page_no, seq, instruction_fr, label, note_html, source_ref,
			has_image, scan_pages FROM exercise WHERE chapter_id=? ORDER BY page_no, seq, id`, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		type ex struct {
			m  map[string]any
			id int64
		}
		var list []ex
		for erows.Next() {
			var eid, page, seq int64
			var instr, label, noteHTML, ref, scanPages string
			var hasImage int
			_ = erows.Scan(&eid, &page, &seq, &instr, &label, &noteHTML, &ref, &hasImage, &scanPages)
			pages := []int{}
			for _, part := range strings.Split(scanPages, ",") {
				if n, e := strconv.Atoi(strings.TrimSpace(part)); e == nil && n > 0 {
					pages = append(pages, n)
				}
			}
			list = append(list, ex{
				id: eid,
				m: map[string]any{
					"id": eid, "page_no": page, "seq": seq, "instruction_fr": instr,
					"label": label, "note_html": noteHTML, "source_ref": ref,
					"has_image": hasImage != 0, "scan_pages": pages,
					"questions": []map[string]any{},
				},
			})
		}
		erows.Close()

		// Question ids whose exercise already has a submission from this user.
		released := map[int64]bool{}
		if srows, serr := db.Query(`SELECT q.id FROM question q JOIN exercise e ON e.id=q.exercise_id
			JOIN attempt a ON a.chapter_id=e.chapter_id AND a.scope='exercise:'||e.id AND a.user_id=1
			WHERE e.chapter_id=? AND a.status='submitted' AND a.discarded_at IS NULL`, id); serr == nil {
			for srows.Next() {
				var qid int64
				if srows.Scan(&qid) == nil {
					released[qid] = true
				}
			}
			srows.Close()
		}
		for i := range list {
			qrows, qerr := db.Query(`SELECT id, seq, type, stem_html, source_ref, answer_status, explanation, answer_json FROM question WHERE exercise_id=? ORDER BY seq`, list[i].id)
			if qerr != nil {
				continue
			}
			qs := []map[string]any{}
			for qrows.Next() {
				var qid, qseq int64
				var qtype, stem, ref, astatus string
				var explanation, answerJSON sql.NullString
				_ = qrows.Scan(&qid, &qseq, &qtype, &stem, &ref, &astatus, &explanation, &answerJSON)
				// Keys are sent only after this exercise has been submitted by
				// this user. Drafts keep has_answer (a boolean), never values.
				item := map[string]any{
					"id": qid, "seq": qseq, "type": qtype,
					"stem_html": stem, "source_ref": ref, "answer_status": astatus,
					"has_answer": astatus == "ready", "explanation": explanation.String,
				}
				if released[qid] {
					item["answer"] = answerValues(answerJSON.String)
					item["ex_answer"] = item["answer"]
					// The exercise-level page needs the raw key to compare
					// variants exactly like the grader does.
					item["answer_json"] = answerJSON.String
				}
				qs = append(qs, item)
			}
			qrows.Close()
			list[i].m["questions"] = qs
		}
		out := []map[string]any{}
		submitted, _ := sectionSubmissions(db, id)
		for _, e := range list {
			if s, ok := submitted[strconv.FormatInt(e.id, 10)]; ok {
				e.m["submission"] = s
			}
			out = append(out, e.m)
		}
		writeJSON(w, 200, map[string]any{
			"exercises": out, "course_id": courseID, "course_code": courseCode,
			"chapter_id": id, "chapter_seq": chapterSeq, "chapter_title_fr": chapterTitle,
			"scan_base": scanBase(courseCode),
		})
	})

	mux.HandleFunc("GET /api/attempts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var chapterID sql.NullInt64
		var scope, mode, started, gradingStatus, status string
		var submitted sql.NullString
		var score, total sql.NullFloat64
		var submittedVersion sql.NullInt64
		if err = db.QueryRow(`SELECT chapter_id, scope, mode, started_at, submitted_at, score, total, grading_status, status,
			submitted_version FROM attempt WHERE id=?`, id).
			Scan(&chapterID, &scope, &mode, &started, &submitted, &score, &total, &gradingStatus, &status,
				&submittedVersion); err != nil {
			writeErr(w, 404, "attempt not found")
			return
		}
		// Submitted papers read from their frozen version; a draft reads its
		// live answers. The old path read only attempt_answer, which is now
		// cleared on submit, so a result page would otherwise be empty.
		items := mapItemsToResult(frozenItems(db, id, status), gradingStatus)
		var courseID sql.NullInt64
		if chapterID.Valid {
			_ = db.QueryRow(`SELECT course_id FROM chapter WHERE id=?`, chapterID.Int64).Scan(&courseID)
		}
		writeJSON(w, 200, map[string]any{
			"attempt": map[string]any{
				"id": id, "chapter_id": nullInt(chapterID), "course_id": nullInt(courseID),
				"scope": scope, "mode": mode,
				"started_at": started, "submitted_at": submitted.String,
				"score": nullFloat(score), "total": nullFloat(total),
				"grading_status": gradingStatus, "status": status,
				"submitted_version": nullInt(submittedVersion),
			},
			"items": items,
		})
	})

	mux.HandleFunc("GET /api/notes", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		chapter := r.URL.Query().Get("chapter_id")
		courseFilter := r.URL.Query().Get("course_id")
		query := `SELECT n.id, n.chapter_id, c.title_fr, co.id, co.title_fr, n.block_id,
			n.exercise_id, n.question_id, n.page_no,
			n.start_offset, n.end_offset, n.quote, n.text, n.created_at, n.updated_at
			FROM note n JOIN chapter c ON c.id=n.chapter_id JOIN course co ON co.id=c.course_id WHERE 1=1`
		args := []any{}
		if chapter != "" {
			query += ` AND n.chapter_id=?`
			args = append(args, chapter)
		}
		if courseFilter != "" {
			query += ` AND c.course_id=?`
			args = append(args, courseFilter)
		}
		if q != "" {
			query += ` AND (n.text LIKE ? OR n.quote LIKE ?)`
			args = append(args, "%"+q+"%", "%"+q+"%")
		}
		query += ` ORDER BY n.id DESC`
		rows, err := db.Query(query, args...)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var nid, cid, courseID, start, end int
			var chapterTitle, courseTitle, quote, text, created, updated string
			var blockID, exerciseID, questionID, pageNo sql.NullInt64
			if err = rows.Scan(&nid, &cid, &chapterTitle, &courseID, &courseTitle, &blockID,
				&exerciseID, &questionID, &pageNo,
				&start, &end, &quote, &text, &created, &updated); err != nil {
				continue
			}
			out = append(out, map[string]any{
				"id": nid, "chapter_id": cid, "chapter_title": chapterTitle,
				"course_id": courseID, "course_title_fr": courseTitle,
				"block_id": nullInt(blockID), "exercise_id": nullInt(exerciseID),
				"question_id": nullInt(questionID), "page_no": nullInt(pageNo),
				"start_offset": start, "end_offset": end, "quote": quote, "text": text,
				"created_at": created, "updated_at": updated,
			})
		}
		writeJSON(w, 200, map[string]any{"notes": out, "total": len(out)})
	})

	mux.HandleFunc("POST /api/notes", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChapterID   int    `json:"chapter_id"`
			BlockID     *int   `json:"block_id"`
			ExerciseID  *int   `json:"exercise_id"`
			QuestionID  *int   `json:"question_id"`
			PageNo      *int   `json:"page_no"`
			StartOffset int    `json:"start_offset"`
			EndOffset   int    `json:"end_offset"`
			Quote       string `json:"quote"`
			Text        string `json:"text"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if body.ChapterID == 0 {
			body.ChapterID = 1
		}
		if body.Text == "" {
			writeErr(w, 400, "note text is empty")
			return
		}
		if body.EndOffset <= body.StartOffset {
			writeErr(w, 400, "invalid selection range")
			return
		}
		now := time.Now().Format(time.RFC3339)
		res, err := db.Exec(`INSERT INTO note(user_id, chapter_id, block_id, exercise_id, question_id, page_no, start_offset, end_offset, quote, text, created_at, updated_at)
			VALUES(1,?,?,?,?,?,?,?,?,?,?,?)`, body.ChapterID, body.BlockID, body.ExerciseID,
			body.QuestionID, body.PageNo, body.StartOffset, body.EndOffset, body.Quote, body.Text, now, now)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, 201, map[string]any{"id": id})
	})

	mux.HandleFunc("PUT /api/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var body struct {
			Text string `json:"text"`
		}
		if err = readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if _, err = db.Exec(`UPDATE note SET text=?, updated_at=? WHERE id=?`, body.Text, time.Now().Format(time.RFC3339), id); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("DELETE /api/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if _, err = db.Exec(`DELETE FROM note WHERE id=?`, id); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /api/answers/status", func(w http.ResponseWriter, r *http.Request) {
		var total, ready int
		_ = db.QueryRow(`SELECT COUNT(*) FROM question`).Scan(&total)
		_ = db.QueryRow(`SELECT COUNT(*) FROM question WHERE answer_status='ready'`).Scan(&ready)
		writeJSON(w, 200, map[string]any{"total": total, "ready": ready, "pending": total - ready})
	})

	// Later: POST the answer key here, question by question.
	mux.HandleFunc("PUT /api/admin/answers", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Answers []struct {
				QuestionID  int64          `json:"question_id"`
				Blanks      map[string]any `json:"blanks"`
				Explanation string         `json:"explanation"`
			} `json:"answers"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		tx, err := db.Begin()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer tx.Rollback()
		count := 0
		for _, a := range body.Answers {
			m := map[string]any{}
			for k, v := range a.Blanks {
				m[k] = v
			}
			if a.Explanation != "" {
				m["explanation"] = a.Explanation
			}
			payload, _ := json.Marshal(m)
			res, err := tx.Exec(`UPDATE question SET answer_json=?, answer_status='ready', explanation=? WHERE id=?`,
				string(payload), a.Explanation, a.QuestionID)
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			if n, _ := res.RowsAffected(); n > 0 {
				count++
			}
		}
		if err = tx.Commit(); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"updated": count, "message": "答案已导入，可调用 /api/admin/regrade 重判历史作答"})
	})

	mux.HandleFunc("POST /api/admin/regrade", func(w http.ResponseWriter, r *http.Request) {
		// Every chapter's frozen versions are re-scored against the keys as
		// they are now; no old paper is rewritten, only its score row.
		rows, err := db.Query(`SELECT DISTINCT chapter_id FROM attempt WHERE status='submitted' AND chapter_id IS NOT NULL`)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		var chapters []int64
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				writeErr(w, 500, err.Error())
				return
			}
			chapters = append(chapters, id)
		}
		rows.Close()
		total, graded, changed := 0, 0, 0
		for _, ch := range chapters {
			out, err := regradeChapter(db, ch)
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			total += out["revisions"].(int)
			graded += out["graded"].(int)
			changed += out["changed"].(int)
		}
		slog.Info("regrade finished", "revisions", total, "graded", graded, "changed", changed)
		writeJSON(w, 200, map[string]any{"revisions": total, "graded": graded, "changed": changed})
	})
}

// ---------------------------------------------------------------- dictionary

func registerDictionaryRoutes(mux *http.ServeMux, db *sql.DB, lex *lexicon) {
	mux.HandleFunc("GET /api/dict/lookup", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if strings.TrimSpace(q) == "" || len([]rune(q)) > 160 {
			writeErr(w, 400, "q is required (max 160 characters)")
			return
		}
		result := lex.lookup(q, 20, false)
		if result.Source == "none" {
			guess := ""
			if len(result.Suggestions) > 0 {
				guess = result.Suggestions[0].Lemma
			}
			go lex.queuePending(result.Normalized, guess, "unresolved")
		}
		writeJSON(w, 200, result)
	})

	mux.HandleFunc("GET /api/dict/suggest", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		limit := 20
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 50 {
				limit = n
			}
		}
		writeJSON(w, 200, map[string]any{"suggestions": lex.suggest(normalizeSpelling(clipWords(q, 6)), limit)})
	})

	mux.HandleFunc("GET /api/dict/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, lex.stats())
	})

	// Paged course glossary for the dictionary browser. The list is derived
	// from the same form index as lookups, never from a second copy of text.
	mux.HandleFunc("GET /api/dict/words", func(w http.ResponseWriter, r *http.Request) {
		q := normalizeSpelling(clipWords(r.URL.Query().Get("q"), 4))
		letter := normalizeSpelling(r.URL.Query().Get("letter"))
		pos := r.URL.Query().Get("pos")
		// The source dump also contains symbols, braille and emoji "words".
		// A French dictionary starts with a letter, not a digit or pictogram.
		if letter != "" && !isFrenchLetter(letter) {
			letter = ""
		}
		page, per := 1, 60
		if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
			page = n
		}
		if n, err := strconv.Atoi(r.URL.Query().Get("per")); err == nil && n > 0 && n <= 200 {
			per = n
		}
		pos = strings.TrimSpace(pos)
		if len([]rune(pos)) > 24 {
			pos = ""
		}
		where := []string{`e.lemma GLOB '[[A-Za-zÀ-ÖØ-öø-ÿŒœ]*'`}
		args := []any{}
		if q != "" {
			where = append(where, "e.lemma LIKE ? ESCAPE '\\'")
			args = append(args, escapeLike(q)+"%")
		}
		if letter != "" && len([]rune(letter)) == 1 {
			where = append(where, "e.lemma LIKE ? ESCAPE '\\'")
			args = append(args, escapeLike(letter)+"%")
		}
		if pos != "" {
			where = append(where, "e.pos = ?")
			args = append(args, pos)
		}
		cond := strings.Join(where, " AND ")
		var total int
		if err := db.QueryRow(`SELECT COUNT(*) FROM dict_entry e WHERE `+cond, args...).Scan(&total); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		args = append(args, per, (page-1)*per)
		rows, err := db.Query(`SELECT e.id, e.lemma, e.display, e.pos, e.gloss_zh FROM dict_entry e WHERE `+
			cond+` ORDER BY e.lemma, e.pos LIMIT ? OFFSET ?`, args...)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		words := []map[string]any{}
		for rows.Next() {
			var id int64
			var lemma, display, posLabel, gloss string
			if rows.Scan(&id, &lemma, &display, &posLabel, &gloss) == nil {
				words = append(words, map[string]any{"entry_id": id, "lemma": lemma,
					"display": display, "pos": posLabel, "gloss_zh": gloss})
			}
		}
		writeJSON(w, 200, map[string]any{"words": words, "total": total,
			"page": page, "per": per, "pages": (total + per - 1) / per})
	})

	mux.HandleFunc("GET /api/dictionary", func(w http.ResponseWriter, r *http.Request) {
		chapter, err := strconv.Atoi(r.URL.Query().Get("chapter_id"))
		if err != nil || chapter <= 0 {
			writeErr(w, 400, "invalid chapter_id")
			return
		}
		var text strings.Builder
		rows, err := db.Query(`SELECT body_html FROM content_block WHERE chapter_id=? ORDER BY page_no, seq`, chapter)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for rows.Next() {
			var html string
			if rows.Scan(&html) == nil {
				text.WriteString(" " + html + " ")
			}
		}
		rows.Close()
		writeJSON(w, 200, map[string]any{"chapter_id": chapter, "glossary": lex.glossary(text.String())})
	})

	mux.HandleFunc("GET /api/dict/pending", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`SELECT form, guess_lemma, reason, hits, first_seen, last_seen
			FROM dict_pending WHERE resolved_entry_id IS NULL ORDER BY hits DESC, last_seen DESC LIMIT 200`)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var form, guess, reason, first, last string
			var hits int
			if rows.Scan(&form, &guess, &reason, &hits, &first, &last) == nil {
				out = append(out, map[string]any{"form": form, "guess_lemma": guess,
					"reason": reason, "hits": hits, "first_seen": first, "last_seen": last})
			}
		}
		writeJSON(w, 200, map[string]any{"pending": out, "total": len(out)})
	})

	mux.HandleFunc("POST /api/dict/pending/{form}/resolve", func(w http.ResponseWriter, r *http.Request) {
		form := normalizeSpelling(r.PathValue("form"))
		var body struct {
			EntryID int64  `json:"entry_id"`
			Lemma   string `json:"lemma"`
			Feat    string `json:"feat"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var entryID int64
		var lemma string
		if body.EntryID > 0 {
			err := db.QueryRow(`SELECT id, lemma FROM dict_entry WHERE id=?`, body.EntryID).Scan(&entryID, &lemma)
			if err != nil {
				writeErr(w, 404, "entry not found")
				return
			}
		} else {
			lemma = normalizeSpelling(body.Lemma)
			if lemma == "" {
				writeErr(w, 400, "entry_id or lemma is required")
				return
			}
			if err := db.QueryRow(`SELECT id, lemma FROM dict_entry WHERE lemma=? ORDER BY id LIMIT 1`, lemma).Scan(&entryID, &lemma); err != nil {
				writeErr(w, 404, "entry not found")
				return
			}
		}
		if _, err := db.Exec(`INSERT INTO dict_form(form, entry_id, feat, lemma, source) VALUES(?,?,?,?,'manual')
			ON CONFLICT(form, entry_id, feat) DO NOTHING`, form, entryID, body.Feat, lemma); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if _, err := db.Exec(`UPDATE dict_pending SET resolved_entry_id=? WHERE form=?`, entryID, form); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		lex.invalidate()
		writeJSON(w, 200, map[string]any{"ok": true, "form": form, "entry_id": entryID})
	})
}

// scanBase maps a course to its scanned-page image prefix. Both books are
// illustration-heavy, so original exercise pages are kept alongside the
// transcribed text; exercises flagged has_image show them inline.
func scanBase(code string) string {
	switch {
	case strings.HasPrefix(code, "vocabulaire"):
		return "scans/vocab/p"
	case strings.HasPrefix(code, "grammaire"):
		return "scans/grammaire/p"
	}
	return ""
}

func nullInt(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

func nullFloat(n sql.NullFloat64) any {
	if !n.Valid {
		return nil
	}
	return n.Float64
}

// answerValues exposes an imported key as plain strings; arrays of accepted
// variants collapse into " / " so a blank can be reviewed as one cell.
func answerValues(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &m) != nil {
		return out
	}
	for k, v := range m {
		if k == "explanation" {
			continue
		}
		var variants []string
		if json.Unmarshal(v, &variants) == nil {
			out[k] = strings.Join(variants, " / ")
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			out[k] = s
		}
	}
	return out
}
