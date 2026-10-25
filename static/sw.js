// Desk's service worker: keeps a copy of the reader page for every piece on the stack so they open
// with no connection. Pages call it "sync" on every load; it asks /offline.json what is on the stack,
// fetches what changed and drops what left. If /offline.json doesn't answer as JSON (signed out),
// it keeps nothing.

const CACHE = "desk-offline";
const META = "/__offline/meta"; // {at, pieces: [piece]}: when the copy was made and what's in it, top of the stack first
const PENDING = "/__offline/scroll/"; // reading positions saved while offline, sent on the next sync
const SHELL = ["/offline", "/static/app.css", "/static/app.js", "/static/htmx.min.js", "/static/highlights.js", "/static/icon.svg"];
const WAIT = 5000; // how long to wait for Desk before using the copy
const FONTS = ["fonts.googleapis.com", "fonts.gstatic.com"];

let generation = 0; // bumped by "clear", so a sync already running stops writing
let syncing = null;

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

self.addEventListener("message", (e) => {
  if (e.data === "sync") e.waitUntil(sync());
  if (e.data === "clear") e.waitUntil(clear());
});

async function clear() {
  generation++;
  await caches.delete(CACHE);
}

async function meta(cache) {
  const res = await cache.match(META);
  return res ? res.json() : null;
}

function sync() {
  syncing ??= refresh().catch(() => {}).finally(() => { syncing = null; });
  return syncing;
}

async function refresh() {
  const gen = generation;
  const res = await fetch("/offline.json", { cache: "no-store" });
  if (!res.ok || res.redirected || !(res.headers.get("Content-Type") || "").startsWith("application/json")) {
    // Signed out: forget everything.
    if (res.redirected || res.status === 401 || res.status === 403) await clear();
    return;
  }
  const list = await res.json();
  if (gen !== generation) return;
  const cache = await caches.open(CACHE);
  await sendPending(cache);

  // Only what's missing: asset() keeps the copies fresh whenever they're loaded online.
  for (const url of SHELL) {
    if (await cache.match(url)) continue;
    const r = await fetch(url, { cache: "no-cache" });
    if (gen !== generation) return;
    if (r.ok && !r.redirected) await cache.put(url, r);
  }

  const before = Object.fromEntries(((await meta(cache))?.pieces || []).map((p) => [p.id, p]));
  const pieces = [];
  for (const p of list) {
    const url = `/read/${p.id}`;
    const old = before[p.id];
    if (old && JSON.stringify(old) === JSON.stringify(p) && (await cache.match(url))) {
      pieces.push(p);
      continue;
    }
    const r = await fetch(url, { headers: { "X-Desk-Offline": "1" } });
    if (gen !== generation) return;
    if (r.ok && !r.redirected) {
      await cache.put(url, r);
      pieces.push(p);
    }
  }
  for (const req of await cache.keys()) {
    const m = new URL(req.url).pathname.match(/^\/read\/(\d+)$/);
    if (m && !pieces.some((p) => p.id === +m[1])) await cache.delete(req);
  }
  if (gen !== generation) return;
  await cache.put(META, new Response(JSON.stringify({ at: Date.now(), pieces }), { headers: { "Content-Type": "application/json" } }));
  for (const c of await self.clients.matchAll()) c.postMessage("offline-synced");
}

async function sendPending(cache) {
  for (const req of await cache.keys()) {
    const path = new URL(req.url).pathname;
    if (!path.startsWith(PENDING)) continue;
    const body = new FormData();
    body.append("f", await (await cache.match(req)).text());
    const r = await fetch(`/items/${path.slice(PENDING.length)}/scroll`, { method: "POST", body });
    if (r.ok) await cache.delete(req);
  }
}

// ---- answering requests ----

self.addEventListener("fetch", (e) => {
  const req = e.request;
  const url = new URL(req.url);
  if (FONTS.includes(url.hostname)) return e.respondWith(font(req));
  if (url.origin !== location.origin) return;

  if (req.method === "POST" && /^\/items\/\d+\/scroll$/.test(url.pathname)) return e.respondWith(scroll(req, url));
  if (req.method !== "GET") return; // Mark read, Save, Let go…: the page says so if Desk can't be reached

  // htmx fetches boosted links and history it can't restore with XHR, not as navigations.
  const page = req.mode === "navigate" || req.headers.get("HX-Boosted") || req.headers.get("HX-History-Restore-Request");
  if (page) return e.respondWith(pageFor(req, url));
  if (url.pathname.startsWith("/static/")) return e.respondWith(asset(req, url));
});

// race waits for Desk, but not forever: a sleeping Mac never says no, it just doesn't answer.
function race(req, wait = WAIT) {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error("timeout")), wait);
    fetch(req).then(resolve, reject).finally(() => clearTimeout(t));
  });
}

async function pageFor(req, url) {
  const cache = await caches.open(CACHE);
  const reader = /^\/read\/\d+$/.test(url.pathname);
  const copy = reader ? await cache.match(url.pathname) : null;
  try {
    // A slow page with no copy gets longer before it's taken for a sleeping Mac.
    const res = await race(req, copy ? WAIT : 3 * WAIT);
    // Opening a kept piece while online refreshes its copy (not a session view, which differs).
    if (copy && res.ok && !res.redirected && !url.search) cache.put(url.pathname, res.clone());
    return res;
  } catch (err) {
    const fallback = copy || (await cache.match("/offline"));
    if (!fallback) throw err;
    // A fresh Response keeps the address the reader asked for, so htmx doesn't push /offline.
    return new Response(fallback.body, { status: 200, headers: fallback.headers });
  }
}

// Static files come from Desk when it answers, so a deploy's new CSS and JS arrive at once,
// and from the copy when it doesn't.
async function asset(req, url) {
  const cache = await caches.open(CACHE);
  try {
    const res = await race(req);
    if (res.ok && (await cache.match(url.pathname))) cache.put(url.pathname, res.clone());
    return res;
  } catch (err) {
    const copy = await cache.match(url.pathname);
    if (!copy) throw err;
    return copy;
  }
}

// Fonts never change at a given address, so a copy is good forever. Only kept while signed in.
async function font(req) {
  const cache = await caches.open(CACHE);
  const copy = await cache.match(req);
  if (copy) return copy;
  const res = await fetch(req);
  if (res.ok && (await cache.match(META))) cache.put(req, res.clone());
  return res;
}

// Reading position: if Desk can't be reached, hold on to it and send it with the next sync.
async function scroll(req, url) {
  const f = (await req.clone().formData()).get("f");
  try {
    return await race(req);
  } catch (err) {
    const cache = await caches.open(CACHE);
    if (!(await cache.match(META))) return new Response(null, { status: 204 });
    const id = url.pathname.split("/")[2];
    await cache.put(PENDING + id, new Response(f));
    // Reopening the piece offline should start where you left off, not where the copy was made.
    const copy = await cache.match(`/read/${id}`);
    if (copy) {
      const html = (await copy.text()).replace(/data-scroll="[^"]*"/, `data-scroll="${+f}"`);
      await cache.put(`/read/${id}`, new Response(html, { headers: copy.headers }));
    }
    return new Response(null, { status: 204 });
  }
}
