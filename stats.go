package main

// Activity statistics: per-course active study time and the daily workload
// wall. All bucketing is done in Asia/Shanghai (+08:00) with a fixed offset so
// the numbers do not depend on the server's tzdata (the VPS runs in UTC).

import (
	"database/sql"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

var shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)

func localDay(t time.Time) string { return t.In(shanghai).Format("2006-01-02") }
func todayLocal() string          { return localDay(time.Now()) }

// parseStamp turns any of the RFC3339 strings written by the app into the
// +08:00 calendar day it belongs to. Legacy rows use a mixture of "Z" and
// "+08:00"; both must land on the same local day.
func parseStamp(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse("2006-01-02 15:04:05", s)
		if err != nil {
			return "", false
		}
	}
	return localDay(t), true
}

func dayRange(today string, n int) []string {
	if n < 1 {
		n = 1
	}
	base, err := time.ParseInLocation("2006-01-02", today, shanghai)
	if err != nil {
		base = time.Now().In(shanghai)
	}
	days := make([]string, 0, n)
	for i := n - 1; i >= 0; i-- {
		days = append(days, base.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return days
}

type statsCourse struct {
	ID           int64            `json:"id"`
	Code         string           `json:"code"`
	Title        string           `json:"title_fr"`
	Level        string           `json:"level"`
	TotalSeconds int64            `json:"total_seconds"`
	Days         map[string]int64 `json:"days"` // date -> active seconds
}

type statsDay struct {
	Seconds     int64 `json:"seconds"`
	Submissions int   `json:"submissions"`
	Answers     int   `json:"answers"`
	Notes       int   `json:"notes"`
	Work        int   `json:"work"` // active minutes + submissions + notes
}

type activityStats struct {
	Today         string              `json:"today"`
	Days          []string            `json:"days"`
	Courses       []statsCourse       `json:"courses"`
	Daily         map[string]statsDay `json:"daily"`
	TotalSeconds  int64               `json:"total_seconds"`
	TodaySeconds  int64               `json:"today_seconds"`
	ActiveDays    int                 `json:"active_days"`
	Streak        int                 `json:"streak"`
	LongestStreak int                 `json:"longest_streak"`
	Timezone      string              `json:"timezone"`
	WorkFormula   string              `json:"work_formula"`
}

func registerStatsRoutes(mux *http.ServeMux, db *sql.DB) {
	// A heartbeat carries a handful of active seconds. Cap a single ping so a
	// buggy/stuck client can never inflate the numbers by hours at once.
	mux.HandleFunc("POST /api/study/ping", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			CourseID int64 `json:"course_id"`
			Seconds  int64 `json:"seconds"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, 400, "invalid request")
			return
		}
		var exists int
		if err := db.QueryRow(`SELECT 1 FROM course WHERE id=?`, in.CourseID).Scan(&exists); err != nil {
			writeErr(w, 400, "unknown course")
			return
		}
		if in.Seconds < 1 {
			writeErr(w, 400, "seconds must be positive")
			return
		}
		if in.Seconds > 600 {
			in.Seconds = 600
		}
		day := todayLocal()
		now := time.Now().UTC().Format(time.RFC3339)
		_, err := db.Exec(`INSERT INTO study_log(user_id, day, course_id, seconds, updated_at)
			VALUES(1,?,?,?,?)
			ON CONFLICT(user_id, day, course_id) DO UPDATE SET
			  seconds=seconds+excluded.seconds, updated_at=excluded.updated_at`,
			day, in.CourseID, in.Seconds, now)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "day": day, "course_id": in.CourseID, "seconds": in.Seconds})
	})

	mux.HandleFunc("GET /api/stats/activity", func(w http.ResponseWriter, r *http.Request) {
		n := 371 // 53 weeks, matching a full GitHub-style contribution wall
		if raw := r.URL.Query().Get("days"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v >= 7 && v <= 730 {
				n = v
			}
		}
		today := todayLocal()
		days := dayRange(today, n)
		inRange := map[string]bool{}
		for _, d := range days {
			inRange[d] = true
		}
		daily := make(map[string]statsDay, n)
		for _, d := range days {
			daily[d] = statsDay{}
		}

		all, err := loadActivityStats(db, days, inRange)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		all.Today = today
		all.Days = days
		all.Timezone = "Asia/Shanghai (UTC+08:00)"
		all.WorkFormula = "活跃分钟数 + 交卷次数 + 笔记条数"
		all.TodaySeconds = dailyToday(all.Courses, today)
		all.TotalSeconds = sumCourseTotals(all.Courses)
		all.ActiveDays, all.Streak, all.LongestStreak = streaks(days, all.Daily)
		writeJSON(w, 200, all)
	})
}

type rawCourse struct {
	id           int64
	code, title  string
	level        string
	totalSeconds int64
	byDay        map[string]int64
}

func loadActivityStats(db *sql.DB, days []string, inRange map[string]bool) (*activityStats, error) {
	out := &activityStats{Courses: []statsCourse{}, Daily: map[string]statsDay{}}
	courses := []rawCourse{}
	rows, err := db.Query(`SELECT id, code, title_fr, level FROM course ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c rawCourse
		if err := rows.Scan(&c.id, &c.code, &c.title, &c.level); err != nil {
			rows.Close()
			return nil, err
		}
		c.byDay = map[string]int64{}
		courses = append(courses, c)
	}
	rows.Close()
	byID := map[int64]*rawCourse{}
	for i := range courses {
		byID[courses[i].id] = &courses[i]
	}

	// Active seconds: stored per local day already, no parsing needed.
	rows, err = db.Query(`SELECT user_id, day, course_id, seconds FROM study_log`)
	if err != nil {
		return nil, err
	}
	type logRow struct {
		day      string
		courseID int64
		seconds  int64
	}
	var logs []logRow
	for rows.Next() {
		var uid, cid, sec int64
		var day string
		if err := rows.Scan(&uid, &day, &cid, &sec); err != nil {
			rows.Close()
			return nil, err
		}
		if c := byID[cid]; c != nil {
			c.totalSeconds += sec
		}
		if inRange[day] {
			logs = append(logs, logRow{day, cid, sec})
		}
	}
	rows.Close()
	for _, l := range logs {
		if c := byID[l.courseID]; c != nil {
			c.byDay[l.day] += l.seconds
		}
		d := out.Daily[l.day]
		d.Seconds += l.seconds
		out.Daily[l.day] = d
	}

	// Submissions and graded answers come from the immutable revision log,
	// joined back to the owning course so the wall has real historical data.
	rows, err = db.Query(`SELECT r.created_at, COALESCE(r.answer_count,0), ch.course_id
		FROM attempt_revision r
		JOIN attempt a ON a.id=r.attempt_id
		JOIN chapter ch ON ch.id=a.chapter_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var stamp string
		var answers int
		var cid int64
		if err := rows.Scan(&stamp, &answers, &cid); err != nil {
			rows.Close()
			return nil, err
		}
		day, ok := parseStamp(stamp)
		if !ok || !inRange[day] {
			continue
		}
		d := out.Daily[day]
		d.Submissions++
		d.Answers += answers
		out.Daily[day] = d
	}
	rows.Close()

	rows, err = db.Query(`SELECT created_at, chapter_id FROM note`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var stamp string
		var chapterID int64
		if err := rows.Scan(&stamp, &chapterID); err != nil {
			rows.Close()
			return nil, err
		}
		day, ok := parseStamp(stamp)
		if !ok || !inRange[day] {
			continue
		}
		d := out.Daily[day]
		d.Notes++
		out.Daily[day] = d
	}
	rows.Close()

	for _, c := range courses {
		out.Courses = append(out.Courses, statsCourse{
			ID: c.id, Code: c.code, Title: c.title, Level: c.level,
			TotalSeconds: c.totalSeconds, Days: c.byDay,
		})
	}
	for day, d := range out.Daily {
		d.Work = int(math.Round(float64(d.Seconds)/60.0)) + d.Submissions + d.Notes
		out.Daily[day] = d
	}
	return out, nil
}

func dailyToday(courses []statsCourse, day string) int64 {
	var total int64
	for _, c := range courses {
		total += c.Days[day]
	}
	return total
}

func sumCourseTotals(courses []statsCourse) int64 {
	var total int64
	for _, c := range courses {
		total += c.TotalSeconds
	}
	return total
}

// streaks returns active-day count, the current streak and the longest
// streak. A day counts as active when it has study time, a submission or a
// note. The current streak may end yesterday (today is not over yet).
func streaks(days []string, daily map[string]statsDay) (int, int, int) {
	if len(days) == 0 {
		return 0, 0, 0
	}
	active := func(d string) bool {
		v := daily[d]
		return v.Seconds > 0 || v.Submissions > 0 || v.Notes > 0
	}
	activeCount, longest, run := 0, 0, 0
	for _, d := range days {
		if active(d) {
			activeCount++
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	current := 0
	if active(days[len(days)-1]) {
		for i := len(days) - 1; i >= 0 && active(days[i]); i-- {
			current++
		}
	} else if len(days) >= 2 && active(days[len(days)-2]) {
		for i := len(days) - 2; i >= 0 && active(days[i]); i-- {
			current++
		}
	}
	return activeCount, current, longest
}

// sortedDates is handy for JSON consumers that need a stable order without
// re-sorting the map keys themselves.
func sortedDates(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for d := range m {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
