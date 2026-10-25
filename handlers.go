package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	for _, name := range []string{"desk", "wire", "reader", "session", "done", "dust", "shelf", "highlights", "follow", "source", "login", "save", "share", "offline"} {
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
		to := "/login"
		// A page asked for while signed out, such as a shared link, opens once you sign in.
		if r.Method == http.MethodGet && r.URL.Path != "/" {
			to += "?next=" + url.QueryEscape(r.URL.RequestURI())
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
	})
}

type loginData struct {
	Error string
	Next  string
}

// localPath keeps a post-login redirect on this site: a path, never another host.
func localPath(next string) string {
	// Browsers drop tabs and newlines and read \ as /, so "/\t/evil.com" would leave the site.
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\t\r\n") {
		return "/"
	}
	return next
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "login", "Sign in", "", loginData{Next: localPath(r.URL.Query().Get("next"))})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	next := localPath(r.FormValue("next"))
	if a.password == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(a.password)) != 1 {
		time.Sleep(time.Second)
		a.render(w, "login", "Sign in", "", loginData{Error: "That password didn't match. Try again.", Next: next})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "desk", Value: a.sessionToken(), Path: "/", MaxAge: 365 * 86400,
		HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, next, http.StatusSeeOther)
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
	Continue    *Item                 // the half-read piece opened most recently
	Gone        *Item                 // just let go of, offered for undo
	Done        *Item                 // just marked read, offered for undo
	Highlights  map[int64][]Highlight // by item, in reading order, for the covers
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
	d.Highlights = map[int64][]Highlight{}
	hs, _ := a.highlights(`WHERE i.status = 'stack' ORDER BY h.item_id, h.pos_start`)
	for _, h := range hs {
		d.Highlights[h.ItemID] = append(d.Highlights[h.ItemID], h)
	}
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
	if _, err := a.addItem(u, guessKind(u), "", 0); err != nil {
		fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---- saving from another page ----

// saveKey goes in the bookmarklet's address. With a password set, a save that arrives without it
// (a link or redirect from some other site) asks first instead of saving straight away.
func (a *App) saveKey() string {
	if a.password == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(a.password))
	mac.Write([]byte("desk bookmarklet"))
	return hex.EncodeToString(mac.Sum(nil))[:20]
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]+$`)

// origin is the address this Desk was reached at, like https://desk.example.com.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	if !hostPattern.MatchString(host) {
		host = "localhost" // a forged Host header must not write script into the bookmark
	}
	return scheme + "://" + host
}

// bookmarklet is a bookmark that opens /save on this Desk with the page you're on.
func (a *App) bookmarklet(r *http.Request) template.URL {
	save := origin(r) + "/save?"
	if k := a.saveKey(); k != "" {
		save += "k=" + k + "&"
	}
	return template.URL("javascript:void(location.href=" + strconv.Quote(save+"url=") + "+encodeURIComponent(location.href))")
}

type saveData struct {
	Bookmarklet template.URL
	URL         string
	Error       string
	Ask         bool  // came without the bookmarklet's key, so it waits for a yes
	Item        *Item // what was saved
	Again       bool  // it was already on the stack
	Self        string
	Origin      string // this Desk's address, for the iPhone shortcut to ask for
}

// savePage is where the bookmarklet lands. A url is saved, then shown at /save?id=… so a refresh
// or Back doesn't save it again. With no url the page explains the bookmarklet.
func (a *App) savePage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := saveData{Bookmarklet: a.bookmarklet(r), URL: q.Get("url"), Self: r.URL.RequestURI(), Origin: origin(r)}
	if id, err := strconv.ParseInt(q.Get("id"), 10, 64); err == nil {
		if it, err := a.item(id); err == nil {
			d.Item, d.Again = &it, q.Has("again")
			a.render(w, "save", "Saved", "", d)
			return
		}
	}
	if d.URL == "" {
		a.render(w, "save", "Save from your browser", "save", d)
		return
	}
	// With no password there's no key, so every save asks.
	k := q.Get("k")
	a.saveLink(w, r, d, k != "" && hmac.Equal([]byte(k), []byte(a.saveKey())))
}

// saveConfirmed is the yes to a save that asked first.
func (a *App) saveConfirmed(w http.ResponseWriter, r *http.Request) {
	a.saveLink(w, r, saveData{Bookmarklet: a.bookmarklet(r), URL: r.FormValue("url")}, true)
}

func (a *App) saveLink(w http.ResponseWriter, r *http.Request, d saveData, sure bool) {
	u, err := normalizeURL(d.URL)
	if p, perr := url.Parse(u); err == nil && perr == nil && p.Host == r.Host {
		err = errors.New("that's a page of Desk itself")
	}
	switch {
	case err != nil:
		d.Error = "“" + d.URL + "” can't go on your stack: " + err.Error() + "."
		a.render(w, "save", "Couldn't save", "", d)
	case !sure:
		d.Ask, d.URL = true, u
		a.render(w, "save", "Save this page?", "", d)
	default:
		var again bool
		a.db.QueryRow(`SELECT COUNT(*) > 0 FROM items WHERE url = ? AND status = 'stack'`, u).Scan(&again)
		id, err := a.addItem(u, guessKind(u), "", 0)
		if err != nil {
			fail(w, err)
			return
		}
		to := fmt.Sprintf("/save?id=%d", id)
		if again {
			to += "&again=1"
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
}

var linkPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

// sharedLink finds the first web address in what another app shared. Apps put it in url, text or
// title, often with words around it, as in "Worth a read https://example.com/post".
func sharedLink(r *http.Request) string {
	for _, field := range []string{"url", "text", "title"} {
		if link := linkPattern.FindString(r.FormValue(field)); link != "" {
			link = strings.TrimRight(link, ".,;:!?'")
			if !strings.Contains(link, "(") {
				link = strings.TrimRight(link, ")")
			}
			return link
		}
	}
	// A bare address with no scheme, like example.com/post.
	return r.FormValue("url")
}

type shareData struct {
	URL   string
	Site  string
	Error string
	Got   string // what was shared, shown when no link was found in it
	Ask   bool   // opened from another site, so save only once you confirm
}

// share is the Web Share Target: Android's share sheet (and the iPhone shortcut) opens it with the link.
func (a *App) share(w http.ResponseWriter, r *http.Request) {
	d := shareData{}
	u, err := normalizeURL(sharedLink(r))
	if err != nil {
		d.Error = "No link found"
		d.Got = strings.TrimSpace(strings.Join([]string{r.FormValue("title"), r.FormValue("text"), r.FormValue("url")}, " "))
		a.render(w, "share", "Nothing saved", "", d)
		return
	}
	d.URL = u
	if p, err := url.Parse(u); err == nil {
		d.Site = strings.TrimPrefix(p.Hostname(), "www.")
	}
	// The share sheet and the shortcut open this directly. A link or redirect from another site
	// would carry the sign-in cookie too, so that asks before saving.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" && site != "same-origin" {
		d.Ask = true
		a.render(w, "share", "Save this?", "", d)
		return
	}
	if _, err := a.addItem(u, guessKind(u), "", 0); err != nil {
		fail(w, err)
		return
	}
	a.render(w, "share", "Saved", "", d)
}

// guessKind treats a site's home page as a whole site to read through, and anything deeper as an article.
func guessKind(link string) string {
	if u, err := url.Parse(link); err == nil && strings.Trim(u.Path, "/") == "" && u.RawQuery == "" {
		return "site"
	}
	return "article"
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
	case "site":
		_, err = a.db.Exec(`UPDATE items SET kind = 'site', minutes = 0 WHERE id = ?`, id)
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
	Item       Item
	Content    template.HTML
	S          string // ids of the session this piece belongs to
	Pos        int
	Count      int
	Next       *Item
	Session    []sessionPiece
	Cols       template.CSS // one grid track per piece, sized by its minutes
	Highlights []Highlight
	Done       *Item // the piece just finished, offered for undo
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
	if d.Highlights, err = a.highlights(`WHERE h.item_id = ? ORDER BY h.pos_start`, it.ID); err != nil {
		fail(w, err)
		return
	}
	if d.Highlights == nil {
		d.Highlights = []Highlight{}
	}
	// The service worker keeping a copy for offline isn't you opening it.
	if r.Header.Get("X-Desk-Offline") == "" {
		a.db.Exec(`UPDATE items SET opened_at = ? WHERE id = ?`, time.Now().Unix(), it.ID)
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("done"), 10, 64); err == nil {
		if done, err := a.item(id); err == nil {
			d.Done = &done
		}
	}
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
	// Finishing a piece in the reader moves on to the next one on the stack.
	if r.FormValue("from") == "reader" {
		if next := a.nextToRead(); next != 0 {
			http.Redirect(w, r, fmt.Sprintf("/read/%d?done=%d", next, id), http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/?done=%d", id), http.StatusSeeOther)
}

// nextToRead is the top piece on the stack that can be read in the reader, or 0.
func (a *App) nextToRead() int64 {
	stack, _ := a.stack()
	for _, it := range stack {
		if it.Kind == "article" && it.FetchState == "ok" {
			return it.ID
		}
	}
	return 0
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
	found, err := a.searchHighlights(q)
	if err != nil {
		fail(w, err)
		return
	}
	var marked int
	a.db.QueryRow(`SELECT COUNT(*) FROM highlights`).Scan(&marked)
	a.render(w, "shelf", "Shelf", "shelf", map[string]any{"Q": q, "Weeks": weeks, "Count": count, "Minutes": total, "Year": year,
		"Found": found, "Marked": marked})
}

func (a *App) highlightsPage(w http.ResponseWriter, r *http.Request) {
	hs, err := a.highlights(`ORDER BY h.created_at DESC, h.id DESC`)
	if err != nil {
		fail(w, err)
		return
	}
	a.render(w, "highlights", "Highlights", "shelf", hs)
}

// ---- highlights ----

// clip keeps user text to a sane size without cutting a character in half.
func clip(s string, n int) string {
	r := []rune(s)
	return string(r[:min(len(r), n)])
}

func (a *App) addHighlightHandler(w http.ResponseWriter, r *http.Request) {
	if utf8.RuneCountInString(r.FormValue("quote")) > 4000 {
		http.Error(w, "That's too long to highlight.", http.StatusBadRequest)
		return
	}
	h := Highlight{ItemID: pathID(r), Quote: r.FormValue("quote"), Prefix: clip(r.FormValue("prefix"), 64),
		Suffix: clip(r.FormValue("suffix"), 64), Note: clip(strings.TrimSpace(r.FormValue("note")), 4000)}
	h.Start, _ = strconv.Atoi(r.FormValue("start"))
	h.End, _ = strconv.Atoi(r.FormValue("end"))
	if strings.TrimSpace(h.Quote) == "" || h.Start < 0 || h.End <= h.Start {
		http.Error(w, "Nothing was selected.", http.StatusBadRequest)
		return
	}
	if _, err := a.item(h.ItemID); err != nil {
		http.NotFound(w, r)
		return
	}
	id, err := a.addHighlight(h)
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"id": id})
}

func (a *App) highlightAction(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	var err error
	switch path.Base(r.URL.Path) {
	case "note":
		_, err = a.db.Exec(`UPDATE highlights SET note = ? WHERE id = ?`, clip(strings.TrimSpace(r.FormValue("note")), 4000), id)
	case "delete":
		_, err = a.db.Exec(`DELETE FROM highlights WHERE id = ?`, id)
	}
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
