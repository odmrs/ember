"use strict";

const LANGS = { en: "en", pt: "pt-BR" };
let messages = {};

function t(key) {
  return messages[key] ?? key;
}

class APIError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

async function api(path, body) {
  const init = body === undefined ? {} : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  };
  const res = await fetch(path, init);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new APIError(data.error ?? "internal", data.message ?? `${path}: ${res.status}`);
  return data;
}

function savedLang() {
  try {
    const v = localStorage.getItem("ember.lang");
    if (v in LANGS) return v;
  } catch {}
  return navigator.language?.toLowerCase().startsWith("pt") ? "pt" : "en";
}

async function setLang(lang) {
  messages = await api(`i18n/${lang}.json`);
  try { localStorage.setItem("ember.lang", lang); } catch {}
  document.documentElement.lang = LANGS[lang];
  for (const el of document.querySelectorAll("[data-i18n]")) el.textContent = t(el.dataset.i18n);
  for (const b of document.querySelectorAll("[data-lang]")) b.setAttribute("aria-pressed", b.dataset.lang === lang);
  render();
}

const status = { version: "", state: "unpaired", device: null };
const pairing = { view: "start", candidates: [], error: null, locked: false };
// conn mirrors the Connector, pushed by the server over /api/events.
const conn = { online: false, phase: "idle", attempt: 0, address: "", failure: "", scanning: false };
const STATE_LABEL = { unpaired: "state.unpaired", searching: "state.searching", linked: "state.linked", offline: "state.offline" };
const FAILURES = {
  unreachable: "conn.unreachable",
  port_closed: "conn.portClosed",
  host_key_mismatch: "conn.hostKeyMismatch",
  auth_rejected: "conn.authRejected",
};
const ERRORS = {
  host_key_mismatch: "err.hostKeyMismatch",
  key_not_accepted: "err.keyNotAccepted",
  verify_failed: "err.verifyFailed",
  unknown_device: "err.unknownDevice",
  bad_address: "err.badAddress",
  already_paired: "err.alreadyPaired",
  unreachable: "err.unreachable",
  not_paired: "err.notPaired",
  not_linked: "err.notLinked",
  outside_library: "err.outsideLibrary",
  not_found: "err.notFound",
  not_folder: "err.notFolder",
};

const $ = (id) => document.getElementById(id);

function el(tag, attrs, ...children) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs ?? {})) {
    if (k === "class") e.className = v;
    else if (k.startsWith("data-")) e.dataset[k.slice(5)] = v;
    else e.setAttribute(k, v);
  }
  e.append(...children);
  return e;
}

function bytes(n) {
  if (!n) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < units.length - 1) { n /= 1000; i++; }
  return `${n.toFixed(i && n < 10 ? 1 : 0)} ${units[i]}`;
}

function pairingActive() {
  return status.state === "unpaired" || pairing.view === "lock";
}

function linkState() {
  if (pairing.view === "scanning") return "searching";
  if (status.state === "unpaired") return "unpaired";
  if (!conn.online) return "offline";
  return conn.phase === "linked" ? "linked" : "searching";
}

function render() {
  const state = linkState();
  $("link").dataset.state = state;
  let label = t(STATE_LABEL[state]);
  if (state === "searching" && pairing.view !== "scanning" && conn.attempt) label += ` #${conn.attempt}`;
  $("link-text").textContent = label;
  if (status.version) $("version-line").textContent = `ember ${status.version} · ${location.host}`;

  const active = pairingActive();
  const connecting = !active && conn.phase !== "linked";
  $("pair").hidden = !active;
  $("hero").hidden = active;
  $("connect").hidden = !connecting;
  $("library").hidden = active || connecting;
  if (active) renderPairing();
  if (connecting) renderConnecting();

  const d = status.device;
  if (d) {
    const address = conn.phase === "linked" ? conn.address : `${d.address}:${d.port}`;
    $("device-line").textContent = `${d.user}@${address} · ${d.fingerprint}`;
    renderHero(d);
  }
  if (!active && !connecting) renderLibrary();
}

function renderConnecting() {
  const d = status.device;
  const address = conn.address || (d ? `${d.address}:${d.port}` : "—");
  $("conn-attempt").textContent = `#${conn.attempt}`;
  $("conn-line").textContent = `${address} · ${t("conn.retry")}`;
  $("conn-scan").hidden = !conn.scanning;

  const fail = $("conn-fail");
  fail.hidden = !conn.failure;
  fail.dataset.kind = conn.failure;
  $("conn-warning").hidden = conn.failure !== "host_key_mismatch";
  let key = FAILURES[conn.failure] ?? "conn.unreachable";
  if (conn.failure === "auth_rejected" && d?.passwordless) key = "conn.authRejectedPasswordless";
  $("conn-fail-text").textContent = t(key);
}

function renderPairing() {
  for (const pane of document.querySelectorAll("#pair [data-view]")) pane.hidden = pane.dataset.view !== pairing.view;
  const stages = { start: 1, scanning: 1, found: 2, installing: 3, lock: 4 };
  $("pair-stage").textContent = `${t("pair.stage")} ${stages[pairing.view]}/4`;
  $("pair-extra").hidden = !["start", "found"].includes(pairing.view);
  $("manual-address").placeholder = t("pair.manualPlaceholder");

  const err = $("pair-error");
  err.hidden = !pairing.error;
  if (pairing.error) err.textContent = t(ERRORS[pairing.error.code] ?? "err.internal") + "\n" + pairing.error.message;

  if (pairing.view === "found") renderCandidates();

  const lock = $("lock");
  lock.dataset.locked = pairing.locked;
  $("lock-text").textContent = t(pairing.locked ? "pair.locked" : "pair.waitingLock");
  $("finish").hidden = !pairing.locked;
}

function row(label, ...value) {
  return el("div", { class: "kv" }, el("span", {}, label), el("span", {}, ...value));
}

function renderCandidates() {
  const list = $("candidates");
  list.replaceChildren();
  $("found-intro").textContent = t(pairing.candidates.length ? "pair.foundIntro" : "pair.noneFound");
  for (const c of pairing.candidates) {
    const item = el("li", { class: "candidate" },
      el("div", { class: "addr" }, c.address, " ", el("span", { class: "dim" }, c.banner)),
      row(t("pair.fingerprint"), el("span", { class: "fp" }, c.fingerprint || "—")),
    );
    const s = c.survey;
    if (s) {
      item.append(
        row(t("pair.koreader"), s.koreaderVersion || "—"),
        row(t("pair.library"), "/mnt/us/documents ", el("span", { class: s.libraryExists ? "ok" : "bad" }, t(s.libraryExists ? "pair.exists" : "pair.missing"))),
        row(t("pair.free"), `${bytes(s.freeBytes)} / ${bytes(s.totalBytes)}`),
        row(t("pair.metadata"), s.metadataMode || "doc"),
        row(t("pair.openBook"), s.lastFile ? s.lastFile.split("/").pop() : "—"),
      );
    } else {
      item.append(el("p", { class: "dim" }, t("pair.noPasswordless")));
    }
    if (c.error) item.append(el("p", { class: "bad" }, c.error));
    if (c.hostKey) {
      item.append(el("div", { class: "actions" },
        el("button", { type: "button", class: "primary", "data-action": "confirm", "data-address": c.address, "data-fingerprint": c.fingerprint }, t("pair.confirm"))));
    }
    list.append(item);
  }
}

// lib is the Library as last read from the Device. Paths are Library paths:
// "" is the root, "Sci-Fi/Asimov" a folder in it.
const lib = {
  path: folderFromHash(),
  folder: null,
  stats: null,
  children: new Map(), // folder path -> its subfolders, filled as folders are read
  expanded: new Set([""]),
  selected: null,
  seq: 0,
};

// The open folder is kept in the URL so a reload returns to it.
function folderFromHash() {
  try {
    return decodeURIComponent(location.hash.slice(1));
  } catch {
    return "";
  }
}

const parentOf = (p) => p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "";
const baseName = (p) => p.slice(p.lastIndexOf("/") + 1);

function ancestors(p) {
  const out = [""];
  if (!p) return out;
  const parts = p.split("/");
  for (let i = 1; i <= parts.length; i++) out.push(parts.slice(0, i).join("/"));
  return out;
}

function readFolder(p) {
  return api(`api/library/folder?path=${encodeURIComponent(p)}`).then((f) => {
    lib.children.set(f.path, f.entries.filter((e) => e.kind === "folder"));
    return f;
  });
}

async function openFolder(p) {
  const seq = ++lib.seq;
  try {
    const f = await readFolder(p);
    if (seq !== lib.seq) return;
    Object.assign(lib, { path: f.path, folder: f, selected: null });
    history.replaceState(null, "", f.path ? `#${encodeURIComponent(f.path)}` : location.pathname);
    for (const a of ancestors(f.path)) lib.expanded.add(a);
    render();
    // After a reconnect deep in the tree, the levels above may be unknown.
    for (const a of ancestors(f.path)) {
      if (!lib.children.has(a)) await readFolder(a).then(render, () => {});
    }
  } catch (err) {
    if (seq !== lib.seq) return;
    log(`${t(ERRORS[err.code] ?? "err.internal")} ${p}`);
    if (p && (err.code === "not_found" || err.code === "not_folder" || err.code === "outside_library")) openFolder("");
  }
}

async function toggleFolder(p) {
  if (lib.expanded.has(p)) {
    lib.expanded.delete(p);
  } else {
    lib.expanded.add(p);
    if (!lib.children.has(p)) await readFolder(p).catch((err) => log(t(ERRORS[err.code] ?? "err.internal")));
  }
  render();
}

async function refreshLibrary() {
  lib.children.clear();
  api("api/library/stats").then((s) => { lib.stats = s; render(); }, (err) => log(t(ERRORS[err.code] ?? "err.internal")));
  await openFolder(lib.path);
}

function renderHero(d) {
  const root = lib.stats?.root ?? d.libraryRoot;
  $("path").textContent = lib.path ? `${root}/${lib.path}` : root;
  $("free").textContent = bytes(lib.stats ? lib.stats.freeBytes : d.survey.freeBytes);
  $("book-count").textContent = lib.folder?.books ?? 0;
  $("folder-count").textContent = lib.folder?.folders ?? 0;
}

function treeNode(p, name, depth, subfolders) {
  const open = lib.expanded.has(p);
  const node = el("div", { class: p === lib.path ? "node on" : "node" });
  node.style.setProperty("--depth", depth);
  const caret = subfolders > 0 || subfolders < 0
    ? el("button", { type: "button", class: "caret", "data-action": "toggle", "data-path": p, "aria-expanded": open }, open ? "▾" : "▸")
    : el("span", { class: "caret" });
  node.append(caret, el("button", { type: "button", class: "go", "data-action": "open", "data-path": p, title: name }, name));
  const out = [node];
  if (open) {
    for (const c of lib.children.get(p) ?? []) out.push(...treeNode(c.path, c.name, depth + 1, c.subfolders ?? 0));
  }
  return out;
}

function itemCount(e) {
  if (e.items === undefined) return t("folder.empty");
  if (e.items < 0) return "—";
  return `${e.items} ${t(e.items === 1 ? "folder.item" : "folder.items")}`;
}

function fileRow(e) {
  const name = el("td", { class: "name" });
  const tr = el("tr", { class: e.kind, "data-select": e.path }, name);
  if (e.path === lib.selected) tr.classList.add("sel");
  if (e.kind === "folder") {
    tr.dataset.folder = e.path;
    name.append(el("button", { type: "button", class: "go", "data-action": "open", "data-path": e.path }, `▸ ${e.name}`));
    tr.append(el("td", { class: "num dim" }, itemCount(e)));
    return tr;
  }
  name.append(e.name);
  if (e.format) name.append(el("span", { class: "ext" }, e.format));
  if (e.openNow) name.append(el("span", { class: "open-now" }, t("openNow")));
  if (e.kind === "other") tr.title = t("otherHint");
  tr.append(el("td", { class: "num" }, bytes(e.size)));
  return tr;
}

function renderLibrary() {
  const root = status.device?.libraryRoot ?? "";
  $("tree").replaceChildren(...treeNode("", `/ ${baseName(root)}`, 0, lib.children.get("")?.length ?? -1));
  $("files-where").textContent = ` · ${lib.path ? baseName(lib.path) : baseName(root)}`;

  const rows = [];
  if (lib.folder && lib.path) {
    const up = parentOf(lib.path);
    rows.push(el("tr", { class: "folder up", "data-folder": up },
      el("td", { class: "name" }, el("button", { type: "button", class: "go", "data-action": "open", "data-path": up }, "↑ ..")),
      el("td", {})));
  }
  for (const e of lib.folder?.entries ?? []) rows.push(fileRow(e));
  $("rows").replaceChildren(...rows);
  $("empty").hidden = !lib.folder || lib.folder.entries.length > 0;
}

function log(text) {
  const e = $("log");
  delete e.dataset.i18n;
  e.textContent = text;
}

function fail(err) {
  pairing.error = { code: err.code ?? "internal", message: err.message };
  log(err.message);
  render();
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function waitForScan() {
  for (;;) {
    const s = await api("api/pair");
    if (s.phase !== "scanning") {
      pairing.candidates = s.candidates;
      pairing.view = "found";
      render();
      return;
    }
    await sleep(700);
  }
}

async function search(address) {
  pairing.error = null;
  pairing.view = "scanning";
  render();
  try {
    await api("api/pair/scan", address ? { address } : {});
    await waitForScan();
  } catch (err) {
    pairing.view = "start";
    fail(err);
  }
}

async function confirm(address, fingerprint) {
  pairing.error = null;
  pairing.view = "installing";
  render();
  try {
    const res = await api("api/pair/confirm", { address, fingerprint });
    Object.assign(status, { state: "paired", device: res.device });
    pairing.view = "lock";
    log(t("log.paired"));
    render();
    watchLock();
  } catch (err) {
    pairing.view = "found";
    fail(err);
  }
}

// The user switches passwordless login off on the Kindle and restarts its SSH
// server; until then probes fail or find it still open, so keep trying.
async function watchLock() {
  while (pairing.view === "lock" && !pairing.locked) {
    try {
      const res = await api("api/pair/lock", {});
      pairing.locked = res.locked;
      pairing.error = null;
    } catch (err) {
      if (err.code === "host_key_mismatch") return fail(err);
    }
    render();
    if (!pairing.locked) await sleep(2000);
  }
}

async function showPublicKey() {
  if ($("pubkey").textContent) return;
  try {
    $("pubkey").textContent = (await api("api/pair/key", {})).publicKey;
  } catch (err) {
    fail(err);
  }
}

document.addEventListener("click", (e) => {
  const lang = e.target.closest("[data-lang]");
  if (lang) return setLang(lang.dataset.lang).catch((err) => log(err.message));

  const b = e.target.closest("[data-action]");
  if (!b) {
    const row = e.target.closest("tr[data-select]");
    if (row) {
      lib.selected = row.dataset.select;
      renderLibrary();
    }
    return;
  }
  switch (b.dataset.action) {
    case "open": return openFolder(b.dataset.path);
    case "toggle": return toggleFolder(b.dataset.path);
    case "search": return search("");
    case "confirm": return confirm(b.dataset.address, b.dataset.fingerprint);
    case "finish":
      pairing.view = "done";
      return render();
    case "copy":
      return navigator.clipboard.writeText($("pubkey").textContent)
        .then(() => log(t("log.copied")), (err) => log(err.message));
  }
});

$("rows").addEventListener("dblclick", (e) => {
  const row = e.target.closest("tr[data-folder]");
  if (row) openFolder(row.dataset.folder);
});

$("manual").addEventListener("submit", (e) => {
  e.preventDefault();
  const address = $("manual-address").value.trim();
  if (address) search(address);
});

$("usb").addEventListener("toggle", (e) => {
  if (e.target.open) showPublicKey();
});

// Keeping this stream open is what keeps the Connector running; the browser
// reconnects it on its own if ember restarts.
function listen() {
  const events = new EventSource("api/events");
  events.addEventListener("state", (e) => {
    const was = conn.phase;
    Object.assign(conn, { attempt: 0, address: "", failure: "", scanning: false }, JSON.parse(e.data), { online: true });
    if (was !== "linked" && conn.phase === "linked") {
      log(`${t("log.linked")} ${conn.address}`);
      refreshLibrary();
    }
    if (was === "linked" && conn.phase !== "linked") log(t("log.lost"));
    render();
  });
  events.addEventListener("error", () => {
    conn.online = false;
    render();
  });
}

async function start() {
  await setLang(savedLang());
  Object.assign(status, await api("api/status"));
  const s = await api("api/pair");
  if (s.phase === "lock") {
    pairing.view = "lock";
    watchLock();
  } else if (status.state === "paired") {
    pairing.view = "done";
  } else if (s.phase === "scanning") {
    pairing.view = "scanning";
    waitForScan().catch(fail);
  } else if (s.phase === "found") {
    pairing.view = "found";
    pairing.candidates = s.candidates;
  }
  render();
  listen();
}

start().catch((err) => log(err.message));
