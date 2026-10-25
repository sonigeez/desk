package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

// ---- templates ----

var funcs = template.FuncMap{
	"ago": ago,
	"agoShort": func(ts int64) string {
		d := int(time.Since(time.Unix(ts, 0)).Hours() / 24)
		switch {
		case d <= 0:
			return "today"
		case d < 60:
			return fmt.Sprintf("%dd", d)
		}
		return fmt.Sprintf("%dmo", d/30)
	},
	"since": since,
	"mins":  mins,
	"host": func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		return strings.TrimPrefix(u.Hostname(), "www.")
	},
	"inc": func(i int) int { return i + 1 },
	"sub": func(a, b int) int { return a - b },
	// postday is the date on a postmark, like "25 OCT".
	"upper":   strings.ToUpper,
	"postday": func(ts int64) string { return strings.ToUpper(time.Unix(ts, 0).Format("2 Jan")) },
	// faded is true for letters that have waited more than two weeks.
	"faded":    func(ts int64) bool { return time.Since(time.Unix(ts, 0)) > 14*24*time.Hour },
	"started":  started,
	"minsLeft": func(it Item) int { return max(1, int(float64(it.Minutes)*(1-it.Scroll)+0.5)) },
	// recent is true for the last two days, when "new" still means news.
	"recent": func(ts int64) bool { return time.Since(time.Unix(ts, 0)) < 48*time.Hour },
	"every": func(days int) string {
		if days == 1 {
			return "every day"
		}
		return fmt.Sprintf("every %d days", days)
	},
	"pct":   func(f float64) int { return int(f * 100) },
	"cloth": cloth,
	"bookStyle": func(it Item, i int) template.CSS {
		h := min(78, 30+it.Minutes*11/10)
		if it.Kind == "site" {
			h = 52 // a whole site has no reading time, so it gets a plain medium thickness
		} else if it.Minutes == 0 {
			h = 32
		}
		offsets := []int{0, 14, 5, 20, 2, 11, 8, 17}
		return template.CSS(fmt.Sprintf("--h:%dpx;--cloth:var(--%s);--off-l:%dpx;--off-r:%dpx",
			h, cloth(it.URL), offsets[i%8], offsets[(i+3)%8]))
	},
	"spineStyle": func(it Item) template.CSS {
		w := max(20, min(64, 16+it.Minutes*11/10))
		return template.CSS(fmt.Sprintf("--w:%dpx;--h:%dpx;--cloth:var(--%s)", w, 128+int(hash(it.Title)%40), cloth(it.URL)))
	},
}

func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// started is true for an article you are partway through.
func started(it Item) bool {
	return it.Scroll > 0.02 && it.Scroll < 0.97 && it.Kind == "article" && it.FetchState == "ok"
}

// cloth picks a binding colour for a link, the same one each time.
func cloth(link string) string { return fmt.Sprintf("c%d", hash(link)%8+1) }

func ago(ts int64) string {
	if ts == 0 {
		return "never"
	}
	d := int(time.Since(time.Unix(ts, 0)).Hours() / 24)
	switch {
	case d <= 0:
		return "today"
	case d == 1:
		return "yesterday"
	case d < 60:
		return fmt.Sprintf("%d days ago", d)
	case d < 730:
		return fmt.Sprintf("%d months ago", d/30)
	}
	return fmt.Sprintf("%d years ago", d/365)
}

// since is ago with finer steps, for things that happen within the day.
func since(ts int64) string {
	d := time.Since(time.Unix(ts, 0))
	switch {
	case ts == 0:
		return "never"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return ago(ts)
}

func mins(m int) string {
	if m >= 60 {
		return fmt.Sprintf("%dh %02dm", m/60, m%60)
	}
	return fmt.Sprintf("%d min", m)
}

func loadPages() map[string]*template.Template {
	pages := map[string]*template.Template{}
	for _, name := range []string{"desk", "wire", "reader", "session", "done", "dust", "shelf", "follow", "source", "login"} {
		pages[name] = template.Must(template.New("").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/"+name+".html"))
	}
	return pages
}

type page struct {
	Title   string
	Nav     string
	WireNew int // posts waiting on the Wire, shown in the menu
	Auth    bool
	D       any
}

func (a *App) render(w http.ResponseWriter, name, title, nav string, d any) {
	p := page{Title: title, Nav: nav, Auth: a.password != "", D: d}
	a.db.QueryRow(`SELECT COUNT(*) FROM posts WHERE state = 'new'`).Scan(&p.WireNew)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.pages[name].ExecuteTemplate(w, "layout", p); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func fail(w http.ResponseWriter, err error) {
	log.Print(err)
	http.Error(w, "Something went wrong: "+err.Error(), http.StatusInternalServerError)
}

// back sends the browser to the page the form was on, dropping any one-off notice.
func back(w http.ResponseWriter, r *http.Request) { backWith(w, r, "", "") }

// backWith is back plus a query value, such as the item a notice is about.
func backWith(w http.ResponseWriter, r *http.Request, key, val string) {
	to := &url.URL{Path: "/"}
	if u, err := url.Parse(r.Referer()); err == nil && u.Host == r.Host {
		to = u
	}
	q := to.Query()
	for _, k := range []string{"gone", "done", "skipped"} {
		q.Del(k)
	}
	if key != "" {
		q.Set(key, val)
	}
	to.RawQuery = q.Encode()
	http.Redirect(w, r, to.RequestURI(), http.StatusSeeOther)
}

func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func parseIDs(s string) []int64 {
	var ids []int64
	for _, p := range strings.Split(s, ",") {
		if id, err := strconv.ParseInt(p, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func joinIDs(its []Item) string {
	var parts []string
	for _, it := range its {
		parts = append(parts, strconv.FormatInt(it.ID, 10))
	}
	return strings.Join(parts, ",")
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.Contains(u.Host, ".") && u.Hostname() != "localhost" {
		return "", errors.New("that doesn't look like a web address")
	}
	return u.String(), nil
}

// ---- login ----

func (a *App) sessionToken() string {
	mac := hmac.New(sha256.New, []byte(a.password))
	mac.Write([]byte("desk session"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *App) requireLogin(next http.Handler) http.Handler {
	if a.password == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie("desk"); err == nil && hmac.Equal([]byte(c.Value), []byte(a.sessionToken())) {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "login", "Sign in", "", "")
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if a.password == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(a.password)) != 1 {
		time.Sleep(time.Second)
		a.render(w, "login", "Sign in", "", "That password didn't match. Try again.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "desk", Value: a.sessionToken(), Path: "/", MaxAge: 365 * 86400,
		HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "desk", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- desk ----

type deskData struct {
	Self        string
	Budget      int
	Stack       []Item
	Picked      map[int64]bool
	PickCount   int
	PickMinutes int
	Total       int
	Pending     bool
	Dust        []Item
	Continue    *Item // the half-read piece opened most recently
	Gone        *Item // just let go of, offered for undo
	Done        *Item // just marked read, offered for undo
	Error       string
}

func (a *App) desk(w http.ResponseWriter, r *http.Request) {
	d := deskData{Self: r.URL.RequestURI(), Picked: map[int64]bool{}, Error: r.URL.Query().Get("error")}
	d.Budget, _ = strconv.Atoi(r.URL.Query().Get("min"))
	var err error
	if d.Stack, err = a.stack(); err != nil {
		fail(w, err)
		return
	}
	for _, it := range d.Stack {
		d.Total += it.Minutes
		d.Pending = d.Pending || it.FetchState == "pending"
	}
	if d.Budget > 0 {
		for _, it := range pick(d.Stack, d.Budget) {
			d.Picked[it.ID] = true
			d.PickCount++
			d.PickMinutes += it.Minutes
		}
	}
	for key, dst := range map[string]**Item{"gone": &d.Gone, "done": &d.Done} {
		if id, err := strconv.ParseInt(r.URL.Query().Get(key), 10, 64); err == nil {
			if it, err := a.item(id); err == nil {
				*dst = &it
			}
		}
	}
	for i, it := range d.Stack {
		if started(it) && (d.Continue == nil || it.OpenedAt > d.Continue.OpenedAt) {
			d.Continue = &d.Stack[i]
		}
	}
	d.Dust, _ = a.dust()
	a.render(w, "desk", "Desk", "desk", d)
}

// surprise opens a random piece from the stack.
func (a *App) surprise(w http.ResponseWriter, r *http.Request) {
	var id int64
	a.db.QueryRow(`SELECT id FROM items WHERE status = 'stack' AND kind = 'article' AND fetch_state = 'ok' AND kept_at >= ?
		ORDER BY RANDOM() LIMIT 1`, dustCutoff()).Scan(&id)
	if id == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/read/%d", id), http.StatusSeeOther)
}

// collect checks every followed blog now instead of waiting for the hourly round.
func (a *App) collect(w http.ResponseWriter, r *http.Request) {
	srcs, err := a.sourcesList("")
	if err != nil {
		fail(w, err)
		return
	}
	for _, s := range srcs {
		if err := a.checkSource(s, false, false); err != nil {
			log.Printf("check %s: %v", s.Name, err)
		}
	}
	http.Redirect(w, r, "/wire", http.StatusSeeOther)
}

type wireData struct {
	Fresh         []Post
	Sources       []SourceView
	Skipped       *Post // just skipped, offered for undo
	LastCollected int64
}

func (a *App) wire(w http.ResponseWriter, r *http.Request) {
	var d wireData
	var err error
	if d.Fresh, err = a.freshPosts(); err != nil {
		fail(w, err)
		return
	}
	if d.Sources, err = a.sourceViews(26, ""); err != nil {
		fail(w, err)
		return
	}
	for _, s := range d.Sources {
		d.LastCollected = max(d.LastCollected, s.CheckedAt)
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("skipped"), 10, 64); err == nil {
		if ps, err := a.posts(`WHERE p.id = ?`, id); err == nil && len(ps) == 1 {
			d.Skipped = &ps[0]
		}
	}
	a.render(w, "wire", "The Wire", "wire", d)
}

func (a *App) saveItem(w http.ResponseWriter, r *http.Request) {
	u, err := normalizeURL(r.FormValue("url"))
	if err != nil {
		http.Redirect(w, r, "/?error="+url.QueryEscape("Couldn't save: "+err.Error()+"."), http.StatusSeeOther)
		return
	}
	kind := "article"
	if r.FormValue("kind") == "site" {
		kind = "site"
	}
	if _, err := a.addItem(u, kind, "", 0); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) itemAction(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var err error
	switch path.Base(r.URL.Path) {
	case "top":
		err = a.moveToTop(id)
	case "keep":
		err = a.keep(id)
	case "drop":
		if err = a.setStatus(id, "dropped"); err == nil {
			backWith(w, r, "gone", strconv.FormatInt(id, 10))
			return
		}
	case "article":
		// Saved as a whole site by mistake: make it an article and fetch it again for its reading time.
		if _, err = a.db.Exec(`UPDATE items SET kind = 'article', note = '', fetch_state = 'pending' WHERE id = ?`, id); err == nil {
			go a.fetchItem(id)
		}
	case "refetch":
		if _, err = a.db.Exec(`UPDATE items SET fetch_state = 'pending' WHERE id = ?`, id); err == nil {
			go a.fetchItem(id)
		}
	case "unread":
		err = a.setStatus(id, "stack")
	case "note":
		_, err = a.db.Exec(`UPDATE items SET note = ? WHERE id = ?`, strings.TrimSpace(r.FormValue("note")), id)
	case "scroll":
		f, _ := strconv.ParseFloat(r.FormValue("f"), 64)
		_, err = a.db.Exec(`UPDATE items SET scroll = ? WHERE id = ?`, max(0, min(1, f)), id)
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	if err != nil {
		fail(w, err)
		return
	}
	back(w, r)
}

// ---- reading ----

type readerData struct {
	Item    Item
	Content template.HTML
	S       string // ids of the session this piece belongs to
	Pos     int
	Count   int
	Next    *Item
	Session []sessionPiece
	Cols    template.CSS // one grid track per piece, sized by its minutes
}

type sessionPiece struct {
	Item    Item
	Done    bool
	Current bool
}

func (a *App) reader(w http.ResponseWriter, r *http.Request) {
	it, err := a.item(pathID(r))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var content string
	a.db.QueryRow(`SELECT content_html FROM items WHERE id = ?`, it.ID).Scan(&content)
	d := readerData{Item: it, Content: template.HTML(content)} // sanitized when it was saved
	a.db.Exec(`UPDATE items SET opened_at = ? WHERE id = ?`, time.Now().Unix(), it.ID)
	if s := r.URL.Query().Get("s"); s != "" {
		ids := parseIDs(s)
		var cols []string
		for i, id := range ids {
			piece, err := a.item(id)
			if err != nil {
				continue
			}
			d.Session = append(d.Session, sessionPiece{Item: piece, Done: piece.Status == "read", Current: id == it.ID})
			cols = append(cols, fmt.Sprintf("%dfr", max(1, piece.Minutes)))
			if id == it.ID {
				d.S, d.Pos, d.Count = s, i+1, len(ids)
				if i+1 < len(ids) {
					if next, err := a.item(ids[i+1]); err == nil {
						d.Next = &next
					}
				}
			}
		}
		d.Cols = template.CSS(strings.Join(cols, " "))
	}
	a.render(w, "reader", it.Title, "desk", d)
}

func (a *App) markRead(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := a.setStatus(id, "read"); err != nil {
		fail(w, err)
		return
	}
	s := r.FormValue("s")
	ids := parseIDs(s)
	for i, sid := range ids {
		if sid == id && i+1 < len(ids) {
			http.Redirect(w, r, fmt.Sprintf("/read/%d?s=%s", ids[i+1], s), http.StatusSeeOther)
			return
		}
	}
	if len(ids) > 0 {
		http.Redirect(w, r, "/session/done?ids="+s, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/?done=%d", id), http.StatusSeeOther)
}

type sessionData struct {
	Budget int
	Picks  []Item
	Used   int
	IDs    string
}

func (a *App) session(w http.ResponseWriter, r *http.Request) {
	d := sessionData{}
	d.Budget, _ = strconv.Atoi(r.URL.Query().Get("min"))
	if d.Budget <= 0 {
		d.Budget = 30
	}
	stack, err := a.stack()
	if err != nil {
		fail(w, err)
		return
	}
	d.Picks = pick(stack, d.Budget)
	for _, it := range d.Picks {
		d.Used += it.Minutes
	}
	d.IDs = joinIDs(d.Picks)
	a.render(w, "session", "Your "+mins(d.Budget), "desk", d)
}

func (a *App) sessionDone(w http.ResponseWriter, r *http.Request) {
	var done []Item
	total := 0
	for _, id := range parseIDs(r.URL.Query().Get("ids")) {
		if it, err := a.item(id); err == nil && it.Status == "read" {
			done = append(done, it)
			total += it.Minutes
		}
	}
	a.render(w, "done", "Session done", "desk", map[string]any{"Items": done, "Minutes": total})
}

func (a *App) dustPage(w http.ResponseWriter, r *http.Request) {
	dust, err := a.dust()
	if err != nil {
		fail(w, err)
		return
	}
	d := map[string]any{"Items": dust}
	if id, err := strconv.ParseInt(r.URL.Query().Get("gone"), 10, 64); err == nil {
		if it, err := a.item(id); err == nil {
			d["Gone"] = it
		}
	}
	a.render(w, "dust", "Gathering dust", "desk", d)
}

func (a *App) shelfPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	weeks, err := a.shelf(q)
	if err != nil {
		fail(w, err)
		return
	}
	count, total := 0, 0
	for _, wk := range weeks {
		count += len(wk.Items)
		total += wk.Minutes
	}
	year, err := a.readingYear(3, 61)
	if err != nil {
		fail(w, err)
		return
	}
	a.render(w, "shelf", "Shelf", "shelf", map[string]any{"Q": q, "Weeks": weeks, "Count": count, "Minutes": total, "Year": year})
}

// ---- following ----

type followData struct {
	URL     string
	Error   string
	Preview *Preview
}

func (a *App) followPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "follow", "Follow a blog", "follow", followData{})
}

func (a *App) followFind(w http.ResponseWriter, r *http.Request) {
	d := followData{URL: r.FormValue("url")}
	u, err := normalizeURL(d.URL)
	if err == nil {
		var p Preview
		if p, err = discover(u); err == nil {
			p.Posts = p.Posts[:min(5, len(p.Posts))]
			d.Preview = &p
		}
	}
	if err != nil {
		d.Error = "Couldn't open that blog: " + err.Error()
	}
	a.render(w, "follow", "Follow a blog", "follow", d)
}

func (a *App) follow(w http.ResponseWriter, r *http.Request) {
	mode := "tell"
	if r.FormValue("mode") == "auto" {
		mode = "auto"
	}
	s := Source{Name: strings.TrimSpace(r.FormValue("name")), SiteURL: r.FormValue("site_url"), FeedURL: r.FormValue("feed_url"), Mode: mode}
	if s.Name == "" {
		s.Name = s.SiteURL
	}
	err := a.db.QueryRow(`INSERT INTO sources (name, site_url, feed_url, mode, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (site_url) DO UPDATE SET mode = excluded.mode RETURNING id`,
		s.Name, s.SiteURL, s.FeedURL, s.Mode, time.Now().Unix()).Scan(&s.ID)
	if err != nil {
		fail(w, err)
		return
	}
	if err := a.checkSource(s, true, r.FormValue("backfill") == "on"); err != nil {
		log.Printf("first check of %s: %v", s.Name, err)
	}
	http.Redirect(w, r, fmt.Sprintf("/sources/%d", s.ID), http.StatusSeeOther)
}

type sourceData struct {
	Source SourceView
	Posts  []Post
}

func (a *App) sourcePage(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	views, err := a.sourceViews(52, "WHERE id = ?", id)
	if err != nil {
		fail(w, err)
		return
	}
	if len(views) == 0 {
		http.NotFound(w, r)
		return
	}
	posts, err := a.posts(`WHERE p.source_id = ? ORDER BY p.published_at DESC LIMIT 100`, id)
	if err != nil {
		fail(w, err)
		return
	}
	a.render(w, "source", views[0].Name, "desk", sourceData{Source: views[0], Posts: posts})
}

func (a *App) sourceAction(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var err error
	switch path.Base(r.URL.Path) {
	case "mode":
		mode := "tell"
		if r.FormValue("mode") == "auto" {
			mode = "auto"
		}
		_, err = a.db.Exec(`UPDATE sources SET mode = ? WHERE id = ?`, mode, id)
	case "check":
		var srcs []Source
		if srcs, err = a.sourcesList(`WHERE id = ?`, id); err == nil && len(srcs) == 1 {
			if cerr := a.checkSource(srcs[0], false, false); cerr != nil {
				log.Printf("check %s: %v", srcs[0].Name, cerr)
			}
		}
	case "unfollow":
		if _, err = a.db.Exec(`DELETE FROM sources WHERE id = ?`, id); err == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	if err != nil {
		fail(w, err)
		return
	}
	back(w, r)
}

func (a *App) postAction(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var err error
	switch path.Base(r.URL.Path) {
	case "add":
		err = a.addPostToStack(id)
	case "unskip":
		_, err = a.db.Exec(`UPDATE posts SET state = 'new' WHERE id = ? AND state = 'skipped'`, id)
	default:
		if err = a.skipPost(id); err == nil {
			backWith(w, r, "skipped", strconv.FormatInt(id, 10))
			return
		}
	}
	if err != nil {
		fail(w, err)
		return
	}
	back(w, r)
}
