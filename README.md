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

## Save from your browser

Open **+ Save → Save from any page with a bookmarklet** in Desk and drag **Save to Desk** to your bookmarks bar. On any page you want to read later, click it: the page goes on top of your stack and Desk shows it being fetched, with a link back to where you were. If you're signed out, Desk asks for your password and then finishes the save.

The bookmarklet holds the address you opened Desk at, so add it from the same address you'll use it with. With `DESK_PASSWORD` set it also carries a key made from the password, so a link from another site can't add to your stack without asking you first. After changing the password, drag a fresh one.

## Use it from your phone

The simplest private way is [Tailscale](https://tailscale.com):

1. Install Tailscale on the computer running Desk and on your phone, signed in to the same account.
2. On the phone, open `http://<computer-name>:8080`.

That's enough to read. To install Desk as an app and save links from other apps' share menus, the phone needs an HTTPS address, which browsers require for those features. Tailscale can give Desk one with a real certificate:

1. In the Tailscale admin console, under DNS, turn on MagicDNS and HTTPS Certificates.
2. On the computer running Desk, run:

```bash
tailscale serve --bg localhost:8080
```

It prints Desk's new address, `https://<computer-name>.<tailnet-name>.ts.net`, reachable only from your own devices. It keeps serving after restarts; `tailscale serve status` shows it and `tailscale serve reset` turns it off. Use this address on the phone from now on.

### Save links from Android

1. Open the `https://…ts.net` address in Chrome and sign in.
2. Chrome menu (⋮) → Add to Home screen → Install.
3. In any app, Share → Desk. The link goes on top of your stack and Desk shows "Saved to your stack".

Desk finds the link whether the app shares it as a link, inside a sentence, or as the title. If you aren't signed in, Desk asks for the password and then saves the link.

Chrome installs Desk from the menu, and the share target comes with the install.

### Save links from iPhone

iPhone doesn't let web apps appear in the share sheet, so a small Shortcut does it. Desk comes with one:

1. On the iPhone, open Desk, tap **Save**, then **Save from your phone or any page**. The page has an **On your iPhone** section.
2. Copy your Desk address there, then tap **Add the Save to Desk shortcut**. Shortcuts opens; paste the address when it asks.

Now in any app, Share → Save to Desk. It opens Desk in your default browser, which saves the link and shows "Saved to your stack". Sign in to Desk in that browser once.

The shortcut is `static/save-to-desk.shortcut`. To rebuild it yourself: a shortcut shown in the Share Sheet that receives URLs and Safari web pages, URL Encodes the Shortcut Input, and opens `<your Desk address>/share?url=` followed by the URL Encoded Text. Sign it with `shortcuts sign --mode anyone` on a Mac.

To put Desk on the home screen, open it in Safari and Share → Add to Home Screen.

### Read offline

While Desk is reachable, your phone quietly keeps a copy of every piece on the stack (the top 50), so they still open when the computer sleeps or Tailscale is off. Books kept on the phone get a small gilt dot on the spine. Other pages show a list of what you can read, and reading positions are sent once Desk is back.

This needs the HTTPS address from `tailscale serve` above: browsers only run the offline helper on `https://` or `localhost`, so the plain `http://<computer-name>:8080` address works online but keeps nothing. Signing out clears the copies.

Keep the computer awake so new posts are found on time. On a Mac: System Settings → Battery → Options → "Prevent automatic sleeping when the display is off" (while plugged in).

## Keyboard

On the desk: <kbd>J</kbd> <kbd>K</kbd> move through the stack, <kbd>R</kbd> reads, <kbd>/</kbd> saves a link, <kbd>1</kbd> <kbd>2</kbd> <kbd>3</kbd> pick 15, 30 or 60 minutes, <kbd>Esc</kbd> closes a book.

Anywhere: <kbd>⌘</kbd> <kbd>K</kbd> (<kbd>Ctrl</kbd> <kbd>K</kbd> on Windows and Linux) searches everything on the stack, the shelf and in dust, by title, site or saved text. Type `site:jvns.ca` to stay on one site; <kbd>↑</kbd> <kbd>↓</kbd> choose and <kbd>Enter</kbd> opens. On a phone, tap Search at the top.

## Backups

Everything is in `data/reading.db`. Copy that file somewhere safe. To copy it while the app is running:

```bash
sqlite3 data/reading.db ".backup backup.db"
```

## How it works

- **Saving a link** puts it on top of the stack and fetches the page in the background to pull out the readable text ([fetch.go](fetch.go)).
- **Following a blog** finds its RSS or Atom feed. If there isn't one, Desk watches the page for new links instead, which is less precise. Every hour it checks for posts it hasn't seen ([fetch.go](fetch.go), `pollFeeds`).
- **Pages** are rendered on the server from [templates/](templates/). [htmx](https://htmx.org) (`static/htmx.min.js`) lets buttons and forms update the page without a full reload. All the styling is in [static/app.css](static/app.css).
- **Sharing** from another app opens `/share`, which saves the first web address it finds. The app icons in `static/` are drawn from the mark in `static/icon.svg` by `go run tools/icons.go`.
- **Data** is one SQLite file ([db.go](db.go), [store.go](store.go)), with full-text search over everything you've read.

## License

[MIT](LICENSE)
