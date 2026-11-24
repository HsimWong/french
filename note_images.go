package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Note images live in the same SQLite file as the notes themselves. That keeps
// backups to a single file and avoids another path to configure on the VPS.
// The browser downscales before uploading, so a few MB per image is plenty.
const maxNoteImageBytes = 6 << 20

func registerNoteImageRoutes(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /api/note-images", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Data string `json:"data"` // data:image/jpeg;base64,... or raw base64
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 9<<20))
		if err := dec.Decode(&body); err != nil {
			writeErr(w, 400, "图片数据过大或格式错误")
			return
		}
		data := strings.TrimSpace(body.Data)
		if i := strings.IndexByte(data, ','); strings.HasPrefix(data, "data:") && i >= 0 {
			data = data[i+1:]
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			writeErr(w, 400, "图片编码错误")
			return
		}
		if len(raw) == 0 || len(raw) > maxNoteImageBytes {
			writeErr(w, 400, "图片不能超过 6 MB")
			return
		}
		// Trust the bytes, not the declared MIME type: only real image data is
		// stored, so a note image URL can never serve HTML or SVG.
		mime := http.DetectContentType(raw)
		switch mime {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			writeErr(w, 400, "只支持 JPG / PNG / GIF / WebP 图片")
			return
		}
		res, err := db.Exec(`INSERT INTO note_image(data, mime, size, created_at) VALUES(?,?,?,?)`,
			raw, mime, len(raw), time.Now().Format(time.RFC3339))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		// Relative URL: works both at / and behind an nginx prefix such as /french/.
		writeJSON(w, 201, map[string]any{"id": id, "url": noteImageURL(id)})
	})

	mux.HandleFunc("GET /api/note-images/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r, "id")
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		var raw []byte
		var mime string
		if err = db.QueryRow(`SELECT data, mime FROM note_image WHERE id=?`, id).Scan(&raw, &mime); err != nil {
			if err == sql.ErrNoRows {
				writeErr(w, 404, "图片不存在")
			} else {
				writeErr(w, 500, err.Error())
			}
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(raw)
	})
}

func noteImageURL(id int64) string {
	return "api/note-images/" + strconv.FormatInt(id, 10)
}

// pruneNoteImages removes uploads that no note references any more (abandoned
// drafts, deleted notes, images removed during editing).
func pruneNoteImages(db *sql.DB) {
	rows, err := db.Query(`SELECT id FROM note_image`)
	if err != nil {
		slog.Warn("prune note images", "error", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return
	}
	var sb strings.Builder
	textRows, err := db.Query(`SELECT text FROM note UNION ALL SELECT text FROM chapter_summary`)
	if err != nil {
		return
	}
	for textRows.Next() {
		var text string
		if textRows.Scan(&text) == nil {
			sb.WriteString(text)
		}
	}
	textRows.Close()
	all := sb.String()
	for _, id := range ids {
		if !strings.Contains(all, noteImageURL(id)) {
			if _, err = db.Exec(`DELETE FROM note_image WHERE id=?`, id); err != nil {
				slog.Warn("delete orphan note image", "id", id, "error", err)
			}
		}
	}
}
