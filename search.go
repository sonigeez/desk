package main

import (
	"context"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// ---- search everything (Cmd/Ctrl+K) ----

// Snippet markers: private-use characters that saved text doesn't contain,
// swapped for <mark> after the text has been escaped.
const markOpen, markClose = "\ue000", "\ue001"

// hitCols is itemCols for queries that join items (as i) to the search index.
const hitCols = `i.id, i.url, i.kind, i.title, i.site, i.excerpt, i.minutes, i.note, i.status, i.fetch_state, i.scroll,
	i.saved_at, i.kept_at, COALESCE(i.read_at, 0), COALESCE(i.source_id, 0), i.opened_at`

var searchPage = template.Must(template.New("").Funcs(funcs).ParseFS(assets, "templates/search.html"))

type Hit struct {
	Item
	Place   string        // Stack, Shelf or Dust
	TitleHL template.HTML // title with matching words marked
	Snippet template.HTML // a few words around the match in the saved text
}

type searchData struct {
	Hits  []Hit
	Sites []SiteHint // site suggestions while typing "site:"
}

// parseSearch pulls a site:example.com filter out of what was typed.
// typing is true while the site is still being typed, so it is matched as a prefix and suggested.
func parseSearch(q string) (words, site string, typing bool) {
	fields := strings.Fields(q)
	var rest []string
	for i, f := range fields {
		v, ok := strings.CutPrefix(strings.ToLower(f), "site:")
		switch {
		case !ok:
			rest = append(rest, f)
		case i == len(fields)-1 && !strings.HasSuffix(q, " "):
			site, typing = v, true
		default:
			site = v
		}
	}
	return strings.Join(rest, " "), strings.TrimPrefix(site, "www."), typing
}

// marked escapes FTS output and turns its markers into <mark>.
func marked(s string) template.HTML {
	s = html.EscapeString(s)
	s = strings.ReplaceAll(s, markOpen, "<mark>")
	return template.HTML(strings.ReplaceAll(s, markClose, "</mark>"))
}

// search looks through the stack, the shelf and dust (never what was let go of).
// in is "", "stack", "shelf" or "dust". Title matches rank first, then the best text matches, then site-only matches.
func (a *App) search(ctx context.Context, q, in string) (searchData, error) {
	words, site, typing := parseSearch(q)
	var d searchData
	cutoff := dustCutoff()
	where := "i.status IN ('stack', 'read')"
	args := []any{}
	switch in {
	case "stack":
		where = "i.status = 'stack' AND i.kept_at >= ?"
		args = append(args, cutoff)
	case "dust":
		where = "i.status = 'stack' AND i.kept_at < ?"
		args = append(args, cutoff)
	case "shelf":
		where = "i.status = 'read'"
	}
	if typing {
		sites, err := a.siteSuggestions(ctx, where, args, site)
		if err != nil {
			return d, err
		}
		d.Sites = sites
	}
	if site != "" {
		// The site column holds the site's own name once fetched, so match the address too.
		// A site still being typed matches as a prefix.
		s, end := likeEscape(site), "/%"
		if typing {
			s, end = s+"%", ""
		}
		where += ` AND (i.site LIKE ? ESCAPE '\' OR i.url LIKE ? ESCAPE '\' OR i.url LIKE ? ESCAPE '\')`
		args = append(args, s, "%://"+s+end, "%://www."+s+end)
	}

	fq := ftsQuery(words)
	var query string
	if fq == "" {
		// Nothing typed yet: the most recently touched pieces.
		query = `SELECT ` + hitCols + `, i.title, '' FROM items i WHERE ` + where + `
			ORDER BY MAX(i.saved_at, i.kept_at, COALESCE(i.read_at, 0), i.opened_at) DESC LIMIT 12`
	} else {
		// f has every row matching the words; t the ones whose title matches them. Both are
		// materialized so the MATCH runs once rather than once per item.
		query = `WITH f AS MATERIALIZED (SELECT rowid, bm25(items_fts, 8.0, 1.0) AS rank FROM items_fts WHERE items_fts MATCH ?),
			     t AS MATERIALIZED (SELECT rowid FROM items_fts WHERE items_fts MATCH ?)
			SELECT ` + hitCols + `, i.title, ''
			FROM items i LEFT JOIN f ON f.rowid = i.id
			WHERE ` + where + ` AND (f.rowid IS NOT NULL OR i.site LIKE ? ESCAPE '\' OR i.url LIKE ? ESCAPE '\')
			ORDER BY i.id NOT IN (SELECT rowid FROM t), f.rowid IS NULL, f.rank, i.saved_at DESC LIMIT 30`
		args = append([]any{fq, "title : (" + fq + ")"}, args...)
		like := "%" + likeEscape(words) + "%"
		args = append(args, like, like)
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var h Hit
		var title, snip string
		it := &h.Item
		if err := rows.Scan(&it.ID, &it.URL, &it.Kind, &it.Title, &it.Site, &it.Excerpt, &it.Minutes, &it.Note,
			&it.Status, &it.FetchState, &it.Scroll, &it.SavedAt, &it.KeptAt, &it.ReadAt, &it.SourceID, &it.OpenedAt,
			&title, &snip); err != nil {
			return d, err
		}
		h.Place = "Shelf"
		if it.Status == "stack" {
			h.Place = "Stack"
			if it.KeptAt < cutoff {
				h.Place = "Dust"
			}
		}
		d.Hits = append(d.Hits, h)
		titles = append(titles, title)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	rows.Close()
	snips, err := a.snippets(ctx, fq, d.Hits)
	if err != nil {
		return d, err
	}
	for i := range d.Hits {
		h := &d.Hits[i]
		title, snip := titles[i], ""
		if m, ok := snips[h.ID]; ok {
			title, snip = m[0], m[1]
		}
		if title == "" {
			title = h.URL // not fetched yet
		}
		h.TitleHL = marked(title)
		if strings.Contains(snip, markOpen) {
			h.Snippet = marked(snip)
		} else if h.Excerpt != "" && h.Excerpt != h.Title {
			h.Snippet = template.HTML(html.EscapeString(h.Excerpt))
		}
	}
	return d, nil
}

// snippets marks the matching words in the title and text of just the hits being shown.
func (a *App) snippets(ctx context.Context, fq string, hits []Hit) (map[int64][2]string, error) {
	out := map[int64][2]string{}
	if fq == "" || len(hits) == 0 {
		return out, nil
	}
	marks := make([]string, len(hits))
	for i, h := range hits {
		marks[i] = strconv.FormatInt(h.ID, 10)
	}
	args := []any{fq}
	// One pass over the matches, marking only the hits shown. (MATCH with rowid IN re-runs the
	// match for every id, which is slow for a short prefix like "a".)
	rows, err := a.db.QueryContext(ctx, `SELECT * FROM (SELECT rowid AS id,
		CASE WHEN rowid IN (`+strings.Join(marks, ",")+`) THEN highlight(items_fts, 0, '`+markOpen+`', '`+markClose+`') END AS title,
		CASE WHEN rowid IN (`+strings.Join(marks, ",")+`) THEN snippet(items_fts, 1, '`+markOpen+`', '`+markClose+`', '…', 18) END AS snip
		FROM items_fts WHERE items_fts MATCH ?) WHERE title IS NOT NULL`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var m [2]string
		if err := rows.Scan(&id, &m[0], &m[1]); err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, rows.Err()
}

type SiteHint struct{ Host, Name string }

// siteSuggestions lists the sites, most saved first, whose address or name starts with prefix.
func (a *App) siteSuggestions(ctx context.Context, where string, args []any, prefix string) ([]SiteHint, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT url, site FROM items i WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	count := map[SiteHint]int{}
	for rows.Next() {
		var link, name string
		if err := rows.Scan(&link, &name); err != nil {
			return nil, err
		}
		u, err := url.Parse(link)
		if err != nil {
			continue
		}
		h := SiteHint{Host: strings.TrimPrefix(u.Hostname(), "www."), Name: name}
		if strings.HasPrefix(h.Host, prefix) || strings.HasPrefix(strings.ToLower(name), prefix) {
			if h.Name == h.Host {
				h.Name = ""
			}
			count[h]++
		}
	}
	var out []SiteHint
	for h := range count {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i].Host < out[j].Host
	})
	return out[:min(6, len(out))], rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (a *App) searchResults(w http.ResponseWriter, r *http.Request) {
	d, err := a.search(r.Context(), r.URL.Query().Get("q"), r.URL.Query().Get("in"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := searchPage.ExecuteTemplate(w, "results", d); err != nil {
		log.Printf("render search: %v", err)
	}
}
