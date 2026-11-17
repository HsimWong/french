package main

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// placeholders mirrors the exercise page's widget pattern, used to split a
// frozen answer into one verdict per blank.
var placeholders = regexp.MustCompile(`\{\{([bLTco])(!?)(?::([^}]*))?\}\}`)

// foldAccent strips the most common French diacritics without pulling in
// golang.org/x/text. Used only for lenient tolerance.
func foldAccent(r rune) rune {
	switch r {
	case 'à', 'â', 'ä', 'á', 'ã', 'å':
		return 'a'
	case 'é', 'è', 'ê', 'ë', 'ẽ':
		return 'e'
	case 'î', 'ï', 'í', 'ì':
		return 'i'
	case 'ô', 'ö', 'ó', 'ò':
		return 'o'
	case 'û', 'ü', 'ù', 'ú':
		return 'u'
	case 'ç':
		return 'c'
	case 'À', 'Â', 'Ä', 'Á', 'Ã', 'Å':
		return 'A'
	case 'É', 'È', 'Ê', 'Ë', 'Ẽ':
		return 'E'
	case 'Î', 'Ï':
		return 'I'
	case 'Ô', 'Ö':
		return 'O'
	case 'Û', 'Ü':
		return 'U'
	case 'Ç':
		return 'C'
	case 'œ':
		return 'o'
	case 'Œ':
		return 'O'
	}
	return r
}

func normalize(s, tolerance string) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "’", "'")
	s = strings.ReplaceAll(s, "ʼ", "'")
	s = strings.ReplaceAll(s, "`", "'")
	if tolerance != "strict" {
		s = strings.ToLower(s)
	}
	if tolerance == "lenient" {
		var b strings.Builder
		for _, r := range s {
			b.WriteRune(foldAccent(r))
		}
		s = b.String()
	}
	if tolerance == "normal" {
		var b strings.Builder
		for _, r := range s {
			if !unicode.Is(unicode.Mn, r) {
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	return strings.Trim(s, " .,;:!?")
}

// answerMap is the shape imported later:
// {"1":["êtes"],"2":["Je","je"],"3":["suis"],"explanation":"..."}
func matchAnswer(userJSON, answerJSON, tolerance string) (correct int, total int, ok bool) {
	var user map[string]string
	if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
		return 0, 0, false
	}
	var ans map[string]json.RawMessage
	if answerJSON == "" || json.Unmarshal([]byte(answerJSON), &ans) != nil {
		return 0, 0, false
	}
	for k, raw := range ans {
		if k == "explanation" {
			continue
		}
		var variants []string
		if err := json.Unmarshal(raw, &variants); err != nil {
			var single string
			if json.Unmarshal(raw, &single) == nil {
				variants = []string{single}
			}
		}
		total++
		u := normalize(user[k], tolerance)
		if u == "" {
			continue
		}
		if _, ok := matchVariants(u, raw, tolerance); ok {
			correct++
		}
	}
	return correct, total, true
}

// matchVariants compares one user value with a key that is either an array of
// accepted spellings or a single string.
func matchVariants(user string, raw json.RawMessage, tolerance string) (string, bool) {
	var variants []string
	if err := json.Unmarshal(raw, &variants); err != nil {
		var single string
		if json.Unmarshal(raw, &single) != nil {
			return "", false
		}
		variants = []string{single}
	}
	for _, v := range variants {
		if user == normalize(v, tolerance) {
			return v, true
		}
	}
	return "", false
}

// gradeAttempt recomputes one submitted attempt against whatever answers are
// currently in the database. Questions without an imported answer stay NULL.
func gradeAttempt(tx *sql.Tx, attemptID int64) (float64, float64, bool, error) {
	rows, err := tx.Query(
		`SELECT aa.id, aa.user_answer_json, q.answer_json, q.answer_status, q.tolerance
		 FROM attempt_answer aa JOIN question q ON q.id = aa.question_id
		 WHERE aa.attempt_id = ?`, attemptID)
	if err != nil {
		return 0, 0, false, err
	}
	type row struct {
		id        int64
		user      string
		answer    sql.NullString
		status    string
		tolerance string
	}
	var list []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.user, &r.answer, &r.status, &r.tolerance); err != nil {
			rows.Close()
			return 0, 0, false, err
		}
		list = append(list, r)
	}
	rows.Close()

	var score, total float64
	allReady := true
	for _, r := range list {
		total++
		if r.status != "ready" || !r.answer.Valid {
			allReady = false
			if _, err = tx.Exec(`UPDATE attempt_answer SET is_correct=NULL, score=NULL, feedback=? WHERE id=?`,
				"答案待导入", r.id); err != nil {
				return 0, 0, false, err
			}
			continue
		}
		tol := r.tolerance
		if tol == "" {
			tol = "normal"
		}
		correct, blanks, ok := matchAnswer(r.user, r.answer.String, tol)
		if !ok || blanks == 0 {
			allReady = false
			if _, err = tx.Exec(`UPDATE attempt_answer SET is_correct=NULL, score=NULL, feedback=? WHERE id=?`,
				"答案格式待校验", r.id); err != nil {
				return 0, 0, false, err
			}
			continue
		}
		frac := float64(correct) / float64(blanks)
		score += frac
		var isCorrect int
		if correct == blanks {
			isCorrect = 1
		}
		if _, err = tx.Exec(`UPDATE attempt_answer SET is_correct=?, score=?, feedback=? WHERE id=?`,
			isCorrect, frac, "", r.id); err != nil {
			return 0, 0, false, err
		}
	}
	return score, total, allReady && total > 0, nil
}

// gradeRows reads the chapter's questions with their answer keys.
type qrow struct {
	qid, eid, page, exSeq, qSeq int64
	stem, ref, instr            string
	answer, status, tolerance   sql.NullString
	explanation                 sql.NullString
}

func gradeRows(tx *sql.Tx, attemptID int64) ([]qrow, error) {
	rows, err := tx.Query(`SELECT q.id, q.stem_html, q.source_ref, e.id, e.page_no, e.seq, q.seq,
		e.instruction_fr, q.answer_json, q.answer_status, q.tolerance, q.explanation
		FROM question q JOIN exercise e ON e.id=q.exercise_id
		WHERE e.chapter_id=(SELECT chapter_id FROM attempt WHERE id=?)
		ORDER BY e.page_no, e.seq, q.seq`, attemptID)
	return scanQRows(rows, err)
}

// exerciseGradeRows reads one exercise's questions for section grading.
// It deliberately looks at exercise_id only, so a paper for another chapter
// can never be scored from this section's key.
func exerciseGradeRows(tx *sql.Tx, exerciseID int64) ([]qrow, error) {
	rows, err := tx.Query(`SELECT q.id, q.stem_html, q.source_ref, e.id, e.page_no, e.seq, q.seq,
		e.instruction_fr, q.answer_json, q.answer_status, q.tolerance, q.explanation
		FROM question q JOIN exercise e ON e.id=q.exercise_id
		WHERE e.id=? ORDER BY q.seq`, exerciseID)
	return scanQRows(rows, err)
}

func scanQRows(rows *sql.Rows, err error) ([]qrow, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []qrow
	for rows.Next() {
		var r qrow
		if err = rows.Scan(&r.qid, &r.stem, &r.ref, &r.eid, &r.page, &r.exSeq, &r.qSeq,
			&r.instr, &r.answer, &r.status, &r.tolerance, &r.explanation); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// perBlankVerdicts stores each blank's outcome (correct / wrong / waiting) so
// a saved version can show point-by-point feedback without re-reading the key.
func perBlankVerdicts(tx *sql.Tx, attemptID int64, answers answerMap, allReady bool) string {
	if !allReady {
		return "{}"
	}
	return verdictsFrom(tx, attemptID, answers)
}

func verdictsFrom(tx *sql.Tx, attemptID int64, answers answerMap) string {
	rows, err := gradeRows(tx, attemptID)
	if err != nil {
		return "{}"
	}
	out := map[string]map[string]string{}
	for _, r := range rows {
		blanks := answers[r.qid]
		if len(blanks) == 0 || r.status.String != "ready" || !r.answer.Valid {
			continue
		}
		tol := r.tolerance.String
		if tol == "" {
			tol = "normal"
		}
		var ans map[string]json.RawMessage
		if json.Unmarshal([]byte(r.answer.String), &ans) != nil {
			continue
		}
		per := map[string]string{}
		for k := range blanks {
			v, ok := ans[k]
			if !ok || k == "explanation" {
				continue
			}
			user := normalize(blanks[k], tol)
			status := "wrong"
			if user == "" {
				status = "empty"
			}
			if user != "" {
				if _, good := matchVariants(user, v, tol); good {
					status = "ok"
				}
			}
			per[k] = status
		}
		if len(per) > 0 {
			out[strconv.FormatInt(r.qid, 10)] = per
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func verdictsForRevision(tx *sql.Tx, attemptID int64, answers answerMap, allReady bool) string {
	if !allReady {
		return "{}"
	}
	return verdictsFrom(tx, attemptID, answers)
}

// gradeFrozen scores a frozen answer map ("questionID:blank" -> value)
// against the answer keys as they are now. Rows are returned in the chapter's
// page/exercise/question order so the result page reads like the paper.
func gradeFrozen(tx *sql.Tx, attemptID int64, answers answerMap) (float64, float64, bool, error) {
	rows, err := tx.Query(`SELECT q.id, q.stem_html, q.source_ref, e.id, e.page_no, e.seq, q.seq,
		e.instruction_fr, q.answer_json, q.answer_status, q.tolerance, q.explanation
		FROM question q JOIN exercise e ON e.id=q.exercise_id
		WHERE e.chapter_id=(SELECT chapter_id FROM attempt WHERE id=?)
		ORDER BY e.page_no, e.seq, q.seq`, attemptID)
	if err != nil {
		return 0, 0, false, err
	}
	var list []qrow
	for rows.Next() {
		var r qrow
		if err = rows.Scan(&r.qid, &r.stem, &r.ref, &r.eid, &r.page, &r.exSeq, &r.qSeq,
			&r.instr, &r.answer, &r.status, &r.tolerance, &r.explanation); err != nil {
			rows.Close()
			return 0, 0, false, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, 0, false, err
	}

	var score, total float64
	allReady := true
	for _, r := range list {
		if len(answers[r.qid]) == 0 {
			continue
		}
		if r.status.String != "ready" || !r.answer.Valid {
			allReady = false
			continue
		}
		tol := r.tolerance.String
		if tol == "" {
			tol = "normal"
		}
		keys := make([]string, 0, len(answers[r.qid]))
		for k := range answers[r.qid] {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			ni, _ := strconv.Atoi(keys[i])
			nj, _ := strconv.Atoi(keys[j])
			return ni < nj
		})
		var accepted map[string]json.RawMessage
		if json.Unmarshal([]byte(r.answer.String), &accepted) != nil {
			allReady = false
			continue
		}
		// Only keys present in the answer sheet count toward the total; a
		// blank left empty is still attempted and scores zero.
		per := map[string]string{}
		for _, k := range keys {
			if _, ok := accepted[k]; !ok {
				continue
			}
			total++
			per[k] = answers[r.qid][k]
		}
		if len(per) == 0 {
			allReady = false
			continue
		}
		// One attempt per blank, scored independently. This shares its rule
		// with verdictsFrom, which draws the red/green marks.
		var right int
		for k, v := range per {
			if _, good := matchVariants(normalize(v, tol), accepted[k], tol); good {
				right++
			}
		}
		score += float64(right) / float64(len(per))
	}
	return score, total, allReady && total > 0, nil
}
