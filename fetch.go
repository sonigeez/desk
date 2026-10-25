package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Desk/1.0"

var (
	client = &http.Client{Timeout: 20 * time.Second}
	// probeClient is for guessing feed addresses, which often just don't exist.
	probeClient = &http.Client{Timeout: 6 * time.Second}
	sanitize    = bluemonday.UGCPolicy()
	tagRe       = regexp.MustCompile(`<[^>]*>`)
)

// get fetches a URL and returns the response and its body (capped at 10 MB).
func get(u string, header map[string]string) (*http.Response, []byte, error) {
	return getWith(client, u, header)
}

func getWith(c *http.Client, u string, header map[string]string) (*http.Response, []byte, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range header {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return resp, nil, nil
	}
	if resp.StatusCode >= 400 {
		return resp, nil, fmt.Errorf("%s answered %s", u, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	return resp, body, err
}

func words(s string) int { return len(strings.Fields(s)) }

func readingMinutes(wordCount int) int { return max(1, (wordCount+229)/230) }

// fetchItem downloads a saved link and stores its readable text.
func (a *App) fetchItem(id int64) {
	it, err := a.item(id)
	if err != nil {
		return
	}
	resp, body, err := get(it.URL, nil)
	var art readability.Article
	if err == nil {
		art, err = readability.FromReader(bytes.NewReader(body), resp.Request.URL)
	}
	if err != nil {
		log.Printf("fetch %s: %v", it.URL, err)
		a.db.Exec(`UPDATE items SET fetch_state = 'failed', title = CASE WHEN title = '' THEN url ELSE title END WHERE id = ?`, id)
		return
	}
	text := strings.TrimSpace(art.TextContent)
	title := strings.TrimSpace(art.Title)
	if title == "" {
		title = it.Title
	}
	if title == "" {
		title = it.URL
	}
	site := strings.TrimSpace(art.SiteName)
	if site == "" {
		site = it.Site
	}
	minutes := readingMinutes(words(text))
	if it.Kind == "site" {
		minutes = 0
	}
	_, err = a.db.Exec(`UPDATE items SET title = ?, site = ?, excerpt = ?, content_html = ?, content_text = ?,
		minutes = ?, fetch_state = 'ok' WHERE id = ?`,
		title, site, strings.TrimSpace(art.Excerpt), sanitize.Sanitize(art.Content), text, minutes, id)
	if err != nil {
		log.Printf("save %s: %v", it.URL, err)
	}
}

// resumeFetches retries links that were still being fetched when the app last stopped.
func (a *App) resumeFetches() {
	its, err := a.items(`WHERE fetch_state = 'pending'`)
	if err != nil {
		return
	}
	for _, it := range its {
		a.fetchItem(it.ID)
	}
}

// ---- following blogs ----

type FeedPost struct {
	GUID      string
	Title     string
	URL       string
	Published int64
	Minutes   int
}

type Preview struct {
	Name    string
	SiteURL string
	FeedURL string // empty when the blog has no feed and the page is watched instead
	Posts   []FeedPost
	Every   int
}

func feedPosts(f *gofeed.Feed) []FeedPost {
	now := time.Now().Unix()
	var out []FeedPost
	for _, it := range f.Items {
		if it.Link == "" {
			continue
		}
		p := FeedPost{GUID: it.GUID, Title: strings.TrimSpace(it.Title), URL: it.Link, Published: now}
		if p.GUID == "" {
			p.GUID = it.Link
		}
		if p.Title == "" {
			p.Title = it.Link
		}
		if it.PublishedParsed != nil {
			p.Published = it.PublishedParsed.Unix()
		} else if it.UpdatedParsed != nil {
			p.Published = it.UpdatedParsed.Unix()
		}
		// Many feeds carry the full post; a short summary says nothing about length.
		if n := words(tagRe.ReplaceAllString(it.Content+" "+it.Description, " ")); n > 150 {
			p.Minutes = readingMinutes(n)
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Published > out[j].Published })
	return out
}

// scanPage pulls the title, advertised feeds and same-site links out of an HTML page.
func scanPage(body []byte, base *url.URL) (title string, feeds []string, links []FeedPost) {
	z := html.NewTokenizer(bytes.NewReader(body))
	seen := map[string]bool{}
	var href, text string
	inA, inTitle := false, false
	now := time.Now().Unix()
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			attr := map[string]string{}
			for _, at := range t.Attr {
				attr[at.Key] = at.Val
			}
			switch t.Data {
			case "title":
				inTitle = title == ""
			case "link":
				typ := attr["type"]
				if strings.Contains(attr["rel"], "alternate") && (strings.Contains(typ, "rss") || strings.Contains(typ, "atom") || strings.Contains(typ, "feed+json")) {
					if u, err := base.Parse(attr["href"]); err == nil {
						feeds = append(feeds, u.String())
					}
				}
			case "a":
				inA, href, text = true, attr["href"], ""
			}
		case html.TextToken:
			if inTitle {
				title += string(z.Text())
			}
			if inA {
				text += string(z.Text())
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				inTitle = false
			case "a":
				inA = false
				u, err := base.Parse(href)
				text = strings.Join(strings.Fields(text), " ")
				if err != nil || u.Host != base.Host || len(text) < 12 || strings.Trim(u.Path, "/") == "" || u.Path == base.Path {
					continue
				}
				if skipPath.MatchString(u.Path) {
					continue
				}
				u.Fragment, u.RawQuery = "", ""
				if !seen[u.String()] && len(links) < 60 {
					seen[u.String()] = true
					links = append(links, FeedPost{GUID: u.String(), Title: text, URL: u.String(), Published: now})
				}
			}
		}
	}
}

var skipPath = regexp.MustCompile(`(?i)/(tags?|categor(y|ies)|page|about|contact|archives?|search|login|feed|rss)(/|$)`)

// discover finds the feed for a blog, or falls back to watching its page.
func discover(raw string) (Preview, error) {
	resp, body, err := get(raw, nil)
	if err != nil {
		return Preview{}, err
	}
	base := resp.Request.URL
	parser := gofeed.NewParser()
	if f, err := parser.Parse(bytes.NewReader(body)); err == nil {
		// The pasted address was the feed itself.
		site := base.Scheme + "://" + base.Host
		if f.Link != "" {
			site = f.Link
		}
		return previewFromFeed(f, site, base.String()), nil
	}
	title, feeds, links := scanPage(body, base)
	for _, p := range []string{"/feed", "/rss.xml", "/atom.xml", "/feed.xml", "/index.xml", "/rss", "/feed/"} {
		feeds = append(feeds, base.Scheme+"://"+base.Host+p)
	}
	for _, fu := range feeds {
		_, fb, err := getWith(probeClient, fu, nil)
		if err != nil {
			continue
		}
		if f, err := parser.Parse(bytes.NewReader(fb)); err == nil && len(f.Items) > 0 {
			p := previewFromFeed(f, base.String(), fu)
			if t := strings.TrimSpace(title); t != "" && p.Name == "" {
				p.Name = t
			}
			return p, nil
		}
	}
	name := strings.TrimSpace(title)
	if name == "" {
		name = strings.TrimPrefix(base.Hostname(), "www.")
	}
	return Preview{Name: name, SiteURL: base.String(), Posts: links}, nil
}

func previewFromFeed(f *gofeed.Feed, site, feed string) Preview {
	posts := feedPosts(f)
	var times []int64
	for _, p := range posts {
		times = append(times, p.Published)
	}
	name := strings.TrimSpace(f.Title)
	if name == "" {
		if u, err := url.Parse(site); err == nil {
			name = strings.TrimPrefix(u.Hostname(), "www.")
		}
	}
	return Preview{Name: name, SiteURL: site, FeedURL: feed, Posts: posts, Every: usualGap(times)}
}

// checkSource looks for posts that haven't been seen before.
// On the first check everything already published is marked "old";
// with backfill the latest three go on the stack.
func (a *App) checkSource(s Source, initial, backfill bool) error {
	var posts []FeedPost
	etag, lastMod := "", ""
	if s.FeedURL != "" {
		hdr := map[string]string{}
		if !initial {
			hdr["If-None-Match"], hdr["If-Modified-Since"] = a.sourceCache(s.ID)
		}
		resp, body, err := get(s.FeedURL, hdr)
		if err != nil {
			a.db.Exec(`UPDATE sources SET checked_at = ?, check_error = ? WHERE id = ?`, time.Now().Unix(), err.Error(), s.ID)
			return err
		}
		if resp.StatusCode == http.StatusNotModified {
			_, err := a.db.Exec(`UPDATE sources SET checked_at = ?, check_error = '' WHERE id = ?`, time.Now().Unix(), s.ID)
			return err
		}
		f, err := gofeed.NewParser().Parse(bytes.NewReader(body))
		if err != nil {
			a.db.Exec(`UPDATE sources SET checked_at = ?, check_error = ? WHERE id = ?`, time.Now().Unix(), "feed could not be read", s.ID)
			return err
		}
		posts = feedPosts(f)
		etag, lastMod = resp.Header.Get("ETag"), resp.Header.Get("Last-Modified")
	} else {
		resp, body, err := get(s.SiteURL, nil)
		if err != nil {
			a.db.Exec(`UPDATE sources SET checked_at = ?, check_error = ? WHERE id = ?`, time.Now().Unix(), err.Error(), s.ID)
			return err
		}
		_, _, posts = scanPage(body, resp.Request.URL)
	}

	var toStack []FeedPost
	for _, p := range posts {
		state := "new"
		if initial {
			state = "old"
		} else if s.Mode == "auto" {
			state = "stack"
		}
		res, err := a.db.Exec(`INSERT OR IGNORE INTO posts (source_id, guid, url, title, published_at, minutes, state)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, s.ID, p.GUID, p.URL, p.Title, p.Published, p.Minutes, state)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 && state == "stack" {
			toStack = append(toStack, p)
		}
	}
	if initial && backfill {
		toStack = posts[:min(3, len(posts))]
	}
	// Oldest first, so the newest ends up on top.
	for i := len(toStack) - 1; i >= 0; i-- {
		p := toStack[i]
		itemID, err := a.addItem(p.URL, "article", p.Title, s.ID)
		if err != nil {
			log.Printf("add %s: %v", p.URL, err)
			continue
		}
		a.db.Exec(`UPDATE posts SET state = 'stack', item_id = ? WHERE source_id = ? AND guid = ?`, itemID, s.ID, p.GUID)
	}
	_, err := a.db.Exec(`UPDATE sources SET checked_at = ?, check_error = '', etag = ?, last_modified = ? WHERE id = ?`,
		time.Now().Unix(), etag, lastMod, s.ID)
	return err
}

func (a *App) sourceCache(id int64) (etag, lastMod string) {
	a.db.QueryRow(`SELECT etag, last_modified FROM sources WHERE id = ?`, id).Scan(&etag, &lastMod)
	return
}

// pollFeeds checks every followed blog, then waits and does it again.
func (a *App) pollFeeds(every time.Duration) {
	for {
		srcs, err := a.sourcesList(`WHERE checked_at < ?`, time.Now().Add(-every).Add(time.Minute).Unix())
		if err != nil {
			log.Printf("poll: %v", err)
		}
		for _, s := range srcs {
			if err := a.checkSource(s, false, false); err != nil {
				log.Printf("check %s: %v", s.Name, err)
			}
		}
		time.Sleep(min(every, 5*time.Minute))
	}
}
