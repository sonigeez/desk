# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

The primary user is the person who built it: a developer who saves far more than they read, and who follows a handful of writers whose new posts they don't want to miss. They read technical blogs (with code), long essays, whole sites or series worked through over weeks, and short news posts.

Most reading happens sitting at the laptop. The phone is for saving links and quick checks, reached over Tailscale.

It is built for this one person first, but may be open-sourced for other people to self-host, so nothing should depend on the author's personal setup.

## Product Purpose

Desk is a self-hosted read-later app and blog follower in one. It replaces a pile of open tabs, a bookmarks folder and an RSS reader. Success means the user reads what they saved, notices when the writers they care about publish, and can let go of things they will never read.

## Positioning

Most read-later apps are an inbox that only grows. Desk works like a desk:

- **The Wire**: blogs you follow report in when they publish, with each blog's posting rhythm visible at a glance. It answers "did they publish anything?" without visiting each site.
- **The Stack**: saved pieces lie flat like books. A thicker book is a longer read, and the top one is next.
- **Time sessions**: a quiet "Only have 15 · 30 · 60 min" line above the stack; the pieces that fit get a ribbon and can be read one after another.
- **Dust review**: anything unread for 60 days asks to be kept or let go.
- **The Shelf**: finished pieces stand upright, grouped by week, and stay searchable.

## Operating Context

- One user, one Go program and one SQLite file, running all day on the user's MacBook Air (lid open, kept awake).
- Opened at `localhost:8080` on the Mac, and on the phone through Tailscale.
- Followed blogs are checked every hour. New posts either wait on the Wire ("Tell me") or go straight onto the stack ("Add everything"), set per blog.
- Saving a link fetches the page in the background and keeps its readable text, so pieces survive the original site going away.

## Capabilities and Constraints

- Website only. No browser extension and no native app.
- Server-rendered Go templates with htmx; very little client-side JavaScript.
- Single-user password login when `DESK_PASSWORD` is set.
- Follows RSS and Atom feeds. A blog without a feed is watched by comparing the links on its page, which is less precise.
- A "whole site" item has no reading time; it carries a free-text progress note instead.
- Fonts load from Google Fonts. Offline, the pages fall back to system fonts.
- Undecided: highlights and notes, import from other read-later services, a reading log beyond the weekly tally.

## Brand Commitments

- The name is **Desk**, and the vocabulary is the Wire, the Stack, the Shelf and gathering dust.
- The user asked for authenticity and taste, and for it not to look like "100 other tools".
- Directions the user has already rejected: a green-felt-and-marigold palette, and a serif display with small-caps spine titles ("not vibing with the font").

## Evidence on Hand

No testimonials, users or metrics. All content shown in the app is the user's own reading. Demos and screenshots must use real feeds or clearly marked sample data, never invented quotes or usage numbers.

## Product Principles

1. Reading over managing. Every screen should move you toward reading something or deciding not to.
2. Show size and time honestly. Reading time and posting rhythm should be visible, not hidden in metadata.
3. Calm, not urgent. New posts are information, not notifications to clear. Nothing nags.
4. Letting go is a feature. The app helps you drop things as readily as it helps you keep them.
5. Portable and self-contained. One program, one file, no accounts, nothing that assumes the author's machine.
