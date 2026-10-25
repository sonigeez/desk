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
  if (e.metaKey || e.ctrlKey || e.altKey || e.target.closest("input, textarea, select")) return;
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
  if (e.key === "Escape" && menu) { menu.open = false; menu.querySelector("summary").focus(); }
});
