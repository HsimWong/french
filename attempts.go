package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Answer state has two homes:
//   - attempt (status=in_progress): the live draft of a chapter, one per
//     chapter thanks to idx_attempt_one_draft, so 续答 always finds it.
//   - attempt_revision: an immutable snapshot taken on each submit, holding
//     the paper exactly as written plus its score and the key version used.
// The attempt row mirrors the newest revision (v1, v2...) for quick lists.

const (
	maxStoredAttempts = 200
	maxAnswerLength   = 4000
	// Each exercise is checked like a section of a paper: several questions
	// make one unit, and the unit is graded as soon as it is submitted.
	sectionScopePrefix = "exercise:"
)

var (
	errQuestionNotInExercise = errors.New("题目不属于这个练习")
	errExerciseEmpty         = errors.New("这个练习还没有导入标准答案")
)

var (
	errNotDraft  = errors.New("该作答已交卷，无法再次提交")
	errNotFound  = errors.New("记录不存在")
	errNoAnswers = errors.New("至少填写一处答案后才能提交")
)

func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }

func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// answerPayload is the request shape shared by draft save and submit. Blanks
// are keyed by the 1-based widget number of the stem.
type answerPayload struct {
	QuestionID int64             `json:"question_id"`
	Blanks     map[string]string `json:"blanks"`
}

type answerMap map[int64]map[string]string

// readAnswers validates and normalizes a client answer list. Empty values are
// kept so that erasing an answer also saves.
func readAnswers(in []answerPayload) (answerMap, error) {
	out := answerMap{}
	for _, a := range in {
		if a.QuestionID <= 0 {
			return nil, fmt.Errorf("invalid question_id %d", a.QuestionID)
		}
		if _, dup := out[a.QuestionID]; dup {
			return nil, fmt.Errorf("duplicate question_id %d", a.QuestionID)
		}
		blanks := map[string]string{}
		for k, v := range a.Blanks {
			n, err := strconv.Atoi(k)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("invalid blank index %q", k)
			}
			if len(v) > maxAnswerLength {
				return nil, fmt.Errorf("第 %d 题第 %d 空作答过长", a.QuestionID, n)
			}
			blanks[strconv.Itoa(n)] = v
		}
		out[a.QuestionID] = blanks
	}
	return out, nil
}

// answerJSON serialises answers in a stable order, so an unchanged paper is
// byte-identical to its previous revision.
func answerJSON(answers answerMap) string {
	ids := make([]int64, 0, len(answers))
	for id := range answers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	b.WriteByte('[')
	for i, id := range ids {
		keys := make([]string, 0, len(answers[id]))
		for k := range answers[id] {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			ni, ei := strconv.Atoi(keys[i])
			nj, ej := strconv.Atoi(keys[j])
			if ei == nil && ej == nil {
				return ni < nj
			}
			if ei == nil || ej == nil {
				return ei == nil
			}
			return keys[i] < keys[j]
		})
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"question_id":%d,"blanks":{`, id)
		for j, k := range keys {
			if j > 0 {
				b.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			vb, _ := json.Marshal(answers[id][k])
			b.Write(kb)
			b.WriteByte(':')
			b.Write(vb)
		}
		b.WriteString("}}")
	}
	b.WriteByte(']')
	return b.String()
}

func parseAnswerJSON(raw string) (answerMap, error) {
	var items []answerPayload
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, err
	}
	out := answerMap{}
	for _, it := range items {
		if it.QuestionID <= 0 {
			continue
		}
		if out[it.QuestionID] == nil {
			out[it.QuestionID] = map[string]string{}
		}
		for k, v := range it.Blanks {
			out[it.QuestionID][k] = v
		}
	}
	return out, nil
}

// mergeAnswers overlays answers (newest wins, including empty values).
func mergeAnswers(base, answers answerMap) answerMap {
	out := make(answerMap, len(base)+len(answers))
	for id, blanks := range base {
		cp := make(map[string]string, len(blanks))
		for k, v := range blanks {
			cp[k] = v
		}
		out[id] = cp
	}
	for id, blanks := range answers {
		if out[id] == nil {
			out[id] = map[string]string{}
		}
		for k, v := range blanks {
			out[id][k] = v
		}
	}
	return out
}

func answersForClient(answers answerMap) map[string]map[string]string {
	out := map[string]map[string]string{}
	for id, blanks := range answers {
		if len(blanks) > 0 && !blankMapEmpty(blanks) {
			out[strconv.FormatInt(id, 10)] = blanks
		}
	}
	return out
}

func blankMapEmpty(blanks map[string]string) bool {
	for _, v := range blanks {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

type rowQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

// onePerChapter serialises the read-modify-write of a chapter's paper, so an
// auto-save, a submit and a restart never split or duplicate a draft.
var chapterLocks sync.Map

type chapterMutex struct {
	mu    sync.Mutex
	users int
}

func lockChapter(chapterID int64) func() {
	v, _ := chapterLocks.LoadOrStore(chapterID, &chapterMutex{})
	c := v.(*chapterMutex)
	c.mu.Lock()
	return func() {
		c.mu.Unlock()
	}
}

type attemptInfo struct {
	ID               int64
	ChapterID        int64
	Status           string
	StartedAt        string
	UpdatedAt        string
	SubmittedAt      string
	GradingStatus    string
	SubmittedVersion int
	Score            *float64
	Total            *float64
	KeyVersion       string
	DiscardedAt      string
}

func (a attemptInfo) isDraft() bool { return a.Status == "in_progress" }

// draftVersion is the version number the draft will get when submitted.
func (a attemptInfo) draftVersion() int { return a.SubmittedVersion + 1 }

func fetchAttempt(q rowQuerier, id int64) (attemptInfo, error) {
	var a attemptInfo
	var chapter, ver sql.NullInt64
	var started, updated, submitted, grading, basis, discarded sql.NullString
	var score, total sql.NullFloat64
	err := q.QueryRow(`SELECT id, chapter_id, status, started_at, updated_at, submitted_at, grading_status,
		submitted_version, score, total, score_basis, discarded_at FROM attempt WHERE id=?`, id).
		Scan(&a.ID, &chapter, &a.Status, &started, &updated, &submitted, &grading, &ver, &score, &total, &basis, &discarded)
	if err != nil {
		return a, err
	}
	a.ChapterID, a.StartedAt, a.UpdatedAt = chapter.Int64, started.String, updated.String
	a.SubmittedAt, a.GradingStatus, a.SubmittedVersion = submitted.String, grading.String, int(ver.Int64)
	a.Score, a.Total, a.KeyVersion, a.DiscardedAt = floatPtr(score), floatPtr(total), basis.String, discarded.String
	return a, nil
}

func floatPtr(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func loadAnswers(q rowQuerier, attemptID int64) (answerMap, error) {
	rows, err := q.Query(`SELECT question_id, user_answer_json FROM attempt_answer WHERE attempt_id=?`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := answerMap{}
	for rows.Next() {
		var qid int64
		var raw string
		if err = rows.Scan(&qid, &raw); err != nil {
			return nil, err
		}
		var blanks map[string]string
		if json.Unmarshal([]byte(raw), &blanks) != nil {
			continue
		}
		out[qid] = blanks
	}
	return out, rows.Err()
}

func chapterQuestionIDs(q rowQuerier, chapterID int64) (map[int64]bool, error) {
	rows, err := q.Query(`SELECT q.id FROM question q JOIN exercise e ON e.id=q.exercise_id WHERE e.chapter_id=?`, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// findDraft returns the live draft id, or 0 when the chapter has none.
func findDraft(q rowQuerier, chapterID int64) (int64, error) {
	var id int64
	err := q.QueryRow(`SELECT id FROM attempt WHERE user_id=1 AND chapter_id=? AND status='in_progress'`, chapterID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func maxSubmittedVersion(q rowQuerier, chapterID int64) (int, error) {
	var v sql.NullInt64
	// Section attempts are their own papers; they must not inflate the chapter
	// lesson's next version number.
	err := q.QueryRow(`SELECT MAX(submitted_version) FROM attempt WHERE user_id=1 AND chapter_id=? AND status='submitted'
		AND (scope IS NULL OR scope NOT LIKE 'exercise:%')`,
		chapterID).Scan(&v)
	if err != nil {
		return 0, err
	}
	return int(max(0, v.Int64)), nil
}

// saveDraft upserts the single live draft. An existing draft is updated in
// place, which is what makes the timestamp below the "last answer" marker.
func saveDraft(db *sql.DB, chapterID int64, answers answerMap) (attemptInfo, error) {
	defer lockChapter(chapterID)()
	// Reuse the live draft at the library level, so every caller (API, admin
	// actions, tests) does not repeat the one-draft-per-chapter lookup.
	id, err := findDraft(db, chapterID)
	if err != nil {
		return attemptInfo{}, err
	}
	tx, err := db.Begin()
	if err != nil {
		return attemptInfo{}, err
	}
	defer tx.Rollback()
	now := nowISO()
	qids, err := chapterQuestionIDs(tx, chapterID)
	if err != nil {
		return attemptInfo{}, err
	}
	if id == 0 {
		ver, err := maxSubmittedVersion(tx, chapterID)
		if err != nil {
			return attemptInfo{}, err
		}
		res, err := tx.Exec(`INSERT INTO attempt(user_id, chapter_id, scope, mode, started_at, updated_at, submitted_version)
			VALUES(1,?,'chapter','lesson',?,?,?)`, chapterID, now, now, ver)
		if err != nil {
			return attemptInfo{}, err
		}
		id, _ = res.LastInsertId()
	} else if _, err = tx.Exec(`UPDATE attempt SET updated_at=? WHERE id=?`, now, id); err != nil {
		return attemptInfo{}, err
	}
	for qid, blanks := range answers {
		if !qids[qid] {
			continue
		}
		payload, _ := json.Marshal(blanks)
		if _, err = tx.Exec(`INSERT INTO attempt_answer(attempt_id, question_id, user_answer_json) VALUES(?,?,?)
			ON CONFLICT(attempt_id, question_id) DO UPDATE SET user_answer_json=excluded.user_answer_json`,
			id, qid, string(payload)); err != nil {
			return attemptInfo{}, err
		}
	}
	// A question whose every blank was erased is no longer part of the paper;
	// leaving it stored would inflate the saved count in drafts and results.
	for qid, blanks := range answers {
		if blankMapEmpty(blanks) {
			if _, err = tx.Exec(`DELETE FROM attempt_answer WHERE attempt_id=? AND question_id=?`, id, qid); err != nil {
				return attemptInfo{}, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return attemptInfo{}, err
	}
	return fetchAttempt(db, id)
}

// buildDraftView assembles what 续答 needs: the live draft overlaid with every
// live submission made after it, so editing a graded paper resumes from the
// newest state without reopening an old version.
func buildDraftView(db *sql.DB, chapterID int64) (map[string]any, error) {
	rows, err := db.Query(`SELECT id, status FROM attempt WHERE user_id=1 AND chapter_id=?
		AND (status='in_progress' OR (status='submitted' AND discarded_at IS NULL))
		ORDER BY id`, chapterID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var status string
		if err = rows.Scan(&id, &status); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	merged := answerMap{}
	var draft attemptInfo
	for _, id := range ids {
		a, err := fetchAttempt(db, id)
		if err != nil {
			return nil, err
		}
		answers, err := loadAnswers(db, id)
		if err != nil {
			return nil, err
		}
		merged = mergeAnswers(merged, answers)
		if a.isDraft() {
			draft = a
		}
	}
	if draft.ID == 0 {
		return map[string]any{"attempt_id": nil, "answers": map[string]map[string]string{}}, nil
	}
	lastSaved := draft.UpdatedAt
	if last, err := newestRevisionTime(db, draft.ID); err == nil && last > lastSaved {
		lastSaved = last
	}
	graded := draft.GradingStatus == "graded" && len(merged) > 0
	return map[string]any{
		"attempt_id": draft.ID, "started_at": draft.StartedAt, "updated_at": lastSaved,
		"draft_version": draft.draftVersion(), "answers": answersForClient(merged),
		"paper_hash": draftHash(merged), "graded": graded,
	}, nil
}

// draftHash identifies one state of a paper; the browser uses it to tell
// whether the opened answers already match the latest version.
func draftHash(answers answerMap) string {
	if len(answers) == 0 {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(answerJSON(answers)))
	return fmt.Sprintf("%016x", h.Sum64())
}

func newestRevisionTime(q rowQuerier, attemptID int64) (string, error) {
	var t sql.NullString
	err := q.QueryRow(`SELECT MAX(created_at) FROM attempt_revision WHERE attempt_id=?`, attemptID).Scan(&t)
	return t.String, err
}

// answerKeyVersion fingerprints the current answer keys, so each frozen
// revision can name exactly which key produced its score.
func answerKeyVersion(q rowQuerier) string {
	var n, ready, last sql.NullInt64
	_ = q.QueryRow(`SELECT COUNT(*), COALESCE(SUM(answer_status='ready'),0), COALESCE(MAX(id),0) FROM question`).Scan(&n, &ready, &last)
	h := fnv.New32a()
	fmt.Fprintf(h, "%d/%d/%d", n.Int64, ready.Int64, last.Int64)
	return fmt.Sprintf("k%d-%08x", ready.Int64, h.Sum32())
}

// submitAttempt freezes the paper as the next version and grades that frozen
// copy. Subsequent answer-key edits do not rewrite old scores silently; a
// regrade does, and updates the named key version with it.
func submitAttempt(db *sql.DB, id int64, answers answerMap, source string) (map[string]any, error) {
	a, err := fetchAttempt(db, id)
	if err != nil {
		return nil, err
	}
	defer lockChapter(a.ChapterID)()
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if a, err = fetchAttempt(tx, id); err != nil {
		return nil, err
	}
	if !a.isDraft() {
		return nil, errNotDraft
	}
	qids, err := chapterQuestionIDs(tx, a.ChapterID)
	if err != nil {
		return nil, err
	}
	for qid := range answers {
		if !qids[qid] {
			delete(answers, qid)
		}
	}
	for qid, blanks := range answers {
		payload, _ := json.Marshal(blanks)
		if _, err = tx.Exec(`INSERT INTO attempt_answer(attempt_id, question_id, user_answer_json) VALUES(?,?,?)
			ON CONFLICT(attempt_id, question_id) DO UPDATE SET user_answer_json=excluded.user_answer_json`,
			id, qid, string(payload)); err != nil {
			return nil, err
		}
	}
	final, err := loadAnswers(tx, id)
	if err != nil {
		return nil, err
	}
	for qid := range final {
		if !qids[qid] || blankMapEmpty(final[qid]) {
			delete(final, qid)
		}
	}
	version, err := nextVersionFrom(tx, a.ChapterID)
	if err != nil {
		return nil, err
	}
	score, total, allReady, err := gradeFrozen(tx, id, final)
	if err != nil {
		return nil, err
	}
	grading, scoreOut, totalOut := "pending", any(nil), any(nil)
	var isCorrect sql.NullInt64
	if allReady {
		grading, scoreOut, totalOut = "graded", round4(score), total
		isCorrect = sql.NullInt64{Int64: 1, Valid: true}
	}
	created := nowISO()
	keyVer := answerKeyVersion(tx)
	verdicts := perBlankVerdicts(tx, id, final, allReady)
	res, err := tx.Exec(`INSERT INTO attempt_revision(attempt_id, version, source, answer_count, answers_json,
		score, total, is_correct, created_at, verdicts_json) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, version, source, len(final), answerJSON(final), scoreOut, totalOut, isCorrect, created, verdicts)
	if err != nil {
		return nil, err
	}
	revID, _ := res.LastInsertId()
	if _, err = tx.Exec(`UPDATE attempt SET status='submitted', submitted_at=?, updated_at=?, score=?, total=?,
		grading_status=?, submitted_version=?, score_basis=? WHERE id=?`,
		created, created, scoreOut, totalOut, grading, version, keyVer, id); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`DELETE FROM attempt_answer WHERE attempt_id=?`, id); err != nil {
		return nil, err
	}
	if err = trimHistory(tx, a.ChapterID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{
		"attempt_id": id, "revision_id": revID, "version": version, "version_label": versionLabel(version),
		"source": source, "grading_status": grading, "answers": len(final),
		"score": scoreOut, "total": totalOut, "key_version": keyVer, "message": gradingMessage(allReady),
	}, nil
}

// submitExercise grades one exercise as its own small paper. The questions
// are checked against that exercise's key only, and each submission becomes a
// frozen version of the attempt (scope = exercise:<id>), so a chapter can be
// checked section by section without finishing the other exercises.
func submitExercise(db *sql.DB, chapterID, exerciseID int64, answers answerMap, source string) (map[string]any, error) {
	var owner int64
	if err := db.QueryRow(`SELECT chapter_id FROM exercise WHERE id=?`, exerciseID).Scan(&owner); err != nil {
		return nil, errNotFound
	}
	if owner != chapterID {
		return nil, errQuestionNotInExercise
	}
	defer lockChapter(chapterID)()
	rows, err := db.Query(`SELECT q.id FROM question q WHERE q.exercise_id=?`, exerciseID)
	if err != nil {
		return nil, err
	}
	qids := map[int64]bool{}
	for rows.Next() {
		var qid int64
		if err = rows.Scan(&qid); err == nil {
			qids[qid] = true
		}
	}
	rows.Close()
	final := answerMap{}
	for qid, blanks := range answers {
		if qids[qid] && !blankMapEmpty(blanks) {
			final[qid] = blanks
		}
	}
	if len(final) == 0 {
		return nil, errNoAnswers
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	keyRows, err := exerciseGradeRows(tx, exerciseID)
	if err != nil {
		return nil, err
	}
	ready := false
	for _, r := range keyRows {
		if r.status.String == "ready" && r.answer.Valid && len(final[r.qid]) > 0 {
			ready = true
			break
		}
	}
	if !ready {
		return nil, errExerciseEmpty
	}

	var version int
	err = tx.QueryRow(`SELECT COALESCE(MAX(version),0) FROM attempt_revision r JOIN attempt a ON a.id=r.attempt_id
		WHERE a.chapter_id=? AND a.user_id=1`, chapterID).Scan(&version)
	if err != nil {
		return nil, err
	}
	version++
	score, total, correct, partial, scored := gradeExerciseKey(keyRows, final)
	grading := "graded"
	var scoreOut, totalOut any = round4(score), total
	var correctOut sql.NullInt64
	if scored == 0 {
		grading, scoreOut, totalOut = "pending", nil, nil
	} else if partial {
		correctOut = sql.NullInt64{Int64: 0, Valid: true}
	}
	created := nowISO()
	keyVer := answerKeyVersion(tx)
	verdicts := verdictsFromRows(keyRows, final)
	sourceTag := source
	if sourceTag != "auto" {
		sourceTag = "submit"
	}
	res, err := tx.Exec(`INSERT INTO attempt(user_id, chapter_id, scope, mode, started_at, updated_at, submitted_at,
		score, total, grading_status, status, submitted_version, score_basis, discarded_at)
		VALUES(1,?,?,'section',?,?,?,?,?,?,'submitted',?,?,NULL)`,
		chapterID, sectionScopePrefix+strconv.FormatInt(exerciseID, 10), created, created, created,
		scoreOut, totalOut, grading, version, keyVer)
	if err != nil {
		return nil, err
	}
	attemptID, _ := res.LastInsertId()
	revRes, err := tx.Exec(`INSERT INTO attempt_revision(attempt_id, version, source, answer_count, answers_json,
		score, total, is_correct, created_at, verdicts_json) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		attemptID, version, sourceTag, len(final), answerJSON(final), scoreOut, totalOut, correctOut, created, verdicts)
	if err != nil {
		return nil, err
	}
	revID, _ := revRes.LastInsertId()
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{
		"attempt_id": attemptID, "revision_id": revID, "exercise_id": exerciseID,
		"version": version, "version_label": versionLabel(version), "scope": "exercise",
		"grading_status": grading, "answers": len(final), "answered_questions": len(final),
		"correct": correct, "partial": partial,
		"score": scoreOut, "total": totalOut, "key_version": keyVer,
		"message": fmt.Sprintf("练习 %d 已单独提交，%s。", exerciseID, gradingMessage(grading == "graded")),
	}, nil
}

// gradeExerciseKey scores only the section's own questions. A partial result
// stays honest when some questions lack a usable key: they are not counted
// as right, and the UI is told how many were actually checked.
func gradeExerciseKey(rows []qrow, answers answerMap) (score, total float64, correct int, partial bool, scored int) {
	for _, r := range rows {
		blanks := answers[r.qid]
		if len(blanks) == 0 || r.status.String != "ready" || !r.answer.Valid {
			continue
		}
		tol := r.tolerance.String
		if tol == "" {
			tol = "normal"
		}
		userJSON, _ := json.Marshal(blanks)
		right, n, ok := matchAnswer(string(userJSON), r.answer.String, tol)
		if !ok || n == 0 {
			continue
		}
		scored++
		total += float64(n)
		score += float64(right)
		correct += right
	}
	// Any answered question without a ready key makes the section partial.
	for _, r := range rows {
		if len(answers[r.qid]) > 0 && (r.status.String != "ready" || !r.answer.Valid) {
			return score, total, correct, true, scored
		}
	}
	return score, total, correct, false, scored
}

func verdictsFromRows(rows []qrow, answers answerMap) string {
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
			raw, ok := ans[k]
			if !ok || k == "explanation" {
				continue
			}
			u := normalize(blanks[k], tol)
			switch {
			case u == "":
				per[k] = "empty"
			default:
				if _, good := matchVariants(u, raw, tol); good {
					per[k] = "ok"
				} else {
					per[k] = "wrong"
				}
			}
		}
		if len(per) > 0 {
			out[strconv.FormatInt(r.qid, 10)] = per
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func versionLabel(v int) string { return fmt.Sprintf("v%d", v) }

func gradingMessage(graded bool) string {
	if graded {
		return "作答已保存并完成判分。"
	}
	return "作答已保存并冻结为本版本。尚无标准答案的题目暂不判分，补齐答案后可重判。"
}

func nextVersionFrom(q rowQuerier, chapterID int64) (int, error) {
	maxSub, err := maxSubmittedVersion(q, chapterID)
	if err != nil {
		return 0, err
	}
	var rev sql.NullInt64
	_ = q.QueryRow(`SELECT MAX(r.version) FROM attempt_revision r JOIN attempt a ON a.id=r.attempt_id
		WHERE a.chapter_id=? AND a.status='submitted'`, chapterID).Scan(&rev)
	return max(maxSub, int(rev.Int64)) + 1, nil
}

// trimHistory keeps the record bounded at maxStoredAttempts per chapter. The
// oldest paper and its revisions go first; the live draft is never touched.
func trimHistory(q rowQuerier, chapterID int64) error {
	rows, err := q.Query(`SELECT id FROM attempt WHERE user_id=1 AND chapter_id=? AND status='submitted'
		ORDER BY submitted_at DESC, id DESC`, chapterID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for i, id := range ids {
		if i < maxStoredAttempts {
			continue
		}
		if _, err = q.Exec(`DELETE FROM attempt_revision WHERE attempt_id=?`, id); err != nil {
			return err
		}
		if _, err = q.Exec(`DELETE FROM attempt_answer WHERE attempt_id=?`, id); err != nil {
			return err
		}
		if _, err = q.Exec(`DELETE FROM attempt WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// restartChapter stores the unfinished draft as an automatic version and
// opens a clean draft, so 重新作答 never destroys written answers.
func discardAttempt(db *sql.DB, attemptID int64) error {
	res, err := db.Exec(`UPDATE attempt SET discarded_at=? WHERE id=? AND status='submitted' AND discarded_at IS NULL`,
		nowISO(), attemptID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// loadVersionIntoDraft copies a frozen version into the live draft. The
// current draft is preserved as an automatic version first, so "载入" never
// loses what was being typed, and the old version itself stays untouched.
func loadVersionIntoDraft(db *sql.DB, chapterID, attemptID int64) (map[string]any, error) {
	var status string
	var version int
	var raw string
	if err := db.QueryRow(`SELECT a.status, r.version, r.answers_json FROM attempt a
		JOIN attempt_revision r ON r.attempt_id=a.id
		WHERE a.id=? AND a.chapter_id=? AND a.user_id=1
		ORDER BY r.version DESC LIMIT 1`, attemptID, chapterID).Scan(&status, &version, &raw); err != nil {
		return nil, errNotFound
	}
	answers, err := parseAnswerJSON(raw)
	if err != nil {
		return nil, err
	}
	if cur, err := findDraft(db, chapterID); err != nil {
		return nil, err
	} else if cur > 0 {
		if _, err = submitAttempt(db, cur, answerMap{}, "auto"); err != nil {
			return nil, err
		}
	}
	draft, err := saveDraft(db, chapterID, answers)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"attempt_id": draft.ID, "draft_version": draft.draftVersion(),
		"loaded_label": versionLabel(version), "source_status": status,
		"message": fmt.Sprintf("已载入 %s 的答案；交卷将生成 v%d。", versionLabel(version), draft.draftVersion()),
	}, nil
}

// regradeChapter re-scores every frozen revision of a chapter against today's
// answer key and reports how many scores moved.
func regradeChapter(db *sql.DB, chapterID int64) (map[string]any, error) {
	rows, err := db.Query(`SELECT r.id, r.attempt_id, r.answers_json, r.version FROM attempt_revision r
		JOIN attempt a ON a.id=r.attempt_id WHERE a.chapter_id=? ORDER BY r.id`, chapterID)
	if err != nil {
		return nil, err
	}
	type rev struct {
		id, attemptID int64
		answers       string
		version       int
	}
	var revs []rev
	for rows.Next() {
		var r rev
		if err = rows.Scan(&r.id, &r.attemptID, &r.answers, &r.version); err != nil {
			rows.Close()
			return nil, err
		}
		revs = append(revs, r)
	}
	rows.Close()

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	keyVer := answerKeyVersion(tx)
	graded, changed := 0, 0
	for _, r := range revs {
		answers, perr := parseAnswerJSON(r.answers)
		if perr != nil {
			continue
		}
		score, total, allReady, gerr := gradeFrozen(tx, r.attemptID, answers)
		if gerr != nil {
			return nil, gerr
		}
		var s, t any
		var ok sql.NullInt64
		if allReady {
			s, t, ok = round4(score), total, sql.NullInt64{Int64: 1, Valid: true}
			graded++
		}
		if _, err = tx.Exec(`UPDATE attempt_revision SET score=?, total=?, is_correct=? WHERE id=?`, s, t, ok, r.id); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`UPDATE attempt_revision SET verdicts_json=? WHERE id=?`,
			verdictsForRevision(tx, r.attemptID, answers, allReady), r.id); err != nil {
			return nil, err
		}
		var before sql.NullFloat64
		_ = tx.QueryRow(`SELECT score FROM attempt WHERE id=? AND submitted_version=?`, r.attemptID, r.version).Scan(&before)
		if _, err = tx.Exec(`UPDATE attempt SET score=?, total=?, grading_status=?, score_basis=? WHERE id=? AND submitted_version=?`,
			s, t, gradingWord(allReady), keyVer, r.attemptID, r.version); err != nil {
			return nil, err
		}
		if before.Valid != allReady || (allReady && before.Float64 != round4(score)) {
			changed++
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	slog.Info("chapter regraded", "chapter", chapterID, "revisions", len(revs), "graded", graded)
	return map[string]any{"revisions": len(revs), "graded": graded, "changed": changed}, nil
}

func gradingWord(graded bool) string {
	if graded {
		return "graded"
	}
	return "pending"
}

func handleAttemptRoutes(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /api/chapters/{id}/attempt", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		d, err := buildDraftView(db, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		hist, err := revisionList(db, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		d["history"] = hist
		if draftA, derr := fetchDraftRow(db, id); derr == nil {
			d["draft_meta"] = draftA
		}
		writeJSON(w, 200, d)
	})

	mux.HandleFunc("PUT /api/chapters/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var body struct {
			Answers []answerPayload `json:"answers"`
		}
		if err = readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		answers, err := readAnswers(body.Answers)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		a, err := saveDraft(db, id, answers)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"attempt_id": a.ID, "draft_version": a.draftVersion(), "saved_at": a.UpdatedAt, "answered": len(answers),
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
			writeErr(w, 400, "chapter_id is required")
			return
		}
		id, err := findDraft(db, body.ChapterID)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if id == 0 {
			a, err := saveDraft(db, body.ChapterID, answerMap{})
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			id = a.ID
		}
		writeJSON(w, 201, map[string]any{"attempt_id": id})
	})

	mux.HandleFunc("POST /api/attempts/{id}/submit", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var body struct {
			Answers []answerPayload `json:"answers"`
			Source  string          `json:"source"`
		}
		if err = readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		answers, err := readAnswers(body.Answers)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		source := body.Source
		if source != "auto" {
			source = "submit"
		}
		out, err := submitAttempt(db, id, answers, source)
		if errors.Is(err, errNotDraft) {
			writeErr(w, 409, err.Error())
			return
		}
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, out)
	})

	mux.HandleFunc("POST /api/chapters/{id}/restart", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		preserved, err := findDraft(db, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if preserved > 0 {
			if _, err = submitAttempt(db, preserved, answerMap{}, "auto"); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
		}
		a, err := saveDraft(db, id, answerMap{})
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"attempt_id": a.ID, "draft_version": a.draftVersion(),
			"message": "已开始新的作答，原草稿作为自动版本保存在记录中。",
		})
	})

	mux.HandleFunc("GET /api/chapters/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		hist, err := revisionList(db, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		sections, serr := sectionSubmissions(db, id)
		if serr != nil {
			writeErr(w, 500, serr.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"history": hist, "sections": sections})
	})

	mux.HandleFunc("POST /api/chapters/{id}/exercises/{eid}/submit", func(w http.ResponseWriter, r *http.Request) {
		chapterID, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		exerciseID, err := pathID(r, "eid")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var body struct {
			Answers []answerPayload `json:"answers"`
			Source  string          `json:"source"`
		}
		if err = readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		answers, err := readAnswers(body.Answers)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		out, err := submitExercise(db, chapterID, exerciseID, answers, body.Source)
		switch {
		case errors.Is(err, errNotFound), errors.Is(err, errQuestionNotInExercise):
			writeErr(w, 404, err.Error())
			return
		case errors.Is(err, errNoAnswers), errors.Is(err, errExerciseEmpty):
			writeErr(w, 409, err.Error())
			return
		case err != nil:
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, out)
	})

	mux.HandleFunc("GET /api/chapters/{id}/exercises/{eid}/submission", func(w http.ResponseWriter, r *http.Request) {
		chapterID, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		exerciseID, err := pathID(r, "eid")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		out, err := latestSectionSubmission(db, chapterID, exerciseID)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"submission": out})
	})

	mux.HandleFunc("GET /api/revisions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		rev, items, err := revisionDetail(db, id)
		if err != nil {
			writeErr(w, 404, "版本不存在")
			return
		}
		writeJSON(w, 200, map[string]any{"revision": rev, "items": items})
	})

	mux.HandleFunc("POST /api/chapters/{id}/load-version", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var body struct {
			AttemptID int64 `json:"attempt_id"`
		}
		if err = readJSON(r, &body); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		out, err := loadVersionIntoDraft(db, id, body.AttemptID)
		if err != nil {
			status := 500
			if errors.Is(err, errNotFound) {
				status = 404
			}
			writeErr(w, status, err.Error())
			return
		}
		writeJSON(w, 200, out)
	})

	mux.HandleFunc("POST /api/revisions/{id}/discard", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var attemptID int64
		var version int
		if err = db.QueryRow(`SELECT attempt_id, version FROM attempt_revision WHERE id=?`, id).Scan(&attemptID, &version); err != nil {
			writeErr(w, 404, "版本不存在")
			return
		}
		// Only the newest submission can be withdrawn; older ones are history.
		var current int
		_ = db.QueryRow(`SELECT submitted_version FROM attempt WHERE id=?`, attemptID).Scan(&current)
		if version != current {
			writeErr(w, 409, "只能作废最新版本")
			return
		}
		if err = discardAttempt(db, attemptID); err != nil {
			writeErr(w, 404, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "message": "该版本已作废，只读记录仍保留在历史中。"})
	})

	mux.HandleFunc("POST /api/chapters/{id}/regrade", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		out, err := regradeChapter(db, id)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, out)
	})
}

// revisionList returns every frozen version of a chapter, newest first.
// fetchDraftRow gives the history panel its "草稿（未交卷）" row, even when
// the draft is empty and therefore omitted from the draft view.
func fetchDraftRow(db *sql.DB, chapterID int64) (map[string]any, error) {
	id, err := findDraft(db, chapterID)
	if err != nil || id == 0 {
		return nil, err
	}
	a, err := fetchAttempt(db, id)
	if err != nil {
		return nil, err
	}
	var answered, qTotal int
	_ = db.QueryRow(`SELECT COUNT(*) FROM attempt_answer WHERE attempt_id=?`, id).Scan(&answered)
	_ = db.QueryRow(`SELECT COUNT(*) FROM question q JOIN exercise e ON e.id=q.exercise_id WHERE e.chapter_id=?`,
		chapterID).Scan(&qTotal)
	return map[string]any{
		"attempt_id": nil, "status": "in_progress", "history_label": fmt.Sprintf("v%d 草稿（未交卷）", a.draftVersion()),
		"score": nil, "total": nil, "grading_status": "pending", "updated_at": a.UpdatedAt,
		"revision_count": 0, "key_version": "", "discarded": false,
		"answered": answered, "question_count": qTotal, "draft_version": a.draftVersion(),
	}, nil
}

func revisionList(db *sql.DB, chapterID int64) ([]map[string]any, error) {
	rows, err := db.Query(`SELECT r.id, r.attempt_id, r.version, r.source, r.answer_count, r.score, r.total,
		r.is_correct, r.created_at, a.discarded_at
		FROM attempt_revision r JOIN attempt a ON a.id=r.attempt_id
		WHERE a.chapter_id=? ORDER BY r.created_at DESC, r.id DESC`, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, attemptID, version, count int64
		var source, created string
		var score, total sql.NullFloat64
		var correct sql.NullInt64
		var discarded sql.NullString
		if err = rows.Scan(&id, &attemptID, &version, &source, &count, &score, &total, &correct, &created, &discarded); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"revision_id": id, "attempt_id": attemptID, "version": version,
			"label": versionLabel(int(version)), "source": source, "answered": count,
			"score": nullFloat(score), "total": nullFloat(total),
			"grading_status": gradingWord(correct.Valid), "created_at": created,
			"discarded":  discarded.Valid,
			"chapter_id": chapterID, "has_recent": chapterID > 0,
		})
	}
	return out, rows.Err()
}

// revisionDetail shows one frozen version in the same per-question order as
// the exercise page. The routed /api/attempts/{id} result page also uses it,
// so a submitted paper keeps its answers after its draft rows are cleared.
func revisionDetail(db *sql.DB, revisionID int64) (map[string]any, []map[string]any, error) {
	return revisionDetailBy(db, `r.id=?`, revisionID)
}

// attemptDetail returns the newest frozen version of one attempt.
func attemptDetail(db *sql.DB, attemptID int64) (map[string]any, []map[string]any, error) {
	return revisionDetailBy(db, `r.attempt_id=? ORDER BY r.version DESC LIMIT 1`, attemptID)
}

// sectionSubmissions lists the newest per-exercise submission for the page.
// Only the newest one per exercise is shown inline; older ones stay in the
// frozen revision history.
func sectionSubmissions(db *sql.DB, chapterID int64) (map[string]map[string]any, error) {
	rows, err := db.Query(`SELECT a.id, a.scope, a.submitted_version, a.score, a.total, a.grading_status,
		a.submitted_at, a.discarded_at FROM attempt a
		WHERE a.chapter_id=? AND a.user_id=1 AND a.status='submitted' AND a.scope LIKE 'exercise:%'`, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var id, version int64
		var scope, grading, submitted string
		var score, total sql.NullFloat64
		var discarded sql.NullString
		if err = rows.Scan(&id, &scope, &version, &score, &total, &grading, &submitted, &discarded); err != nil {
			return nil, err
		}
		eid := strings.TrimPrefix(scope, sectionScopePrefix)
		prev, ok := out[eid]
		if ok && prev["submitted_at"].(string) > submitted {
			continue
		}
		out[eid] = map[string]any{
			"attempt_id": id, "exercise_id": eid, "version": version,
			"score": nullFloat(score), "total": nullFloat(total),
			"grading_status": grading, "submitted_at": submitted, "discarded": discarded.Valid,
		}
	}
	return out, nil
}

// latestSectionSubmission returns one exercise's latest answers, key and
// verdicts so the page can show the correct answers after submission.
func latestSectionSubmission(db *sql.DB, chapterID, exerciseID int64) (map[string]any, error) {
	var attemptID int64
	err := db.QueryRow(`SELECT id FROM attempt WHERE chapter_id=? AND user_id=1 AND scope=? AND status='submitted'
		ORDER BY submitted_at DESC, id DESC LIMIT 1`, chapterID, sectionScopePrefix+strconv.FormatInt(exerciseID, 10)).
		Scan(&attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sectionResult(db, chapterID, exerciseID, attemptID)
}

// sectionResult joins the frozen answers with the exercise's key. It is only
// served after a submit for that exercise, so draft pages never reveal keys.
func sectionResult(db *sql.DB, chapterID, exerciseID, attemptID int64) (map[string]any, error) {
	a, err := fetchAttempt(db, attemptID)
	if err != nil {
		return nil, err
	}
	var raw, verdicts, created, source, scope string
	var version, count int64
	var score, total sql.NullFloat64
	var correct sql.NullInt64
	if err = db.QueryRow(`SELECT r.answers_json, COALESCE(r.verdicts_json,'{}'), r.created_at, r.source, r.version,
		r.answer_count, r.score, r.total, r.is_correct, a.scope
		FROM attempt_revision r JOIN attempt a ON a.id=r.attempt_id WHERE r.attempt_id=? AND a.chapter_id=?
		ORDER BY r.version DESC LIMIT 1`, attemptID, chapterID).Scan(&raw, &verdicts, &created, &source, &version,
		&count, &score, &total, &correct, &scope); err != nil {
		return nil, err
	}
	answers, err := parseAnswerJSON(raw)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT q.id, q.seq, q.stem_html, q.source_ref, COALESCE(q.answer_json,''),
		q.answer_status, COALESCE(q.explanation,'') FROM question q WHERE q.exercise_id=? ORDER BY q.seq`, exerciseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var qid, seq int64
		var stem, ref, key, status, expl string
		if err = rows.Scan(&qid, &seq, &stem, &ref, &key, &status, &expl); err != nil {
			return nil, err
		}
		if len(answers[qid]) == 0 {
			continue
		}
		items = append(items, map[string]any{
			"question_id": qid, "seq": seq, "stem_html": stem, "source_ref": ref,
			"answer_status": status, "explanation": expl,
			"correct": answerValues(key), "blanks": answers[qid],
		})
	}
	var perBlank map[string]map[string]string
	_ = json.Unmarshal([]byte(verdicts), &perBlank)
	perQ := map[string]map[string]string{}
	for k, v := range perBlank {
		perQ[k] = v
	}
	return map[string]any{
		"attempt_id": attemptID, "exercise_id": exerciseID, "scope": scope,
		"version": version, "label": versionLabel(int(version)), "source": source,
		"created_at": created, "answered": count,
		"score": nullFloat(score), "total": nullFloat(total),
		"grading_status": a.GradingStatus,
		"items":          items, "verdicts": perQ,
	}, nil
}

// frozenItems returns a submitted attempt's newest frozen answers, or the
// live draft's answers before submission, in page/exercise/question order.
func frozenItems(db *sql.DB, attemptID int64, status string) []map[string]any {
	if status == "submitted" {
		_, items, err := attemptDetail(db, attemptID)
		if err == nil {
			return items
		}
		return []map[string]any{}
	}
	rows, err := db.Query(`SELECT aa.question_id, q.stem_html, q.source_ref, e.instruction_fr, e.page_no,
		aa.user_answer_json FROM attempt_answer aa
		JOIN question q ON q.id=aa.question_id JOIN exercise e ON e.id=q.exercise_id
		WHERE aa.attempt_id=? ORDER BY e.page_no, e.seq, q.seq`, attemptID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var qid, page int64
		var stem, ref, instr, raw string
		if err = rows.Scan(&qid, &stem, &ref, &instr, &page, &raw); err != nil {
			return items
		}
		var blanks map[string]string
		_ = json.Unmarshal([]byte(raw), &blanks)
		items = append(items, map[string]any{
			"question_id": qid, "stem_html": stem, "source_ref": ref, "instruction_fr": instr,
			"page_no": page, "blanks": blanks,
		})
	}
	return items
}

// mapItemsToResult adds the whole-question verdict to each frozen item while
// keeping the per-blank marks intact. done implies every answer key was ready.
func mapItemsToResult(items []map[string]any, gradingStatus string) []map[string]any {
	for _, it := range items {
		_, ok := it["is_correct"]
		if ok {
			continue
		}
		var verdict any
		if gradingStatus == "graded" {
			marks, _ := it["verdicts"].(map[string]string)
			verdict = 1
			for _, v := range marks {
				if v != "ok" {
					verdict = 0
					break
				}
			}
		}
		it["is_correct"] = verdict
		it["answer_status"] = "pending"
		if gradingStatus == "graded" {
			it["answer_status"] = "ready"
		}
	}
	return items
}

func revisionDetailBy(db *sql.DB, where string, arg int64) (map[string]any, []map[string]any, error) {
	var attemptID, version, count int64
	var source, created, answers string
	var score, total sql.NullFloat64
	var correct sql.NullInt64
	var verdicts, basis sql.NullString
	var revisionID int64
	err := db.QueryRow(`SELECT r.id, r.attempt_id, r.version, r.source, r.created_at, r.answer_count, r.answers_json,
		r.score, r.total, r.is_correct, r.verdicts_json, a.score_basis
		FROM attempt_revision r JOIN attempt a ON a.id=r.attempt_id WHERE `+where, arg).
		Scan(&revisionID, &attemptID, &version, &source, &created, &count, &answers, &score, &total, &correct, &verdicts, &basis)
	if err != nil {
		return nil, nil, err
	}
	var perBlank map[string]map[string]string
	if verdicts.Valid {
		_ = json.Unmarshal([]byte(verdicts.String), &perBlank)
	}
	flat, err := parseAnswerJSON(answers)
	if err != nil {
		return nil, nil, err
	}
	rows, err := db.Query(`SELECT q.id, q.stem_html, q.source_ref, e.id, e.page_no, e.seq, q.seq,
		e.instruction_fr, q.explanation
		FROM question q JOIN exercise e ON e.id=q.exercise_id
		WHERE e.chapter_id=(SELECT chapter_id FROM attempt WHERE id=?)
		ORDER BY e.page_no, e.seq, q.seq`, attemptID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var qid, eid, page, exSeq, qSeq int64
		var stem, ref, instr string
		var expl sql.NullString
		if err = rows.Scan(&qid, &stem, &ref, &eid, &page, &exSeq, &qSeq, &instr, &expl); err != nil {
			return nil, nil, err
		}
		blanks := flat[qid]
		if len(blanks) == 0 {
			continue
		}
		marks := perBlank[strconv.FormatInt(qid, 10)]
		if marks == nil {
			marks = map[string]string{}
		}
		items = append(items, map[string]any{
			"question_id": qid, "stem_html": stem, "source_ref": ref, "exercise_id": eid,
			"page_no": page, "instruction_fr": instr, "blanks": blanks, "explanation": expl.String,
			"verdicts": marks,
		})
	}
	rev := map[string]any{
		"id": revisionID, "attempt_id": attemptID, "version": version, "label": versionLabel(int(version)),
		"source": source, "created_at": created, "answered": count,
		"score": nullFloat(score), "total": nullFloat(total),
		"grading_status": gradingWord(correct.Valid || basis.String != ""), "key_version": basis.String,
	}
	return rev, items, nil
}

// exerciseAnswerPoints returns every blank in an exercise's key.
func exerciseAnswerPoints(db *sql.DB, exerciseID int64) (map[int64]int, int, map[int64]string, error) {
	rows, err := db.Query(`SELECT id, answer_json, answer_status FROM question WHERE exercise_id=? ORDER BY seq`, exerciseID)
	if err != nil {
		return nil, 0, nil, err
	}
	defer rows.Close()
	perQ := map[int64]int{}
	raw := map[int64]string{}
	ready := 0
	for rows.Next() {
		var qid int64
		var key, status string
		if err = rows.Scan(&qid, &key, &status); err != nil {
			return nil, 0, nil, err
		}
		raw[qid] = key
		n := 0
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(key), &m) == nil {
			for k := range m {
				if k != "explanation" {
					n++
				}
			}
		}
		perQ[qid] = n
		if status == "ready" && n > 0 {
			ready++
		}
	}
	return perQ, ready, raw, rows.Err()
}
