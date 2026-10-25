// Page changes keep your scroll position; moving to another page starts at the top.
htmx.config.scrollIntoViewOnBoost = false;
let lastPath = location.pathname;
document.addEventListener("htmx:pushedIntoHistory", () => {
  if (location.pathname !== lastPath) window.scrollTo(0, 0);
  lastPath = location.pathname;
});

// ---- reader: progress bar, minutes left, remembered place, text size ----

let saveTimer = null;

function readerState() {
  const article = document.getElementById("article");
  if (!article) return null;
  const top = article.offsetTop;
  const span = Math.max(1, article.offsetHeight - window.innerHeight);
  const f = Math.min(1, Math.max(0, (window.scrollY - top + 80) / span));
  return { article, f };
}

function saveScroll() {
  const s = readerState();
  if (!s) return;
  const body = new FormData();
  body.append("f", s.f.toFixed(3));
  navigator.sendBeacon(`/items/${s.article.dataset.id}/scroll`, body);
}

window.addEventListener("scroll", () => {
  const s = readerState();
  if (!s) return;
  const bar = document.getElementById("progress") || document.getElementById("seg-now");
  if (bar) bar.style.width = (s.f * 100).toFixed(1) + "%";
  document.getElementById("up-next")?.classList.toggle("show", s.f > 0.85);
  const left = document.getElementById("left");
  const total = +s.article.dataset.minutes;
  if (left && total) left.textContent = s.f > 0.97 ? "done" : `${Math.max(1, Math.ceil(total * (1 - s.f)))} min left`;
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveScroll, 1500);
}, { passive: true });

document.addEventListener("visibilitychange", () => { if (document.hidden) saveScroll(); });
document.addEventListener("htmx:beforeRequest", saveScroll);

function setSize(rem) {
  document.documentElement.style.setProperty("--reader-size", rem + "rem");
}

htmx.onLoad(() => {
  try { const v = localStorage.getItem("reader-size"); if (v) setSize(+v); } catch (e) {}
  const article = document.getElementById("article");
  if (!article) return;
  requestAnimationFrame(() => window.dispatchEvent(new Event("scroll")));
  const f = +article.dataset.scroll;
  if (f > 0.02 && f < 0.97) {
    requestAnimationFrame(() => window.scrollTo(0, article.offsetTop - 80 + f * (article.offsetHeight - window.innerHeight)));
  }
});

document.addEventListener("click", (e) => {
  const b = e.target.closest("[data-size]");
  if (!b) return;
  const now = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--reader-size")) || 1.22;
  const next = Math.min(1.6, Math.max(1, now + 0.08 * +b.dataset.size));
  setSize(next.toFixed(2));
  try { localStorage.setItem("reader-size", next.toFixed(2)); } catch (e) {}
});

// ---- desk keys: J/K move through the stack (opening each book), R reads, / saves, 1-3 pick a time, Esc closes ----

const timeKeys = { 1: 15, 2: 30, 3: 60 };

document.addEventListener("keydown", (e) => {
  if (e.metaKey || e.ctrlKey || e.altKey || e.target.closest("input, textarea, select, dialog")) return;
  const stack = document.getElementById("stack");
  if (!stack) return;
  const key = e.key.toLowerCase();
  const spines = [...stack.querySelectorAll("summary")];
  const at = spines.indexOf(document.activeElement);
  if (key === "r") {
    const book = document.activeElement?.closest(".book") || stack.querySelector(".book:not(.failed)");
    const read = book?.querySelector(".read-link");
    if (read) { e.preventDefault(); read.click(); }
  } else if (key === "j" || key === "k") {
    e.preventDefault();
    const next = spines[key === "j" ? Math.min(spines.length - 1, at + 1) : Math.max(0, at - 1)];
    if (!next) return;
    next.parentElement.open = true; // details[name] closes the others
    next.focus();
    next.scrollIntoView({ block: "nearest" });
  } else if (key === "/" || e.code === "Slash") {
    const menu = document.querySelector(".save-menu");
    if (menu) { e.preventDefault(); menu.open = true; }
  } else if (timeKeys[key]) {
    document.querySelector(`.timepick a[href="/?min=${timeKeys[key]}"]`)?.click();
  } else if (key === "escape") {
    const here = document.activeElement?.closest("details[open]");
    if (here) {
      here.open = false;
      here.querySelector("summary").focus();
    } else {
      stack.querySelectorAll("details[open]").forEach((d) => { d.open = false; });
    }
  }
});

// An undo notice is a one-off: once shown, take its marker out of the address bar so a refresh doesn't repeat it.
function dropNoticeMarker() {
  const url = new URL(location.href);
  let changed = false;
  for (const k of ["gone", "done", "skipped"]) {
    if (url.searchParams.has(k)) { url.searchParams.delete(k); changed = true; }
  }
  if (changed) history.replaceState(history.state, "", url.pathname + url.search);
}
htmx.onLoad(dropNoticeMarker);
document.addEventListener("htmx:pushedIntoHistory", dropNoticeMarker);

// Opening Save puts the cursor in the field; Esc closes it.
document.addEventListener("toggle", (e) => {
  if (e.target.matches?.(".save-menu") && e.target.open) e.target.querySelector("input[name=url]")?.focus();
}, true);
document.addEventListener("keydown", (e) => {
  const menu = document.querySelector(".save-menu[open]");
  if (e.key === "Escape" && menu && !e.target.closest("dialog")) { menu.open = false; menu.querySelector("summary").focus(); }
});

// ---- search everything: Cmd+K (Ctrl+K elsewhere) from any page ----

const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
let findTimer = null;
let findAbort = null;
let findReturn = null;
let findDirty = false; // typed, but the results haven't caught up yet

function finder() { return document.getElementById("finder"); }

htmx.onLoad(() => {
  if (!isMac) document.querySelectorAll(".find-key").forEach((k) => { k.textContent = "Ctrl K"; });
});

function findIn() {
  try { return sessionStorage.getItem("find-in") || ""; } catch (e) { return ""; }
}

function openFinder() {
  const dlg = finder();
  if (!dlg) return;
  const input = dlg.querySelector("#finder-q");
  if (!dlg.open) {
    findReturn = document.activeElement;
    setFilter(findIn(), false);
    dlg.showModal();
    runSearch();
  }
  input.focus();
  input.select();
}

function closeFinder() {
  const dlg = finder();
  if (!dlg?.open) return;
  findAbort?.abort();
  dlg.close();
  if (findReturn?.isConnected) findReturn.focus();
  findReturn = null;
}

function setFilter(value, search = true) {
  finder().querySelectorAll(".finder-filters button").forEach((b) => {
    b.setAttribute("aria-pressed", b.dataset.in === value ? "true" : "false");
  });
  try { sessionStorage.setItem("find-in", value); } catch (e) {}
  if (search) runSearch();
}

function runSearch() {
  const dlg = finder();
  const q = dlg.querySelector("#finder-q").value;
  const where = dlg.querySelector(".finder-filters [aria-pressed=true]")?.dataset.in || "";
  findAbort?.abort();
  findAbort = new AbortController();
  clearTimeout(findTimer);
  findDirty = false;
  return fetch(`/search?q=${encodeURIComponent(q)}&in=${where}`, { signal: findAbort.signal })
    .then((r) => (r.ok && !r.redirected ? r.text() : Promise.reject(r))) // redirected: signed out
    .then((html) => showResults(html, q.trim(), where))
    .catch((e) => { if (e.name !== "AbortError") showResults("", q.trim(), where, true); });
}

const placeName = { "": "your stack, shelf or dust", stack: "your stack", shelf: "your shelf", dust: "the dust" };

function showResults(html, q, where, failed) {
  const dlg = finder();
  const list = dlg.querySelector("#finder-list");
  list.innerHTML = html;
  htmx.process(list); // so opening a piece is a boosted page change like any other link
  const options = list.querySelectorAll("[role=option]");
  const hits = list.querySelectorAll("[role=option]:not(.finder-site)").length;
  const empty = dlg.querySelector("#finder-empty");
  empty.hidden = options.length > 0;
  dlg.querySelector("#finder-q").setAttribute("aria-expanded", options.length > 0);
  empty.textContent = failed ? "Search isn't working right now." : q ? `Nothing in ${placeName[where]} matches “${q}”.` : `Nothing in ${placeName[where]} yet.`;
  dlg.querySelector("#finder-heading").hidden = !!q || !hits;
  dlg.querySelector("#finder-status").textContent = failed ? empty.textContent
    : hits ? `${hits} ${hits === 1 ? "result" : "results"}${q ? "" : ", most recent first"}`
    : options.length ? `${options.length} matching ${options.length === 1 ? "site" : "sites"}` : empty.textContent;
  select(options[0]);
}

function select(option) {
  const dlg = finder();
  const input = dlg.querySelector("#finder-q");
  dlg.querySelectorAll("[role=option][aria-selected=true]").forEach((o) => o.setAttribute("aria-selected", "false"));
  if (!option) { input.removeAttribute("aria-activedescendant"); return; }
  option.setAttribute("aria-selected", "true");
  input.setAttribute("aria-activedescendant", option.id);
  option.scrollIntoView({ block: "nearest" });
}

function choose(option) {
  if (!option) return;
  const input = finder().querySelector("#finder-q");
  if (option.dataset.site) {
    // Swap the half-typed "site:…" for the chosen site and keep searching.
    input.value = input.value.replace(/\S*$/, `site:${option.dataset.site} `);
    input.focus();
    runSearch();
    return;
  }
  const link = option.querySelector("a");
  if (link.target !== "_blank") closeFinder();
  link.click();
}

document.addEventListener("keydown", (e) => {
  if (e.key.toLowerCase() === "k" && (isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && !e.altKey && !e.shiftKey) {
    if (!finder()) return;
    e.preventDefault();
    openFinder();
  }
});

document.addEventListener("input", (e) => {
  if (e.target.id !== "finder-q") return;
  clearTimeout(findTimer);
  findDirty = true;
  findTimer = setTimeout(runSearch, 160);
});

document.addEventListener("keydown", (e) => {
  if (e.target.id !== "finder-q") return;
  const options = [...finder().querySelectorAll("[role=option]")];
  const at = options.findIndex((o) => o.getAttribute("aria-selected") === "true");
  if (e.key === "ArrowDown" || e.key === "ArrowUp") {
    e.preventDefault();
    if (!options.length) return;
    const next = e.key === "ArrowDown" ? (at + 1) % options.length : (at - 1 + options.length) % options.length;
    select(options[next]);
  } else if (e.key === "Escape") {
    e.preventDefault(); // a search field would only clear itself on the first press
    closeFinder();
  } else if (e.key === "Enter") {
    e.preventDefault();
    if (findDirty) runSearch().then(() => choose(finder().querySelector("[role=option][aria-selected=true]")));
    else choose(options[at]);
  }
});

document.addEventListener("click", (e) => {
  if (e.target.closest(".find-open")) { openFinder(); return; }
  const dlg = finder();
  if (!dlg?.open) return;
  if (e.target === dlg || e.target.closest(".finder-close")) { closeFinder(); return; } // the backdrop or Close
  const chip = e.target.closest(".finder-filters button");
  if (chip) { setFilter(chip.dataset.in); dlg.querySelector("#finder-q").focus(); return; }
  const option = e.target.closest("[role=option]");
  if (!option) return;
  if (option.dataset.site) choose(option);
  else if (option.querySelector("a").target !== "_blank") closeFinder();
});

// Esc with focus on a filter would close the dialog natively; go through closeFinder all the same.
document.addEventListener("cancel", (e) => {
  if (e.target.id === "finder") { e.preventDefault(); closeFinder(); }
}, true);
