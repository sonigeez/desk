package main

import (
	"database/sql"
	"embed"
	"html/template"
	"log"
	"mime"
	"net/http"
	"os"
	"time"
)

//go:embed templates static
var assets embed.FS

type App struct {
	db       *sql.DB
	password string
	pages    map[string]*template.Template
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	addr := env("DESK_ADDR", ":8080")
	every, err := time.ParseDuration(env("DESK_CHECK_EVERY", "1h"))
	if err != nil {
		log.Fatalf("DESK_CHECK_EVERY: %v", err)
	}
	db, err := openDB(env("DESK_DB", "data/reading.db"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	app := &App{db: db, password: os.Getenv("DESK_PASSWORD"), pages: loadPages()}
	if app.password == "" {
		log.Print("DESK_PASSWORD is not set, so anyone who can reach this port can use the app")
	}

	go app.resumeFetches()
	go app.pollFeeds(every)

	log.Printf("Desk is running on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, app.routes()))
}

func (a *App) routes() http.Handler {
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(assets))
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)

	mux.HandleFunc("GET /{$}", a.desk)
	mux.HandleFunc("POST /items", a.saveItem)
	mux.HandleFunc("GET /share", a.share)
	mux.HandleFunc("POST /items/{id}/top", a.itemAction)
	mux.HandleFunc("POST /items/{id}/keep", a.itemAction)
	mux.HandleFunc("POST /items/{id}/drop", a.itemAction)
	mux.HandleFunc("POST /items/{id}/unread", a.itemAction)
	mux.HandleFunc("POST /items/{id}/note", a.itemAction)
	mux.HandleFunc("POST /items/{id}/scroll", a.itemAction)
	mux.HandleFunc("POST /items/{id}/refetch", a.itemAction)
	mux.HandleFunc("POST /items/{id}/article", a.itemAction)
	mux.HandleFunc("POST /items/{id}/site", a.itemAction)
	mux.HandleFunc("POST /items/{id}/read", a.markRead)
	mux.HandleFunc("GET /read/{id}", a.reader)

	mux.HandleFunc("GET /session", a.session)
	mux.HandleFunc("GET /session/done", a.sessionDone)
	mux.HandleFunc("GET /dust", a.dustPage)
	mux.HandleFunc("GET /shelf", a.shelfPage)
	mux.HandleFunc("GET /wire", a.wire)
	mux.HandleFunc("POST /wire/collect", a.collect)
	mux.HandleFunc("GET /surprise", a.surprise)

	mux.HandleFunc("GET /follow", a.followPage)
	mux.HandleFunc("POST /follow/find", a.followFind)
	mux.HandleFunc("POST /follow", a.follow)
	mux.HandleFunc("GET /sources/{id}", a.sourcePage)
	mux.HandleFunc("POST /sources/{id}/mode", a.sourceAction)
	mux.HandleFunc("POST /sources/{id}/check", a.sourceAction)
	mux.HandleFunc("POST /sources/{id}/unfollow", a.sourceAction)
	mux.HandleFunc("POST /posts/{id}/add", a.postAction)
	mux.HandleFunc("POST /posts/{id}/skip", a.postAction)
	mux.HandleFunc("POST /posts/{id}/unskip", a.postAction)
	return a.requireLogin(mux)
}
