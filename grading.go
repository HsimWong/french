package main

import (
	"database/sql"
	"encoding/json"
	"strings"
	"unicode"
)

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
		for _, v := range variants {
			if u == normalize(v, tolerance) {
				correct++
				break
			}
		}
	}
	return correct, total, true
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
