# Desk

A self-hosted reading desk. Save links to a stack, follow the blogs you care about and see when they publish, read in a clean reader, and keep what you've finished on a shelf.

One Go program and one SQLite file. No accounts, no cloud, nothing to subscribe to.

## What it does

- **The Stack.** Everything you save lies on your desk as a book. Thicker books are longer reads, the top one is next, and half-read books show a gilt line for how far you got. Pick "Only have 15 · 30 · 60 min" and the pieces that fit get a ribbon; start a session and read them one after another.
- **The Wire.** Follow a blog by pasting its address. Desk finds its feed and checks it every hour. New posts arrive as postmarked letters you add to the stack or skip, and every blog you follow has a pigeonhole showing how often it posts.
- **The reader.** Saved text in one calm column, so pieces survive the original site going away. Remembers your place.
- **Dust review.** Anything unread for 60 days asks to be kept or let go.
- **The Shelf.** Finished pieces stand upright, grouped by week, with six months of reading drawn as a row of spines, one per day. Everything you've read is searchable.

## Run it

Pick whichever suits you. All three run the same program.

### With Docker

```bash
docker run -d --name desk -p 8080:8080 -e DESK_PASSWORD=change-me -v ./data:/data --restart unless-stopped ghcr.io/sonigeez/desk:latest
```

Or with Docker Compose: download [compose.yaml](compose.yaml), set your password in it, then run `docker compose up -d`. Your data is in `./data/reading.db`.

### Download a binary

Grab the file for your system from [Releases](https://github.com/sonigeez/desk/releases), unpack it, and run `./desk` (on Windows, `desk.exe`).

### From source

You need [Go](https://go.dev/dl/) 1.26 or newer.

```bash
git clone https://github.com/sonigeez/desk.git
```

```bash
cd desk && go build -o desk . && ./desk
```

Open http://localhost:8080. Your data lives in `data/reading.db`.

## Settings

All optional, set as environment variables:

| Variable | Default | What it does |
|---|---|---|
| `DESK_PASSWORD` | none | Password for the sign-in page. Without it, anyone who can reach the port can use the app. |
| `DESK_ADDR` | `:8080` | Address and port to listen on. |
| `DESK_DB` | `data/reading.db` | Where the database file lives. |
| `DESK_CHECK_EVERY` | `1h` | How often to check followed blogs for new posts, e.g. `30m`, `2h`. |

## Start it automatically on a Mac

1. Build it: `go build -o desk .`
2. In `launchd/desk.plist`, replace `/path/to/desk` with the folder you cloned into, and `CHANGE-ME` with a password.
3. Install and start it:

```bash
mkdir -p data && cp launchd/desk.plist ~/Library/LaunchAgents/local.desk.plist && launchctl load ~/Library/LaunchAgents/local.desk.plist
```

It now starts when you log in and restarts if it crashes. Logs go to `data/desk.log`. After changing the code, rebuild and restart it:

```bash
go build -o desk . && launchctl kickstart -k gui/$(id -u)/local.desk
```

On Linux, a systemd service running `./desk` from the project folder does the same job.

## Use it from your phone

The simplest private way is [Tailscale](https://tailscale.com):

1. Install Tailscale on the computer running Desk and on your phone, signed in to the same account.
2. On the phone, open `http://<computer-name>:8080`.
3. On iPhone, Share → Add to Home Screen makes it feel like an app.

Keep the computer awake so new posts are found on time. On a Mac: System Settings → Battery → Options → "Prevent automatic sleeping when the display is off" (while plugged in).

## Keyboard

On the desk: <kbd>J</kbd> <kbd>K</kbd> move through the stack, <kbd>R</kbd> reads, <kbd>/</kbd> saves a link, <kbd>1</kbd> <kbd>2</kbd> <kbd>3</kbd> pick 15, 30 or 60 minutes, <kbd>Esc</kbd> closes a book.

## Backups

Everything is in `data/reading.db`. Copy that file somewhere safe. To copy it while the app is running:

```bash
sqlite3 data/reading.db ".backup backup.db"
```

## How it works

- **Saving a link** puts it on top of the stack and fetches the page in the background to pull out the readable text ([fetch.go](fetch.go)).
- **Following a blog** finds its RSS or Atom feed. If there isn't one, Desk watches the page for new links instead, which is less precise. Every hour it checks for posts it hasn't seen ([fetch.go](fetch.go), `pollFeeds`).
- **Pages** are rendered on the server from [templates/](templates/). [htmx](https://htmx.org) (`static/htmx.min.js`) lets buttons and forms update the page without a full reload. All the styling is in [static/app.css](static/app.css).
- **Data** is one SQLite file ([db.go](db.go), [store.go](store.go)), with full-text search over everything you've read.

## License

[MIT](LICENSE)
