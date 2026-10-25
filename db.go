package main

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sources (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL,
  site_url      TEXT NOT NULL UNIQUE,
  feed_url      TEXT NOT NULL DEFAULT '',  -- empty: no feed, watch the page for new links
  mode          TEXT NOT NULL DEFAULT 'tell', -- tell | auto
  etag          TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  checked_at    INTEGER NOT NULL DEFAULT 0,
  check_error   TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS items (
  id           INTEGER PRIMARY KEY,
  url          TEXT NOT NULL UNIQUE,
  kind         TEXT NOT NULL DEFAULT 'article', -- article | site
  title        TEXT NOT NULL DEFAULT '',
  site         TEXT NOT NULL DEFAULT '',
  excerpt      TEXT NOT NULL DEFAULT '',
  content_html TEXT NOT NULL DEFAULT '',
  content_text TEXT NOT NULL DEFAULT '',
  minutes      INTEGER NOT NULL DEFAULT 0,
  note         TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL DEFAULT 'stack',   -- stack | read | dropped
  fetch_state  TEXT NOT NULL DEFAULT 'pending', -- pending | ok | failed
  position     REAL NOT NULL,                   -- higher is nearer the top of the stack
  scroll       REAL NOT NULL DEFAULT 0,         -- how far through the reader, 0..1
  source_id    INTEGER REFERENCES sources(id) ON DELETE SET NULL,
  saved_at     INTEGER NOT NULL,
  kept_at      INTEGER NOT NULL,                -- reset by "Keep" in dust review
  read_at      INTEGER
);

CREATE TABLE IF NOT EXISTS posts (
  id           INTEGER PRIMARY KEY,
  source_id    INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  guid         TEXT NOT NULL,
  url          TEXT NOT NULL,
  title        TEXT NOT NULL,
  published_at INTEGER NOT NULL,
  minutes      INTEGER NOT NULL DEFAULT 0,
  state        TEXT NOT NULL, -- new | stack | skipped | old (published before you followed)
  item_id      INTEGER REFERENCES items(id) ON DELETE SET NULL,
  UNIQUE (source_id, guid)
);

CREATE VIRTUAL TABLE IF NOT EXISTS items_fts USING fts5(title, content_text, content='items', content_rowid='id');

CREATE TRIGGER IF NOT EXISTS items_ai AFTER INSERT ON items BEGIN
  INSERT INTO items_fts(rowid, title, content_text) VALUES (new.id, new.title, new.content_text);
END;
CREATE TRIGGER IF NOT EXISTS items_ad AFTER DELETE ON items BEGIN
  INSERT INTO items_fts(items_fts, rowid, title, content_text) VALUES ('delete', old.id, old.title, old.content_text);
END;
CREATE TRIGGER IF NOT EXISTS items_au AFTER UPDATE OF title, content_text ON items BEGIN
  INSERT INTO items_fts(items_fts, rowid, title, content_text) VALUES ('delete', old.id, old.title, old.content_text);
  INSERT INTO items_fts(rowid, title, content_text) VALUES (new.id, new.title, new.content_text);
END;

-- A highlight is anchored by character offsets into the reader's text, with the quote and the text
-- around it kept so it can be found again if the saved text changes.
CREATE TABLE IF NOT EXISTS highlights (
  id         INTEGER PRIMARY KEY,
  item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  quote      TEXT NOT NULL,
  prefix     TEXT NOT NULL DEFAULT '',
  suffix     TEXT NOT NULL DEFAULT '',
  pos_start  INTEGER NOT NULL DEFAULT 0,
  pos_end    INTEGER NOT NULL DEFAULT 0,
  note       TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS highlights_item ON highlights(item_id);

CREATE VIRTUAL TABLE IF NOT EXISTS highlights_fts USING fts5(quote, note, content='highlights', content_rowid='id');

CREATE TRIGGER IF NOT EXISTS highlights_ai AFTER INSERT ON highlights BEGIN
  INSERT INTO highlights_fts(rowid, quote, note) VALUES (new.id, new.quote, new.note);
END;
CREATE TRIGGER IF NOT EXISTS highlights_ad AFTER DELETE ON highlights BEGIN
  INSERT INTO highlights_fts(highlights_fts, rowid, quote, note) VALUES ('delete', old.id, old.quote, old.note);
END;
CREATE TRIGGER IF NOT EXISTS highlights_au AFTER UPDATE OF quote, note ON highlights BEGIN
  INSERT INTO highlights_fts(highlights_fts, rowid, quote, note) VALUES ('delete', old.id, old.quote, old.note);
  INSERT INTO highlights_fts(rowid, quote, note) VALUES (new.id, new.quote, new.note);
END;
`

func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// One connection keeps SQLite writes simple; this app has one user.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// Added after the first release; fails harmlessly once the column exists.
	db.Exec(`ALTER TABLE items ADD COLUMN opened_at INTEGER NOT NULL DEFAULT 0`)
	return db, nil
}
