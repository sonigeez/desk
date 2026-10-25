// ---- reader highlights: select text to mark it; click (or Enter on) a mark to edit its note or remove it ----
// A highlight is stored as character offsets into the article's text plus the quote and a little text either
// side, so it can be found again if the saved text ever changes. One that can't be found stays in the list.

let prose = null, hls = [], pending = null, editing = null, barTimer = null, barPressed = false;

function textNodes() {
  const walk = document.createTreeWalker(prose, NodeFilter.SHOW_TEXT);
  const out = [];
  for (let n = walk.nextNode(); n; n = walk.nextNode()) out.push(n);
  return out;
}

// offsetOf is the position of a range boundary counted in characters of the article's text.
function offsetOf(node, off) {
  const r = document.createRange();
  r.selectNodeContents(prose);
  r.setEnd(node, off);
  return r.toString().length;
}

// Quotes are compared without whitespace: the quote keeps the line breaks you saw, the page text has none
// between paragraphs. squash also maps each kept character back to its place in the page text.
const squash = (s) => s.replace(/\s+/g, "");

function squashed(text) {
  let s = "";
  const at = [];
  for (let i = 0; i < text.length; i++) if (!/\s/.test(text[i])) { s += text[i]; at.push(i); }
  return { s, at };
}

// locate finds where a highlight's quote is now: at its saved offsets if the text is unchanged,
// otherwise the occurrence whose surroundings match best. null if the quote is gone.
function locate(h, text, sq) {
  const q = squash(h.quote), pre = squash(h.prefix), suf = squash(h.suffix);
  if (!q) return null;
  if (squash(text.slice(h.start, h.end)) === q) return [h.start, h.end];
  let best = null, bestScore = -Infinity;
  for (let i = sq.s.indexOf(q); i !== -1; i = sq.s.indexOf(q, i + 1)) {
    const score = (sq.s.slice(0, i).endsWith(pre) ? 2 : 0) + (sq.s.startsWith(suf, i + q.length) ? 2 : 0)
      - Math.abs(sq.at[i] - h.start) / (text.length + 1);
    if (score > bestScore) { best = [sq.at[i], sq.at[i + q.length - 1] + 1]; bestScore = score; }
  }
  return best;
}

function wrap(h, start, end) {
  let pos = 0;
  const marks = [];
  for (const n of textNodes()) {
    const len = n.data.length, a = Math.max(0, start - pos), b = Math.min(len, end - pos);
    pos += len;
    if (a >= b || !n.data.slice(a, b).trim()) continue; // outside, or only the whitespace between blocks
    let t = n;
    if (b < len) t.splitText(b);
    if (a > 0) t = t.splitText(a);
    const m = document.createElement("mark");
    m.className = "hl";
    m.dataset.hid = h.id;
    t.replaceWith(m);
    m.append(t);
    marks.push(m);
  }
  if (!marks.length) return;
  marks[0].id = "h" + h.id;
  marks[0].tabIndex = 0;
  marks[0].setAttribute("role", "button");
  marks.at(-1).classList.add("hl-end");
  dress(h);
}

// dress shows on the marks whether the highlight has a note.
function dress(h) {
  const marks = prose.querySelectorAll(`mark.hl[data-hid="${h.id}"]`);
  marks.forEach((m) => m.classList.toggle("has-note", !!h.note));
  if (marks[0]) {
    marks[0].setAttribute("aria-label", h.note ? `Highlight with a note: ${h.note}` : "Highlight, add a note or remove it");
    marks.forEach((m) => { m.title = h.note || ""; });
  }
}

function unwrap(id) {
  prose.querySelectorAll(`mark.hl[data-hid="${id}"]`).forEach((m) => {
    const parent = m.parentNode;
    m.replaceWith(...m.childNodes);
    parent.normalize();
  });
}

function renderList() {
  const list = document.getElementById("hl-list");
  const ol = list.querySelector("ol");
  ol.replaceChildren();
  // In reading order; ones that couldn't be found go last.
  for (const h of [...hls].sort((x, y) => x.at - y.at)) {
    const li = document.createElement("li");
    const q = document.createElement("button");
    q.type = "button";
    q.className = "hl-jump";
    q.dataset.hid = h.id;
    q.textContent = h.quote;
    li.append(q);
    if (h.note) {
      const p = document.createElement("p");
      p.className = "hl-list-note";
      p.textContent = h.note;
      li.append(p);
    }
    if (h.lost) {
      const p = document.createElement("p");
      p.className = "meta";
      p.textContent = "Couldn't find this passage in the saved text any more.";
      li.append(p);
    }
    ol.append(li);
  }
  list.hidden = !hls.length;
}

function post(url, data) {
  return fetch(url, { method: "POST", body: new URLSearchParams(data) }).then((r) => {
    if (!r.ok) throw new Error(r.statusText);
    return r;
  });
}

// ---- selecting ----

const word = /[\p{L}\p{N}'’]/u;

function selectionInProse() {
  const sel = getSelection();
  if (!prose || !sel.rangeCount || sel.isCollapsed) return null;
  const r = sel.getRangeAt(0);
  if (!prose.contains(r.commonAncestorContainer)) return null;
  const text = prose.textContent;
  let start = offsetOf(r.startContainer, r.startOffset), end = offsetOf(r.endContainer, r.endOffset);
  while (start < end && /\s/.test(text[start])) start++;
  while (end > start && /\s/.test(text[end - 1])) end--;
  if (end - start < 2) return null;
  // Like a real highlighter, take whole words: widen a selection that starts or ends partway through one.
  let s0 = start, e0 = end;
  while (s0 > 0 && word.test(text[s0 - 1]) && word.test(text[s0])) s0--;
  while (e0 < text.length && word.test(text[e0]) && word.test(text[e0 - 1])) e0++;
  const quote = text.slice(s0, start) + sel.toString().trim() + text.slice(end, e0);
  return { start: s0, end: e0, text, quote, rect: r.getBoundingClientRect() };
}

function showBar() {
  const bar = document.getElementById("hl-bar");
  if (!bar) return;
  if (barPressed) return;
  pending = selectionInProse();
  if (!pending) { bar.hidden = true; return; }
  bar.hidden = false;
  const touch = matchMedia("(pointer: coarse)").matches;
  const r = pending.rect, w = bar.offsetWidth, h = bar.offsetHeight;
  // On a phone the system's own copy menu sits above the selection, so this goes below it.
  const top = touch || r.top < h + 70 ? r.bottom + 12 : r.top - h - 10;
  const left = Math.min(Math.max(8, r.left + r.width / 2 - w / 2), document.documentElement.clientWidth - w - 8);
  bar.style.top = top + window.scrollY + "px";
  bar.style.left = left + window.scrollX + "px";
}

document.addEventListener("selectionchange", () => {
  if (!prose) return;
  clearTimeout(barTimer);
  barTimer = setTimeout(showBar, 220);
});

async function create(withNote) {
  const p = pending || selectionInProse();
  document.getElementById("hl-bar").hidden = true;
  pending = null;
  barPressed = false;
  if (!p) return;
  const h = {
    quote: p.quote,
    prefix: p.text.slice(Math.max(0, p.start - 40), p.start),
    suffix: p.text.slice(p.end, p.end + 40),
    start: p.start, end: p.end, at: p.start, note: "",
  };
  const article = document.getElementById("article");
  try {
    const r = await post(`/items/${article.dataset.id}/highlights`, h);
    h.id = (await r.json()).id;
  } catch (e) {
    alert("Couldn't save that highlight. Try again.");
    return;
  }
  getSelection().removeAllRanges();
  hls.push(h);
  wrap(h, h.start, h.end);
  renderList();
  if (withNote) openEditor(h);
}

// ---- editing ----

function openEditor(h) {
  if (!h) return;
  const dlg = document.getElementById("hl-dialog");
  editing = h;
  document.getElementById("hl-quote").textContent = h.quote;
  document.getElementById("hl-note").value = h.note;
  dlg.showModal();
  document.getElementById("hl-note").focus();
}

async function closeEditor(how) {
  const h = editing;
  editing = null;
  if (!h) return;
  try {
    if (how === "save") {
      const note = document.getElementById("hl-note").value.trim();
      await post(`/highlights/${h.id}/note`, { note });
      h.note = note;
      dress(h);
    } else if (how === "remove") {
      await post(`/highlights/${h.id}/delete`, {});
      unwrap(h.id);
      hls = hls.filter((x) => x !== h);
    }
  } catch (e) {
    alert("Couldn't save that change. Try again.");
  }
  renderList();
  if (how !== "remove") document.getElementById("h" + h.id)?.focus();
}

function byID(id) { return hls.find((h) => String(h.id) === String(id)); }

function jumpTo(h) {
  const m = document.getElementById("h" + h.id);
  if (!m) return;
  m.scrollIntoView({ block: "center" });
  m.focus({ preventScroll: true });
  prose.querySelectorAll(`mark.hl[data-hid="${h.id}"]`).forEach((x) => {
    x.classList.remove("flash");
    void x.offsetWidth; // restart the animation
    x.classList.add("flash");
  });
}

document.addEventListener("click", (e) => {
  if (!prose) return;
  const act = e.target.closest("[data-hl]");
  if (act) { create(act.dataset.hl === "note"); return; }
  const jump = e.target.closest(".hl-jump");
  if (jump) {
    const h = byID(jump.dataset.hid);
    if (h && !h.lost) jumpTo(h); else if (h) openEditor(h);
    return;
  }
  const mark = e.target.closest("mark.hl");
  // A link inside a highlight still works as a link, and a click that ends a new selection isn't a click on the mark.
  if (mark && !e.target.closest("a") && getSelection().isCollapsed) openEditor(byID(mark.dataset.hid));
});

// Keep the selection when a toolbar button is pressed with a mouse. A tap can still clear it on a
// phone, so while the bar is pressed the selection it was shown for is kept.
document.addEventListener("mousedown", (e) => { if (e.target.closest("#hl-bar")) e.preventDefault(); });
document.addEventListener("pointerdown", (e) => { barPressed = !!e.target.closest("#hl-bar"); });

document.addEventListener("keydown", (e) => {
  if (!prose || e.metaKey || e.ctrlKey || e.altKey || e.target.closest("input, textarea, select, dialog")) return;
  const key = e.key.toLowerCase();
  if ((key === "h" || key === "n") && selectionInProse()) {
    e.preventDefault();
    create(key === "n");
  } else if ((e.key === "Enter" || e.key === " ") && e.target.matches?.("mark.hl")) {
    e.preventDefault();
    openEditor(byID(e.target.dataset.hid));
  }
});

// The dialog's buttons say what to do with the highlight; Esc and Close just close it.
document.addEventListener("submit", (e) => {
  const dlg = e.target.closest("#hl-dialog");
  if (!dlg) return;
  e.preventDefault();
  dlg.close();
  closeEditor(e.submitter?.value || "");
});

// ---- drawing the saved highlights when a piece opens ----

htmx.onLoad(() => {
  const el = document.getElementById("prose");
  if (!el) { prose = null; return; }
  if (el === prose) return;
  prose = el;
  try { hls = JSON.parse(document.getElementById("hl-data").textContent) || []; } catch (e) { hls = []; }
  const text = prose.textContent, sq = squashed(text);
  // Wrapping doesn't change the text, so every highlight can be placed against the same string.
  for (const h of hls) {
    const at = locate(h, text, sq);
    h.lost = !at;
    h.at = at ? at[0] : Infinity;
    if (at) wrap(h, ...at);
  }
  renderList();
  const m = location.hash.match(/^#h(\d+)$/);
  const target = m && byID(m[1]);
  // After the reader has put you back at your last place, a link to a highlight takes you to it instead.
  if (target && !target.lost) setTimeout(() => jumpTo(target), 60);
});
