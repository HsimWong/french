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
		_ = db.QueryRow(`SELECT COUNT(*) FROM note n JOIN chapter c ON c.id=n.chapter_id WHERE c.course_id=?`, courseID).Scan(&notesCount)
		_ = db.QueryRow(`SELECT COUNT(*) FROM attempt WHERE status='submitted' AND chapter_id IN (SELECT id FROM chapter WHERE course_id=?)`, courseID).Scan(&attemptsCount)
		_ = db.QueryRow(`SELECT COUNT(*) FROM question q JOIN exercise e ON e.id=q.exercise_id JOIN chapter c ON c.id=e.chapter_id WHERE c.course_id=?`, courseID).Scan(&qTotal)
		_ = db.QueryRow(`SELECT COUNT(*) FROM question q JOIN exercise e ON e.id=q.exercise_id JOIN chapter c ON c.id=e.chapter_id WHERE c.course_id=? AND q.answer_status='ready'`, courseID).Scan(&qReady)

		writeJSON(w, 200, map[string]any{
			"course": course, "chapters": chapters,
			"stats": map[string]any{
				"notes": notesCount, "attempts": attemptsCount,
				"questions": qTotal, "answers_ready": qReady,
			},
		})
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
		var courseCode, chapterTitle string
		if err = db.QueryRow(`SELECT co.id, co.code, c.title_fr FROM chapter c JOIN course co ON co.id=c.course_id WHERE c.id=?`, id).
			Scan(&courseID, &courseCode, &chapterTitle); err != nil {
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
			m        map[string]any
			id       int64
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

		for i := range list {
			qrows, qerr := db.Query(`SELECT id, seq, type, stem_html, source_ref, answer_status FROM question WHERE exercise_id=? ORDER BY seq`, list[i].id)
			if qerr != nil {
				continue
			}
			qs := []map[string]any{}
			for qrows.Next() {
				var qid, qseq int64
				var qtype, stem, ref, astatus string
				_ = qrows.Scan(&qid, &qseq, &qtype, &stem, &ref, &astatus)
				qs = append(qs, map[string]any{
					"id": qid, "seq": qseq, "type": qtype,
					"stem_html": stem, "source_ref": ref, "answer_status": astatus,
				})
			}
			qrows.Close()
			list[i].m["questions"] = qs
		}
		out := []map[string]any{}
		for _, e := range list {
			out = append(out, e.m)
		}
		writeJSON(w, 200, map[string]any{
			"exercises": out, "course_id": courseID, "course_code": courseCode,
			"chapter_title_fr": chapterTitle, "scan_base": scanBase(courseCode),
		})
	})

	mux.HandleFunc("POST /api/attempts", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChapterID int64 `json:"chapter_id"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if body.ChapterID == 0 {
			body.ChapterID = 1
		}
		res, err := db.Exec(`INSERT INTO attempt(user_id, chapter_id, scope, mode, started_at) VALUES(1,?,'chapter','lesson',?)`,
			body.ChapterID, time.Now().Format(time.RFC3339))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, 201, map[string]any{"attempt_id": id})
	})

	mux.HandleFunc("POST /api/attempts/{id}/submit", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var body struct {
			Answers []struct {
				QuestionID int64             `json:"question_id"`
				Blanks     map[string]string `json:"blanks"`
			} `json:"answers"`
		}
		if err = readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		tx, err := db.Begin()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer tx.Rollback()
		var state string
		if err = tx.QueryRow(`SELECT status FROM attempt WHERE id=?`, id).Scan(&state); err != nil {
			writeErr(w, 404, "attempt not found")
			return
		}
		for _, a := range body.Answers {
			if a.Blanks == nil {
				a.Blanks = map[string]string{}
			}
			payload, _ := json.Marshal(a.Blanks)
			if _, err = tx.Exec(`INSERT INTO attempt_answer(attempt_id, question_id, user_answer_json) VALUES(?,?,?)
				ON CONFLICT(attempt_id, question_id) DO UPDATE SET user_answer_json=excluded.user_answer_json`,
				id, a.QuestionID, string(payload)); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
		}
		score, total, allReady, err := gradeAttempt(tx, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		gradingStatus := "pending"
		var scoreOut any
		if allReady {
			gradingStatus = "graded"
			scoreOut = score
		}
		if _, err = tx.Exec(`UPDATE attempt SET status='submitted', submitted_at=?, score=?, total=?, grading_status=? WHERE id=?`,
			time.Now().Format(time.RFC3339), scoreOut, total, gradingStatus, id); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if err = tx.Commit(); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"attempt_id": id, "grading_status": gradingStatus,
			"score": scoreOut, "total": total,
			"message": "作答已保存。标准答案尚未导入，暂不判分；导入后可一键重判。",
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
		if err = db.QueryRow(`SELECT chapter_id, scope, mode, started_at, submitted_at, score, total, grading_status, status FROM attempt WHERE id=?`, id).
			Scan(&chapterID, &scope, &mode, &started, &submitted, &score, &total, &gradingStatus, &status); err != nil {
			writeErr(w, 404, "attempt not found")
			return
		}
		items := []map[string]any{}
		rows, err := db.Query(`SELECT aa.question_id, q.stem_html, q.source_ref, e.instruction_fr, e.page_no,
			aa.user_answer_json, aa.is_correct, aa.feedback, q.answer_status
			FROM attempt_answer aa
			JOIN question q ON q.id=aa.question_id
			JOIN exercise e ON e.id=q.exercise_id
			WHERE aa.attempt_id = ?
			ORDER BY e.page_no, e.seq, q.seq`, id)
		if err == nil {
			for rows.Next() {
				var qid, page int64
				var stem, ref, instr, userJSON, astatus string
				var feedback sql.NullString
				var correct sql.NullInt64
				_ = rows.Scan(&qid, &stem, &ref, &instr, &page, &userJSON, &correct, &feedback, &astatus)
				var blanks map[string]string
				_ = json.Unmarshal([]byte(userJSON), &blanks)
				items = append(items, map[string]any{
					"question_id": qid, "stem_html": stem, "source_ref": ref,
					"instruction_fr": instr, "page_no": page,
					"blanks": blanks, "is_correct": nullInt(correct),
					"feedback": feedback.String, "answer_status": astatus,
				})
			}
			rows.Close()
		}
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
			},
			"items": items,
		})
	})

	mux.HandleFunc("GET /api/notes", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		chapter := r.URL.Query().Get("chapter_id")
		courseFilter := r.URL.Query().Get("course_id")
		query := `SELECT n.id, n.chapter_id, c.title_fr, co.id, co.title_fr, n.block_id, n.page_no,
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
			var blockID, pageNo sql.NullInt64
			if err = rows.Scan(&nid, &cid, &chapterTitle, &courseID, &courseTitle, &blockID, &pageNo,
				&start, &end, &quote, &text, &created, &updated); err != nil {
				continue
			}
			out = append(out, map[string]any{
				"id": nid, "chapter_id": cid, "chapter_title": chapterTitle,
				"course_id": courseID, "course_title_fr": courseTitle,
				"block_id": nullInt(blockID), "page_no": nullInt(pageNo),
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
		res, err := db.Exec(`INSERT INTO note(user_id, chapter_id, block_id, page_no, start_offset, end_offset, quote, text, created_at, updated_at)
			VALUES(1,?,?,?,?,?,?,?,?,?)`, body.ChapterID, body.BlockID, body.PageNo,
			body.StartOffset, body.EndOffset, body.Quote, body.Text, now, now)
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
				QuestionID  int64             `json:"question_id"`
				Blanks      map[string]any    `json:"blanks"`
				Explanation string            `json:"explanation"`
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
		rows, err := db.Query(`SELECT id FROM attempt WHERE status='submitted' ORDER BY id`)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		var ids []int64
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		tx, err := db.Begin()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		defer tx.Rollback()
		graded := 0
		for _, id := range ids {
			score, total, allReady, err := gradeAttempt(tx, id)
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			status := "pending"
			var s any
			if allReady {
				status = "graded"
				s = score
				graded++
			}
			if _, err = tx.Exec(`UPDATE attempt SET score=?, total=?, grading_status=? WHERE id=?`, s, total, status, id); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
		}
		if err = tx.Commit(); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		slog.Info("regrade finished", "attempts", len(ids), "graded", graded)
		writeJSON(w, 200, map[string]any{"attempts": len(ids), "graded": graded})
	})
}

// scanBase maps a course to its scanned-page image prefix. Both books are
// illustration-heavy, so original exercise pages are kept alongside the
// transcribed text; exercises flagged has_image show them inline.
func scanBase(code string) string {
	switch {
	case strings.HasPrefix(code, "vocabulaire"):
		return "/scans/vocab/p"
	case strings.HasPrefix(code, "grammaire"):
		return "/scans/grammaire/p"
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
