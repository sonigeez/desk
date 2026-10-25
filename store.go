package main

import (
	"database/sql"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Items untouched for this long move off the stack into dust review.
const dustAfter = 60 * 24 * time.Hour

type Item struct {
	ID         int64
	URL        string
	Kind       string
	Title      string
	Site       string
	Excerpt    string
	Minutes    int
	Note       string
	Status     string
	FetchState string
	Scroll     float64
	SavedAt    int64
	KeptAt     int64
	ReadAt     int64
	SourceID   int64
	OpenedAt   int64
}

const itemCols = `id, url, kind, title, site, excerpt, minutes, note, status, fetch_state, scroll,
	saved_at, kept_at, COALESCE(read_at, 0), COALESCE(source_id, 0), opened_at`

func (a *App) items(where string, args ...any) ([]Item, error) {
	rows, err := a.db.Query("SELECT "+itemCols+" FROM items "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.URL, &it.Kind, &it.Title, &it.Site, &it.Excerpt, &it.Minutes, &it.Note,
			&it.Status, &it.FetchState, &it.Scroll, &it.SavedAt, &it.KeptAt, &it.ReadAt, &it.SourceID, &it.OpenedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (a *App) item(id int64) (Item, error) {
	its, err := a.items("WHERE id = ?", id)
	if err != nil {
		return Item{}, err
	}
	if len(its) == 0 {
		return Item{}, sql.ErrNoRows
	}
	return its[0], nil
}

func dustCutoff() int64 { return time.Now().Add(-dustAfter).Unix() }

func (a *App) stack() ([]Item, error) {
	return a.items("WHERE status = 'stack' AND kept_at >= ? ORDER BY position DESC", dustCutoff())
}

func (a *App) dust() ([]Item, error) {
	return a.items("WHERE status = 'stack' AND kept_at < ? ORDER BY kept_at", dustCutoff())
}

// addItem puts a link on top of the stack and fetches its text in the background.
// Saving a link that is already known brings it back to the top.
func (a *App) addItem(rawURL, kind, title string, sourceID int64) (int64, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	var id int64
	var state string
	err = a.db.QueryRow(`
		INSERT INTO items (url, kind, title, site, position, saved_at, kept_at, source_id)
		VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM items), ?, ?, NULLIF(?, 0))
		ON CONFLICT (url) DO UPDATE SET status = 'stack', position = excluded.position, kept_at = excluded.kept_at
		RETURNING id, fetch_state`,
		u.String(), kind, title, strings.TrimPrefix(u.Hostname(), "www."), now, now, sourceID).Scan(&id, &state)
	if err != nil {
		return 0, err
	}
	if state != "ok" {
		go a.fetchItem(id)
	}
	return id, nil
}

func (a *App) moveToTop(id int64) error {
	_, err := a.db.Exec(`UPDATE items SET position = (SELECT MAX(position) + 1 FROM items) WHERE id = ?`, id)
	return err
}

func (a *App) keep(id int64) error {
	_, err := a.db.Exec(`UPDATE items SET position = (SELECT MAX(position) + 1 FROM items), kept_at = ? WHERE id = ?`,
		time.Now().Unix(), id)
	return err
}

func (a *App) setStatus(id int64, status string) error {
	var readAt any
	if status == "read" {
		readAt = time.Now().Unix()
	}
	_, err := a.db.Exec(`UPDATE items SET status = ?, read_at = ? WHERE id = ?`, status, readAt, id)
	return err
}

// pick walks down the stack and takes every piece that still fits in the time left.
func pick(stack []Item, budget int) []Item {
	var out []Item
	left := budget
	for _, it := range stack {
		if it.Kind == "article" && it.FetchState == "ok" && it.Minutes > 0 && it.Minutes <= left {
			out = append(out, it)
			left -= it.Minutes
		}
	}
	return out
}

func weekStart() int64 {
	now := time.Now()
	day := (int(now.Weekday()) + 6) % 7 // Monday = 0
	y, m, d := now.Date()
	return time.Date(y, m, d-day, 0, 0, 0, 0, now.Location()).Unix()
}

// ---- shelf ----

type ShelfWeek struct {
	Label   string
	Items   []Item
	Minutes int
}

func (a *App) shelf(q string) ([]ShelfWeek, error) {
	var its []Item
	var err error
	if q = ftsQuery(q); q != "" {
		its, err = a.items(`WHERE status = 'read' AND id IN (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)
			ORDER BY read_at DESC`, q)
	} else {
		its, err = a.items(`WHERE status = 'read' ORDER BY read_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	thisWeek := weekStart()
	var weeks []ShelfWeek
	for _, it := range its {
		start := thisWeek
		if it.ReadAt < thisWeek {
			start -= ((thisWeek-it.ReadAt-1)/(7*86400) + 1) * 7 * 86400
		}
		label := weekLabel(start, thisWeek)
		if len(weeks) == 0 || weeks[len(weeks)-1].Label != label {
			weeks = append(weeks, ShelfWeek{Label: label})
		}
		w := &weeks[len(weeks)-1]
		w.Items = append(w.Items, it)
		w.Minutes += it.Minutes
	}
	return weeks, nil
}

func weekLabel(start, thisWeek int64) string {
	switch {
	case start >= thisWeek:
		return "This week"
	case start >= thisWeek-7*86400:
		return "Last week"
	}
	s := time.Unix(start, 0)
	e := s.AddDate(0, 0, 6)
	if s.Month() == e.Month() {
		return s.Format("Jan 2") + " – " + e.Format("2")
	}
	return s.Format("Jan 2") + " – " + e.Format("Jan 2")
}

// ftsQuery turns what the user typed into a safe FTS5 prefix query.
func ftsQuery(q string) string {
	var terms []string
	for _, w := range strings.Fields(q) {
		w = strings.ReplaceAll(w, `"`, "")
		if w != "" {
			terms = append(terms, `"`+w+`"*`)
		}
	}
	return strings.Join(terms, " ")
}

// ---- sources and posts ----

type Source struct {
	ID         int64
	Name       string
	SiteURL    string
	FeedURL    string
	Mode       string
	CheckError string
	CheckedAt  int64
}

type SourceView struct {
	Source
	NewCount    int
	Total       int
	Read        int
	LastPost    int64
	Every       int      // usual days between posts, 0 if unknown
	Quiet       bool     // longer than usual since the last post
	Weeks       []string // one CSS class per week, oldest first
	ActiveWeeks int      // weeks with at least one post
	Months      []string
}

type Post struct {
	ID          int64
	SourceID    int64
	SourceName  string
	URL         string
	Title       string
	PublishedAt int64
	Minutes     int
	State       string // new | stack | skipped | old | read | dropped
}

func (a *App) sourcesList(where string, args ...any) ([]Source, error) {
	rows, err := a.db.Query(`SELECT id, name, site_url, feed_url, mode, check_error, checked_at FROM sources `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.ID, &s.Name, &s.SiteURL, &s.FeedURL, &s.Mode, &s.CheckError, &s.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (a *App) posts(where string, args ...any) ([]Post, error) {
	rows, err := a.db.Query(`
		SELECT p.id, p.source_id, s.name, p.url, p.title, p.published_at, COALESCE(NULLIF(i.minutes, 0), p.minutes),
		       CASE WHEN i.status IN ('read', 'dropped') THEN i.status ELSE p.state END
		FROM posts p JOIN sources s ON s.id = p.source_id LEFT JOIN items i ON i.id = p.item_id `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.SourceID, &p.SourceName, &p.URL, &p.Title, &p.PublishedAt, &p.Minutes, &p.State); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (a *App) freshPosts() ([]Post, error) {
	return a.posts(`WHERE p.state = 'new' ORDER BY p.published_at DESC LIMIT 40`)
}

// sourceViews adds posting rhythm and counts to each source.
func (a *App) sourceViews(weeks int, where string, args ...any) ([]SourceView, error) {
	srcs, err := a.sourcesList(where, args...)
	if err != nil {
		return nil, err
	}
	all, err := a.posts(`ORDER BY p.published_at DESC`)
	if err != nil {
		return nil, err
	}
	byID := map[int64][]Post{}
	for _, p := range all {
		byID[p.SourceID] = append(byID[p.SourceID], p)
	}
	now := time.Now().Unix()
	out := make([]SourceView, len(srcs))
	for i, s := range srcs {
		v := SourceView{Source: s, Weeks: make([]string, weeks)}
		ps := byID[s.ID]
		v.Total = len(ps)
		var times []int64
		for _, p := range ps {
			switch p.State {
			case "new":
				v.NewCount++
			case "read":
				v.Read++
			}
			times = append(times, p.PublishedAt)
			if ago := int((now - p.PublishedAt) / (7 * 86400)); ago >= 0 && ago < weeks {
				v.Weeks[weeks-1-ago] = "on"
			}
		}
		for _, w := range v.Weeks {
			if w != "" {
				v.ActiveWeeks++
			}
		}
		if v.Weeks[weeks-1] == "on" {
			v.Weeks[weeks-1] = "on recent"
		}
		if len(times) > 0 {
			v.LastPost = times[0]
		}
		v.Every = usualGap(times)
		v.Quiet = v.Every > 0 && float64(now-v.LastPost) > 1.5*float64(v.Every)*86400 && now-v.LastPost > 7*86400
		for w := 0; w < weeks; w += weeks / 6 {
			v.Months = append(v.Months, time.Unix(now-int64(weeks-1-w)*7*86400, 0).Format("Jan"))
		}
		out[i] = v
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastPost > out[j].LastPost })
	return out, nil
}

// usualGap is the median number of days between the latest posts.
func usualGap(desc []int64) int {
	if len(desc) > 11 {
		desc = desc[:11]
	}
	var gaps []int64
	for i := 1; i < len(desc); i++ {
		if g := desc[i-1] - desc[i]; g > 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return 0
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return max(1, int(gaps[len(gaps)/2]/86400))
}

func (a *App) addPostToStack(postID int64) error {
	ps, err := a.posts(`WHERE p.id = ?`, postID)
	if err != nil || len(ps) == 0 {
		return err
	}
	itemID, err := a.addItem(ps[0].URL, "article", ps[0].Title, ps[0].SourceID)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`UPDATE posts SET state = 'stack', item_id = ? WHERE id = ?`, itemID, postID)
	return err
}

func (a *App) skipPost(postID int64) error {
	_, err := a.db.Exec(`UPDATE posts SET state = 'skipped' WHERE id = ?`, postID)
	return err
}

// ---- day by day ----

type ReadingDay struct {
	Height int
	Cloth  string // empty when nothing was read that day
	Label  string // "Thu 23 Oct · 24 min"
}

type DayShelf struct {
	From, To string
	Days     []ReadingDay
}

type ReadingYear struct {
	Shelves  []DayShelf
	DaysRead int
	Minutes  int
	BestDay  string // the weekday with the most reading, like "Sundays"
}

// readingYear lays out the last shelves*perShelf days, one spine per day:
// taller for more minutes read, in the cloth of that day's longest piece.
func (a *App) readingYear(shelves, perShelf int) (ReadingYear, error) {
	now := time.Now()
	y, m, d := now.Date()
	first := time.Date(y, m, d-shelves*perShelf+1, 0, 0, 0, 0, now.Location())
	its, err := a.items(`WHERE status = 'read' AND read_at >= ?`, first.Unix())
	if err != nil {
		return ReadingYear{}, err
	}
	type day struct {
		minutes, longest int
		url              string
	}
	byDate := map[string]*day{}
	for _, it := range its {
		key := time.Unix(it.ReadAt, 0).Format("2006-01-02")
		if byDate[key] == nil {
			byDate[key] = &day{}
		}
		dd := byDate[key]
		dd.minutes += it.Minutes
		if dd.url == "" || it.Minutes > dd.longest {
			dd.longest, dd.url = it.Minutes, it.URL
		}
	}
	var yr ReadingYear
	byWeekday := map[time.Weekday]int{}
	for s := 0; s < shelves; s++ {
		start := first.AddDate(0, 0, s*perShelf)
		sh := DayShelf{From: start.Format("2 Jan"), To: start.AddDate(0, 0, perShelf-1).Format("2 Jan")}
		for i := 0; i < perShelf; i++ {
			t := start.AddDate(0, 0, i)
			rd := ReadingDay{Height: 3, Label: t.Format("Mon 2 Jan") + " · nothing read"}
			if dd := byDate[t.Format("2006-01-02")]; dd != nil {
				rd = ReadingDay{Height: min(72, 14+dd.minutes), Cloth: cloth(dd.url), Label: t.Format("Mon 2 Jan") + " · " + mins(dd.minutes)}
				yr.DaysRead++
				yr.Minutes += dd.minutes
				byWeekday[t.Weekday()] += dd.minutes
			}
			sh.Days = append(sh.Days, rd)
		}
		yr.Shelves = append(yr.Shelves, sh)
	}
	best := -1
	for wd, m := range byWeekday {
		if m > best {
			best, yr.BestDay = m, wd.String()+"s"
		}
	}
	return yr, nil
}
