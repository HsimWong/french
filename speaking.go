package main

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Camera-speaking practice adds three tables to schema.sql and stores video
// bytes on disk. Nothing in the existing course/attempt/note/lexicon schema
// is changed, so an old french.db keeps all of its data and behaviour.
const (
	maxSpeakingVideoBytes  = 60 << 20 // 60 MB is plenty for a 1-minute clip
	defaultSpeakingDir     = "data/speaking-videos"
	speakingRevisionMaxLen = 200000
)

type speakingRevision struct {
	ID        int64  `json:"id"`
	Version   int    `json:"version"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

type speakingVideo struct {
	ID           int64  `json:"id"`
	URL          string `json:"url"`
	OriginalName string `json:"original_name"`
	Mime         string `json:"mime"`
	Size         int64  `json:"size"`
	CreatedAt    string `json:"created_at"`
}

type speakingPractice struct {
	ID        int64              `json:"id"`
	Episode   int                `json:"episode"`
	Date      string             `json:"date"`
	Title     string             `json:"title"`
	Source    string             `json:"source"`
	Focus     string             `json:"focus"`
	Level     string             `json:"level"`
	Status    string             `json:"status"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
	Revisions []speakingRevision `json:"revisions"`
	Videos    []speakingVideo    `json:"videos"`
}

func speakingVideoDir() string {
	dir := strings.TrimSpace(os.Getenv("SPEAKING_VIDEO_DIR"))
	if dir == "" {
		dir = defaultSpeakingDir
	}
	return dir
}

func registerSpeakingRoutes(mux *http.ServeMux, db *sql.DB) {
	dir := speakingVideoDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("speaking video directory unavailable", "dir", dir, "error", err)
	}

	mux.HandleFunc("GET /api/speaking/practices", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`SELECT id, episode, practice_date, title_zh, source_zh,
			focus_zh, level, status, created_at, updated_at
			FROM speaking_practice ORDER BY episode DESC LIMIT 200`)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out := []speakingPractice{}
		for rows.Next() {
			var p speakingPractice
			if err := rows.Scan(&p.ID, &p.Episode, &p.Date, &p.Title, &p.Source,
				&p.Focus, &p.Level, &p.Status, &p.CreatedAt, &p.UpdatedAt); err != nil {
				rows.Close()
				writeErr(w, 500, err.Error())
				return
			}
			p.Revisions = []speakingRevision{}
			p.Videos = []speakingVideo{}
			out = append(out, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for i := range out {
			revRows, err := db.Query(`SELECT id, version, kind, text, note, created_at
				FROM speaking_revision WHERE practice_id=? ORDER BY version DESC`, out[i].ID)
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			for revRows.Next() {
				var v speakingRevision
				if err := revRows.Scan(&v.ID, &v.Version, &v.Kind, &v.Text, &v.Note, &v.CreatedAt); err != nil {
					revRows.Close()
					writeErr(w, 500, err.Error())
					return
				}
				out[i].Revisions = append(out[i].Revisions, v)
			}
			revRows.Close()

			vidRows, err := db.Query(`SELECT id, original_name, mime, size, created_at
				FROM speaking_video WHERE practice_id=? ORDER BY created_at DESC, id DESC`, out[i].ID)
			if err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			for vidRows.Next() {
				var v speakingVideo
				if err := vidRows.Scan(&v.ID, &v.OriginalName, &v.Mime, &v.Size, &v.CreatedAt); err != nil {
					vidRows.Close()
					writeErr(w, 500, err.Error())
					return
				}
				v.URL = speakingVideoURL(v.ID)
				out[i].Videos = append(out[i].Videos, v)
			}
			vidRows.Close()
		}
		writeJSON(w, 200, map[string]any{"practices": out, "total": len(out)})
	})

	// Upsert one prompt per episode/day, protected by the normal site session.
	mux.HandleFunc("POST /api/speaking/practices", func(w http.ResponseWriter, r *http.Request) {
		upsertSpeakingPractice(w, r, db)
	})

	// Minimal ingress for the daily automation. It requires a dedicated token
	// instead of the site passcode and can only upsert a practice prompt —
	// revisions, videos, notes and courses stay behind the full auth gate.
	mux.HandleFunc("POST /api/speaking/ingest", func(w http.ResponseWriter, r *http.Request) {
		if !speakingIngestConfigured() {
			writeErr(w, 503, "服务器未配置 SPEAKING_INGEST_TOKEN")
			return
		}
		if !validSpeakingIngestToken(r) {
			writeErr(w, 401, "发布令牌无效")
			return
		}
		upsertSpeakingPractice(w, r, db)
	})

	// Append-only revisions: every "submit correction" creates a new version,
	// so the history of drafts / feedback / final scripts is never overwritten.
	mux.HandleFunc("POST /api/speaking/practices/{id}/revisions", func(w http.ResponseWriter, r *http.Request) {
		practiceID, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var exists int64
		if err := db.QueryRow(`SELECT id FROM speaking_practice WHERE id=?`, practiceID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, 404, "练习不存在")
			} else {
				writeErr(w, 500, err.Error())
			}
			return
		}
		var in struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
			Note string `json:"note"`
		}
		if err := readJSON(r, &in); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		kind := strings.TrimSpace(in.Kind)
		if kind == "" {
			kind = "draft"
		}
		switch kind {
		case "draft", "correction", "final":
		default:
			writeErr(w, 400, "kind 只能是 draft / correction / final")
			return
		}
		text := strings.TrimSpace(in.Text)
		if text == "" {
			writeErr(w, 400, "内容不能为空")
			return
		}
		if len(text) > speakingRevisionMaxLen {
			writeErr(w, 400, "内容过长")
			return
		}
		var version int
		if err := db.QueryRow(`SELECT COALESCE(MAX(version),0)+1 FROM speaking_revision WHERE practice_id=?`,
			practiceID).Scan(&version); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		res, err := db.Exec(`INSERT INTO speaking_revision
			(practice_id, version, kind, text, note, created_at) VALUES(?,?,?,?,?,?)`,
			practiceID, version, kind, text, strings.TrimSpace(in.Note), time.Now().Format(time.RFC3339))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, 201, map[string]any{"id": id, "version": version, "kind": kind})
	})

	// Stream multipart upload straight to disk: buffering a 60 MB clip in the
	// 420 MB-memory VPS would put the whole site at risk.
	mux.HandleFunc("POST /api/speaking/practices/{id}/videos", func(w http.ResponseWriter, r *http.Request) {
		practiceID, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var exists int64
		if err := db.QueryRow(`SELECT id FROM speaking_practice WHERE id=?`, practiceID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, 404, "练习不存在")
			} else {
				writeErr(w, 500, err.Error())
			}
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			writeErr(w, 400, "请使用 multipart/form-data 上传")
			return
		}
		var tmp *os.File
		var storedName, originalName, mime string
		var size int64
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				if tmp != nil {
					tmp.Close()
					os.Remove(tmp.Name())
				}
				writeErr(w, 400, "读取上传文件失败")
				return
			}
			original := strings.TrimSpace(part.FileName())
			if part.FormName() != "video" || original == "" || tmp != nil {
				part.Close()
				continue
			}
			originalName = filepath.Base(original)
			ext := strings.ToLower(filepath.Ext(originalName))
			if ext != ".mp4" && ext != ".m4v" && ext != ".webm" {
				part.Close()
				writeErr(w, 400, "只支持 MP4 / M4V / WebM 视频")
				return
			}
			storedName, err = randomVideoName(ext)
			if err != nil {
				part.Close()
				writeErr(w, 500, "生成文件名失败")
				return
			}
			tmp, err = os.CreateTemp(dir, ".upload-*")
			if err != nil {
				part.Close()
				writeErr(w, 500, "无法创建临时文件")
				return
			}
			head := make([]byte, 512)
			n, err := io.ReadFull(part, head)
			if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				tmp.Close()
				os.Remove(tmp.Name())
				part.Close()
				writeErr(w, 400, "读取视频失败")
				return
			}
			head = head[:n]
			if _, err := tmp.Write(head); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				part.Close()
				writeErr(w, 500, "写入视频失败")
				return
			}
			remaining, err := io.Copy(tmp, io.LimitReader(part, maxSpeakingVideoBytes+1-int64(n)))
			part.Close()
			size = int64(n) + remaining
			if err != nil || size < 1024 || size > maxSpeakingVideoBytes {
				tmp.Close()
				os.Remove(tmp.Name())
				writeErr(w, 400, "视频大小需在 1 KB – 60 MB 之间")
				return
			}
			mime, err = detectSpeakingVideoMIME(head, ext)
			if err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				writeErr(w, 400, err.Error())
				return
			}
			if err := tmp.Close(); err != nil {
				os.Remove(tmp.Name())
				writeErr(w, 500, "保存视频失败")
				return
			}
			if err := os.Rename(tmp.Name(), filepath.Join(dir, storedName)); err != nil {
				os.Remove(tmp.Name())
				writeErr(w, 500, "保存视频失败")
				return
			}
			break
		}
		if tmp == nil {
			writeErr(w, 400, "未找到视频文件（字段名 video）")
			return
		}
		res, err := db.Exec(`INSERT INTO speaking_video
			(practice_id, stored_name, original_name, mime, size, created_at)
			VALUES(?,?,?,?,?,?)`, practiceID, storedName, originalName, mime, size, time.Now().Format(time.RFC3339))
		if err != nil {
			os.Remove(filepath.Join(dir, storedName))
			writeErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, 201, map[string]any{
			"id": id, "url": speakingVideoURL(id), "size": size, "mime": mime,
		})
	})

	mux.HandleFunc("GET /api/speaking/videos/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var storedName, originalName, mime string
		if err := db.QueryRow(`SELECT stored_name, original_name, mime FROM speaking_video WHERE id=?`, id).
			Scan(&storedName, &originalName, &mime); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, 404, "视频不存在")
			} else {
				writeErr(w, 500, err.Error())
			}
			return
		}
		if filepath.Base(storedName) != storedName {
			writeErr(w, 500, "视频文件名非法")
			return
		}
		f, err := os.Open(filepath.Join(dir, storedName))
		if err != nil {
			writeErr(w, 404, "视频文件不存在")
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		// ServeContent handles Range requests, so browsers can seek in long clips.
		http.ServeContent(w, r, originalName, time.Time{}, f)
	})
}

// upsertSpeakingPractice is shared by the session-protected page endpoint and
// the token-protected automation ingress. Keep validation in one place.
func upsertSpeakingPractice(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var in struct {
		Episode  int    `json:"episode"`
		Date     string `json:"date"`
		Title    string `json:"title"`
		Source   string `json:"source"`
		Focus    string `json:"focus"`
		Level    string `json:"level"`
		Status   string `json:"status"`
		TitleZh  string `json:"title_zh"`
		SourceZh string `json:"source_zh"`
		FocusZh  string `json:"focus_zh"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.Episode <= 0 {
		writeErr(w, 400, "episode 必须为正整数")
		return
	}
	date := strings.TrimSpace(in.Date)
	if date == "" {
		writeErr(w, 400, "date 格式应为 YYYY-MM-DD")
		return
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		writeErr(w, 400, "date 格式应为 YYYY-MM-DD")
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = strings.TrimSpace(in.TitleZh)
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = strings.TrimSpace(in.SourceZh)
	}
	if title == "" || source == "" {
		writeErr(w, 400, "title 和 source 不能为空")
		return
	}
	focus := strings.TrimSpace(in.Focus)
	if focus == "" {
		focus = strings.TrimSpace(in.FocusZh)
	}
	level := strings.TrimSpace(in.Level)
	if level == "" {
		level = "A2-B1"
	}
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = "active"
	}
	now := time.Now().Format(time.RFC3339)
	var existing int64
	existed := db.QueryRow(`SELECT id FROM speaking_practice WHERE episode=?`, in.Episode).
		Scan(&existing) == nil
	_, err := db.Exec(`INSERT INTO speaking_practice
		(episode, practice_date, title_zh, source_zh, focus_zh, level, status, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(episode) DO UPDATE SET
		  practice_date=excluded.practice_date,
		  title_zh=excluded.title_zh,
		  source_zh=excluded.source_zh,
		  focus_zh=excluded.focus_zh,
		  level=excluded.level,
		  status=excluded.status,
		  updated_at=excluded.updated_at`,
		in.Episode, date, title, source, focus, level, status, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "practice_date") {
			writeErr(w, 409, "该日期已属于其他期数")
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	var id int64
	if err := db.QueryRow(`SELECT id FROM speaking_practice WHERE episode=?`, in.Episode).Scan(&id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "episode": in.Episode, "updated": existed})
}

func speakingIngestConfigured() bool {
	return strings.TrimSpace(os.Getenv("SPEAKING_INGEST_TOKEN")) != ""
}

func validSpeakingIngestToken(r *http.Request) bool {
	want := strings.TrimSpace(os.Getenv("SPEAKING_INGEST_TOKEN"))
	if want == "" {
		return false
	}
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(got), "bearer ") {
		got = strings.TrimSpace(got[7:])
	} else {
		got = strings.TrimSpace(r.Header.Get("X-Speaking-Token"))
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func speakingVideoURL(id int64) string {
	return "api/speaking/videos/" + strconv.FormatInt(id, 10)
}

func randomVideoName(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + ext, nil
}

// Trust the bytes, not the extension or declared Content-Type: the served
// file must never be HTML/SVG that could execute in the site's origin.
func detectSpeakingVideoMIME(head []byte, ext string) (string, error) {
	sniffed := http.DetectContentType(head)
	if sniffed == "video/mp4" || sniffed == "video/webm" {
		return sniffed, nil
	}
	if len(head) >= 12 && string(head[4:8]) == "ftyp" {
		if ext == ".mp4" || ext == ".m4v" {
			return "video/mp4", nil
		}
	}
	if len(head) >= 4 && head[0] == 0x1A && head[1] == 0x45 && head[2] == 0xDF && head[3] == 0xA3 {
		if ext == ".webm" {
			return "video/webm", nil
		}
	}
	return "", errors.New("文件内容不是有效的 MP4 / WebM 视频")
}
