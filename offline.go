package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
)

// Phones keep a copy of at most this many pieces from the top of the stack.
const offlineLimit = 50

// serviceWorker serves static/sw.js with permission to control the whole app, not just /static/.
func serviceWorker(w http.ResponseWriter, r *http.Request) {
	js, err := fs.ReadFile(assets, "static/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(js)
}

// offlinePage is what the service worker shows for pages it has no copy of.
func (a *App) offlinePage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "offline", "Offline", "desk", nil)
}

type offlinePiece struct {
	ID      int64   `json:"id"`
	Title   string  `json:"title"`
	Site    string  `json:"site"`
	Minutes int     `json:"minutes"`
	Scroll  float64 `json:"scroll"`
	Size    int     `json:"size"`  // changes when the text is fetched again
	Marks   string  `json:"marks"` // changes when a highlight or note is added, edited or removed
}

// offlineList tells the service worker which reader pages to keep. It sits behind the sign-in,
// so a signed-out phone gets the login page here and keeps nothing.
func (a *App) offlineList(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id, title, site, minutes, scroll, length(content_html),
		(SELECT COUNT(*) || ':' || COALESCE(MAX(id), 0) || ':' || COALESCE(SUM(length(note)), 0) FROM highlights WHERE item_id = items.id)
		FROM items
		WHERE status = 'stack' AND kept_at >= ? AND kind = 'article' AND fetch_state = 'ok'
		ORDER BY position DESC LIMIT ?`, dustCutoff(), offlineLimit)
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []offlinePiece{}
	for rows.Next() {
		var p offlinePiece
		if err := rows.Scan(&p.ID, &p.Title, &p.Site, &p.Minutes, &p.Scroll, &p.Size, &p.Marks); err != nil {
			fail(w, err)
			return
		}
		out = append(out, p)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}
