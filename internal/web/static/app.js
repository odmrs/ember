"use strict";

const LANGS = { en: "en", pt: "pt-BR" };
let messages = {};
let lang = "en";

function t(key) {
  return messages[key] ?? key;
}

// fill puts values into a message's {placeholders}.
function fill(text, values) {
  return text.replace(/\{(\w+)\}/g, (m, k) => values[k] ?? m);
}

class APIError extends Error {
  constructor(code, message, data) {
    super(message);
    this.code = code;
    this.data = data ?? {};
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
  if (!res.ok) throw new APIError(data.error ?? "internal", data.message ?? `${path}: ${res.status}`, data);
  return data;
}

function savedLang() {
  try {
    const v = localStorage.getItem("ember.lang");
    if (v in LANGS) return v;
  } catch {}
  return navigator.language?.toLowerCase().startsWith("pt") ? "pt" : "en";
}

async function setLang(next) {
  messages = await api(`i18n/${next}.json`);
  lang = next;
  try { localStorage.setItem("ember.lang", lang); } catch {}
  document.documentElement.lang = LANGS[lang];
  for (const el of document.querySelectorAll("[data-i18n]")) el.textContent = t(el.dataset.i18n);
  for (const b of document.querySelectorAll("[data-lang]")) b.setAttribute("aria-pressed", b.dataset.lang === lang);
  render();
}

const status = { version: "", state: "unpaired", device: null };
const pairing = { view: "start", candidates: [], error: null, locked: false };
// conn mirrors the Connector, pushed by the server over /api/events.
const conn = { online: false, phase: "idle", attempt: 0, address: "", failure: "", scanning: false, keepAwake: "" };
const ui = { settings: false };
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
  collision: "err.collision",
  open_now_needs_confirm: "err.openNow",
  metadata_mode_unsupported: "err.metadataMode",
  into_itself: "err.intoItself",
  library_root: "err.libraryRoot",
  extension_change: "err.extensionChange",
  partial_failure: "err.partial",
  bad_request: "err.badRequest",
  no_space: "err.noSpace",
  incomplete: "err.incomplete",
  not_file: "err.notFile",
  root_outside: "err.rootOutside",
  bad_port: "err.badPort",
  bad_user: "err.badUser",
  confirmation_required: "err.confirmationRequired",
};

// errorText is an API error as the user reads it; a refused name says why.
function errorText(err) {
  if (err.code === "invalid_name") return t(`err.name.${err.data.reason}`);
  return t(ERRORS[err.code] ?? "err.internal");
}

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
  const paired = status.state === "paired";
  const settings = ui.settings && paired && !active;
  const connecting = !active && !settings && conn.phase !== "linked";
  $("settings-btn").disabled = !paired || active;
  $("settings-btn").setAttribute("aria-pressed", settings);
  $("pwd-banner").hidden = !status.device?.passwordless;
  $("pair").hidden = !active;
  $("settings").hidden = !settings;
  $("hero").hidden = active || settings;
  $("connect").hidden = !connecting;
  $("library").hidden = active || connecting || settings;
  if (active) renderPairing();
  if (connecting) renderConnecting();
  if (settings) renderSettings();

  const d = status.device;
  if (d) {
    const address = conn.phase === "linked" ? conn.address : `${d.address}:${d.port}`;
    $("device-line").textContent = `${d.user}@${address} · ${d.fingerprint}`;
    renderHero(d);
  } else {
    $("device-line").textContent = "—";
  }
  const awake = conn.phase === "linked" ? conn.keepAwake : "";
  $("awake-line").hidden = !awake;
  $("awake-line").dataset.state = awake;
  if (awake) $("awake-line").textContent = t(`awake.${awake}`);
  if (!active && !connecting && !settings) renderLibrary();
  if (uploads.items.length) renderQueue();
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
  sel: new Set(), // selected Library paths in the open folder
  anchor: null, // where a shift-click range starts
  edit: null, // {kind: "new"} or {kind: "rename", path}, with the typed value
  busy: false, // a change is on its way to the Device
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
    if (f.path !== lib.path) Object.assign(lib, { sel: new Set(), anchor: null, edit: null });
    Object.assign(lib, { path: f.path, folder: f });
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
  const mode = lib.stats?.metadataMode ?? "doc";
  const meta = $("meta");
  meta.hidden = mode === "doc";
  meta.textContent = `${t("meta.tag")} ${mode.toUpperCase()}`;
  meta.title = t(mode === "hash" ? "meta.hash" : "meta.dir");
  meta.classList.toggle("warn", mode !== "hash");
}

function treeNode(p, name, depth, subfolders) {
  const open = lib.expanded.has(p);
  const node = el("div", { class: p === lib.path ? "node on" : "node", "data-drop": p });
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

function nameInput(value) {
  return el("input", { class: "name-edit", id: "name-edit", value, autocomplete: "off", spellcheck: "false" });
}

function fileRow(e) {
  const name = el("td", { class: "name" });
  const tr = el("tr", { class: e.kind, "data-select": e.path, draggable: "true" }, name);
  if (lib.sel.has(e.path)) tr.classList.add("sel");
  const editing = lib.edit?.kind === "rename" && lib.edit.path === e.path;
  if (editing) {
    name.append(nameInput(lib.edit.value));
  } else if (e.kind === "folder") {
    name.append(el("button", { type: "button", class: "go", "data-action": "open", "data-path": e.path }, `▸ ${e.name}`));
  } else {
    name.append(e.name);
  }
  if (e.kind === "folder") {
    tr.dataset.folder = e.path;
    tr.dataset.drop = e.path;
    tr.append(el("td", { class: "num dim" }, itemCount(e)));
  } else {
    if (!editing && e.format) name.append(el("span", { class: "ext" }, e.format));
    if (e.openNow) name.append(el("span", { class: "open-now" }, t("openNow")));
    if (e.kind === "other") tr.title = t("otherHint");
    tr.append(el("td", { class: "num" }, bytes(e.size)));
  }
  tr.append(el("td", { class: "act" }, el("span", { class: "rowact" },
    el("button", { type: "button", "data-action": "rename", "data-path": e.path }, t("rename")),
    el("button", { type: "button", class: "bad", "data-action": "delete-one", "data-path": e.path }, t("del")))));
  return tr;
}

function renderLibrary() {
  const root = status.device?.libraryRoot ?? "";
  $("tree").replaceChildren(...treeNode("", `/ ${baseName(root)}`, 0, lib.children.get("")?.length ?? -1));
  $("files-where").textContent = ` · ${lib.path ? baseName(lib.path) : baseName(root)}`;

  const rows = [];
  if (lib.folder && lib.path) {
    const up = parentOf(lib.path);
    rows.push(el("tr", { class: "folder up", "data-folder": up, "data-drop": up },
      el("td", { class: "name" }, el("button", { type: "button", class: "go", "data-action": "open", "data-path": up }, "↑ ..")),
      el("td", {}), el("td", {})));
  }
  if (lib.edit?.kind === "new") {
    const input = nameInput(lib.edit.value);
    input.placeholder = t("newFolder.placeholder");
    rows.push(el("tr", { class: "folder new" }, el("td", { class: "name" }, "▸ ", input), el("td", {}), el("td", {})));
  }
  for (const e of lib.folder?.entries ?? []) rows.push(fileRow(e));
  $("rows").replaceChildren(...rows);
  $("empty").hidden = !lib.folder || lib.folder.entries.length > 0 || lib.edit?.kind === "new";
  $("tb-folder").disabled = !lib.folder || lib.busy;
  $("tb-upload").disabled = !lib.folder;
  markToolbar();

  const input = $("name-edit");
  if (input && !input.dataset.focused) {
    input.dataset.focused = "1";
    input.focus();
    input.setSelectionRange(lib.edit.selStart ?? 0, lib.edit.selEnd ?? input.value.length);
  }
}

// Selection follows file managers: click picks one, Cmd/Ctrl-click toggles,
// Shift-click takes the range from the last plain click.
function select(p, e) {
  if (e.shiftKey && lib.anchor) {
    const order = (lib.folder?.entries ?? []).map((x) => x.path);
    const [a, b] = [order.indexOf(lib.anchor), order.indexOf(p)].sort((x, y) => x - y);
    if (a >= 0) {
      lib.sel = new Set(order.slice(a, b + 1));
      return markSelection();
    }
  }
  if (e.metaKey || e.ctrlKey) {
    if (lib.sel.has(p)) lib.sel.delete(p);
    else lib.sel.add(p);
  } else {
    lib.sel = new Set([p]);
  }
  lib.anchor = p;
  markSelection();
}

// markSelection updates the rows in place: rebuilding them would drop a
// drag that is starting, and the second click of a double-click.
function markSelection() {
  for (const tr of $("rows").querySelectorAll("tr[data-select]")) tr.classList.toggle("sel", lib.sel.has(tr.dataset.select));
  markToolbar();
}

function markToolbar() {
  $("tb-delete").disabled = lib.sel.size === 0 || lib.busy;
  $("tb-get").disabled = !selectedFile();
}

// selectedFile is the one selected entry when it is a file, which GET takes.
function selectedFile() {
  if (lib.sel.size !== 1) return null;
  const e = entryAt([...lib.sel][0]);
  return e && e.kind !== "folder" ? e : null;
}

const entryAt = (p) => lib.folder?.entries.find((e) => e.path === p);

function startNewFolder() {
  if (!lib.folder || lib.busy) return;
  lib.edit = { kind: "new", value: "" };
  renderLibrary();
}

function startRename(p) {
  const e = entryAt(p);
  if (!e || lib.busy) return;
  // Select the name without its extension, as file managers do.
  const ext = e.kind === "book" && e.format ? e.format.length + 1 : 0;
  lib.edit = { kind: "rename", path: p, value: e.name, selStart: 0, selEnd: e.name.length - ext };
  lib.sel = new Set([p]);
  lib.anchor = p;
  renderLibrary();
}

function cancelEdit() {
  if (!lib.edit) return;
  lib.edit = null;
  renderLibrary();
}

async function commitEdit() {
  const edit = lib.edit;
  if (!edit || lib.busy) return;
  if (edit.kind === "new") {
    const res = await change("mkdir", { parent: lib.path, name: edit.value }, edit);
    if (res) log(fill(t("log.created"), { name: edit.value }));
    return;
  }
  const e = entryAt(edit.path);
  if (!e || edit.value === e.name) return cancelEdit();
  const res = await withOpenConfirm(e.openNow, (confirmOpen) =>
    change("rename", { path: edit.path, name: edit.value, confirmOpen }, edit));
  if (!res) return;
  lib.sel = new Set([lib.path ? `${lib.path}/${edit.value}` : edit.value]);
  log(fill(t("log.renamed"), { from: e.name, to: edit.value }) + warnings(res));
  renderLibrary();
}

// change sends one write and shows the folder as the Device now has it. On
// failure it logs why and resolves to null, keeping an edit open so the
// name can be fixed.
async function change(op, body, edit) {
  lib.busy = true;
  renderLibrary();
  try {
    const res = await api(`api/library/${op}`, { ...body, view: lib.path });
    if (lib.edit === edit) lib.edit = null;
    applyChange(res);
    return res;
  } catch (err) {
    if (err.code === "open_now_needs_confirm") throw err;
    log(`${errorText(err)} ${err.code === "invalid_name" ? "" : err.message}`.trim());
    if (err.code === "not_found") refreshAfterChange();
    return null;
  } finally {
    lib.busy = false;
    renderLibrary();
  }
}

function applyChange(res) {
  if (res.folder) {
    lib.folder = res.folder;
    lib.children.set(res.folder.path, res.folder.entries.filter((e) => e.kind === "folder"));
    const present = new Set(res.folder.entries.map((e) => e.path));
    lib.sel = new Set([...lib.sel].filter((p) => present.has(p)));
  }
  refreshAfterChange();
}

// Other folders in the tree may have gained or lost subfolders.
function refreshAfterChange() {
  if (!lib.folder) return openFolder("");
  for (const p of [...lib.expanded]) {
    if (p === lib.path) continue;
    readFolder(p).then(render, () => {
      lib.expanded.delete(p);
      lib.children.delete(p);
      render();
    });
  }
  api("api/library/stats").then((s) => { lib.stats = s; render(); }, () => {});
  render();
}

// withOpenConfirm runs a change that may touch the Book KOReader has open.
// It asks first when the listing already says so, and again if the Device
// says so.
async function withOpenConfirm(known, run, why = t("dlg.openMove")) {
  const confirmed = () => ask({ title: t("dlg.openTitle"), body: [why], ok: t("dlg.goAhead") });
  if (known && !(await confirmed())) return cancelEdit();
  try {
    return await run(known);
  } catch (err) {
    if (err.code !== "open_now_needs_confirm") throw err;
    if (!(await confirmed())) return cancelEdit();
    return run(true);
  }
}

function warnings(res) {
  return (res.warnings ?? []).map((w) => " · " + fill(t(`warn.${w.code}`), { path: baseName(w.path) })).join("");
}

function counted(n, name) {
  return n === 1 ? name : `${n} ${t("folder.items")}`;
}

async function move(paths, to) {
  if (lib.busy || !paths?.length) return;
  const open = paths.some((p) => entryAt(p)?.openNow || (entryAt(p)?.kind === "folder" && lib.stats?.openNow?.startsWith(p + "/")));
  const res = await withOpenConfirm(open, (confirmOpen) => change("move", { paths, to, confirmOpen }));
  if (!res) return;
  log(fill(t("log.moved"), { what: counted(paths.length, baseName(paths[0])), to: where(to) }) + warnings(res));
}

// remove asks first, showing what goes: Book and folder counts, the
// progress that dies with the Books, and the open Book if it is among them.
async function remove(paths) {
  if (lib.busy || !paths.length) return;
  let p;
  try {
    p = await api(`api/library/preview?${paths.map((x) => "path=" + encodeURIComponent(x)).join("&")}`);
  } catch (err) {
    return log(`${errorText(err)} ${err.message}`);
  }
  const what = counted(paths.length, baseName(paths[0]));
  const body = [fill(t("dlg.deleteBody"), { what })];
  const counts = el("div", { class: "counts" });
  for (const [key, n] of [["dlg.books", p.books], ["dlg.folders", p.folders], ["dlg.others", p.others]]) {
    if (n) counts.append(row(t(key), String(n)));
  }
  body.push(counts);
  if (p.books) body.push(t("dlg.progressLost"));
  body.push(t("dlg.cannotUndo"));
  const ok = await ask({
    title: fill(t("dlg.deleteTitle"), { what }),
    body,
    warn: p.openNow ? t("dlg.openDelete") : "",
    ok: t("delete"),
  });
  if (!ok) return;
  // KOReader may have opened one of them since the preview.
  const res = await withOpenConfirm(false, (confirmOpen) => change("delete", { paths, confirmOpen: p.openNow || confirmOpen }), t("dlg.openDelete"));
  if (res) log(fill(t("log.deleted"), { what }) + warnings(res));
}

// ask shows the confirm dialog and resolves to true when the user confirms.
function ask({ title, body, warn, ok, cancel = t("cancel"), okClass = "danger" }) {
  const d = $("dialog");
  $("dialog-title").textContent = title;
  const content = body.map((b) => typeof b === "string" ? el("p", {}, b) : b);
  if (warn) content.push(el("p", { class: "warn" }, warn));
  $("dialog-body").replaceChildren(...content);
  $("dialog-ok").textContent = ok;
  $("dialog-ok").className = okClass;
  d.querySelector("[value=cancel]").textContent = cancel;
  d.returnValue = "";
  d.showModal();
  d.querySelector("[value=cancel]").focus();
  return new Promise((resolve) => d.addEventListener("close", () => resolve(d.returnValue === "ok"), { once: true }));
}

function openSettings() {
  ui.settings = true;
  fillSettings();
  $("set-msg").textContent = "";
  render();
  $("set-root").focus();
}

function closeSettings() {
  ui.settings = false;
  render();
}

// fillSettings puts the saved Profile into the form. It runs when the screen
// opens and after a save, never on a plain render, so typing is not undone.
function fillSettings() {
  const d = status.device;
  if (!d) return;
  $("set-root").value = d.libraryRoot;
  $("set-port").value = d.port;
  $("set-user").value = d.user;
  $("set-awake").checked = d.keepAwake;
}

function renderSettings() {
  const on = !!status.device?.passwordless;
  $("pwd-state").textContent = t(on ? "pwd.on" : "pwd.off");
  $("pwd-state").dataset.on = on;
  $("pwd-toggle").textContent = t(on ? "pwd.turnOff" : "pwd.turnOn");
  $("set-lang").value = lang;
}

function settingsMessage(text, bad) {
  const m = $("set-msg");
  m.textContent = text;
  m.className = bad ? "set-msg bad" : "set-msg ok";
}

// The Library root is checked on the Kindle, so these errors read as answers
// about the folder typed, not about a Book.
const ROOT_ERRORS = { not_found: "set.err.rootMissing", not_folder: "set.err.rootNotFolder", not_linked: "set.err.rootNeedsLink" };

async function saveSettings() {
  const d = status.device;
  if (!d) return;
  const port = Number($("set-port").value);
  if (!Number.isInteger(port)) return settingsMessage(t("err.badPort"), true);
  const want = { libraryRoot: $("set-root").value.trim(), port, user: $("set-user").value.trim(), keepAwake: $("set-awake").checked };
  const body = {};
  for (const [k, v] of Object.entries(want)) if (v !== d[k]) body[k] = v;
  if (!Object.keys(body).length) return settingsMessage(t("set.unchanged"), false);

  $("set-save").disabled = true;
  try {
    const res = await api("api/settings", body);
    const rootChanged = res.device.libraryRoot !== d.libraryRoot;
    status.device = res.device;
    fillSettings();
    settingsMessage(t("set.saved"), false);
    log(t("log.settingsSaved"));
    if (rootChanged) {
      Object.assign(lib, { path: "", folder: null, stats: null, children: new Map(), expanded: new Set([""]), sel: new Set(), anchor: null, edit: null });
      history.replaceState(null, "", location.pathname);
      if (conn.phase === "linked") refreshLibrary();
    }
  } catch (err) {
    settingsMessage(t(ROOT_ERRORS[err.code] ?? ERRORS[err.code] ?? "err.internal"), true);
  } finally {
    $("set-save").disabled = false;
    render();
  }
}

// Turning passwordless mode on is two requests with the user's answer in
// between: the server hands out a one-time token, and only a confirm
// carrying it switches the mode on. Turning it off is one click.
async function togglePasswordless() {
  try {
    if (status.device?.passwordless) {
      status.device = (await api("api/settings/passwordless/off", {})).device;
      log(t("log.pwdOff"));
      return;
    }
    const { token } = await api("api/settings/passwordless/request", {});
    const ok = await ask({
      title: t("pwd.dlgTitle"),
      body: [t("pwd.dlgBody"), t("pwd.dlgKindle")],
      warn: t("pwd.dlgWarn"),
      ok: t("pwd.dlgOk"),
    });
    if (!ok) return log(t("log.pwdKept"));
    status.device = (await api("api/settings/passwordless/confirm", { token })).device;
    log(t("log.pwdOn"));
  } catch (err) {
    log(errorText(err));
  } finally {
    render();
  }
}

async function forget() {
  const ok = await ask({
    title: t("forget.dlgTitle"),
    body: [t("forget.dlgBody"), t("forget.dlgKey")],
    ok: t("forget.button"),
  });
  if (!ok) return;
  try {
    await api("api/settings/forget", { confirm: true });
  } catch (err) {
    return log(errorText(err));
  }
  Object.assign(status, { state: "unpaired", device: null });
  Object.assign(pairing, { view: "start", candidates: [], error: null, locked: false });
  Object.assign(lib, { path: "", folder: null, stats: null, children: new Map(), expanded: new Set([""]), sel: new Set(), anchor: null, edit: null });
  ui.settings = false;
  history.replaceState(null, "", location.pathname);
  // ember made a new key pair; the old one shown for USB is void.
  $("pubkey").textContent = "";
  log(t("log.forgotten"));
  render();
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
    if (row && !e.target.closest("input")) select(row.dataset.select, e);
    return;
  }
  switch (b.dataset.action) {
    case "open": return openFolder(b.dataset.path);
    case "toggle": return toggleFolder(b.dataset.path);
    case "new-folder": return startNewFolder();
    case "delete": return remove([...lib.sel]);
    case "delete-one": return remove([b.dataset.path]);
    case "rename": return startRename(b.dataset.path);
    case "upload": return $("upload-input").click();
    case "download": return download(selectedFile());
    case "cancel-upload": return cancelUpload(Number(b.dataset.id));
    case "clear-queue": return clearQueue();
    case "settings": return ui.settings ? closeSettings() : openSettings();
    case "settings-open": return openSettings();
    case "settings-close": return closeSettings();
    case "passwordless": return togglePasswordless();
    case "forget": return forget();
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

// Double-click opens a folder and renames anything else.
$("rows").addEventListener("dblclick", (e) => {
  if (e.target.closest("input, button")) return;
  const row = e.target.closest("tr[data-folder]");
  if (row) return openFolder(row.dataset.folder);
  const item = e.target.closest("tr[data-select]");
  if (item) startRename(item.dataset.select);
});

$("rows").addEventListener("input", (e) => {
  if (!lib.edit || e.target.id !== "name-edit") return;
  Object.assign(lib.edit, { value: e.target.value, selStart: e.target.selectionStart, selEnd: e.target.selectionEnd });
});

$("rows").addEventListener("keydown", (e) => {
  if (e.target.id !== "name-edit") return;
  if (e.key === "Enter") {
    e.preventDefault();
    commitEdit();
  } else if (e.key === "Escape") {
    cancelEdit();
  }
});

// Leaving the field drops the edit, unless a change is already on its way.
// Chrome also fires focusout while a render is replacing the field, so the
// check waits until the render is over and only counts a field still shown.
$("rows").addEventListener("focusout", (e) => {
  const input = e.target;
  if (input.id !== "name-edit") return;
  setTimeout(() => {
    if (!lib.busy && lib.edit && input.isConnected && document.activeElement !== input) cancelEdit();
  });
});

document.addEventListener("keydown", (e) => {
  if ($("library").hidden || $("dialog").open || e.target.closest("input, textarea")) return;
  // A Mac's delete key sends Backspace; Cmd+Backspace is Finder's delete.
  if (e.key === "Delete" || e.key === "Backspace") {
    e.preventDefault();
    if (lib.sel.size) remove([...lib.sel]);
    return;
  }
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key === "n" || e.key === "N") {
    e.preventDefault();
    startNewFolder();
  } else if (e.key === "F2") {
    e.preventDefault();
    if (lib.sel.size === 1) startRename([...lib.sel][0]);
  } else if (e.key === "Escape" && lib.sel.size) {
    lib.sel = new Set();
    markSelection();
  }
});

// where names a folder for the log and the dialogs.
function where(p) {
  return p ? baseName(p) : `/ ${baseName(status.device?.libraryRoot ?? "")}`;
}

// download hands the file to the browser's own download manager, which
// streams it to disk. The HEAD first turns a refusal into a log line instead
// of a saved error page.
async function download(e) {
  if (!e) return;
  const url = `api/library/download?path=${encodeURIComponent(e.path)}`;
  const res = await fetch(url, { method: "HEAD" }).catch(() => null);
  if (!res?.ok) return log(`${t(res ? "err.notFound" : "err.unreachable")} ${e.name}`);
  const a = el("a", { href: url, download: e.name });
  document.body.append(a);
  a.click();
  a.remove();
  log(fill(t("log.download"), { name: e.name }));
}

// Uploads run one at a time, in the order the files were picked. Each is
// checked just before it is sent, so a name taken or space used up by the
// previous one is seen, and a taken name is asked about when its turn comes.
const uploads = { items: [], running: false, seq: 0 };
const FINISHED = new Set(["done", "skipped", "failed", "cancelled"]);

function enqueue(files) {
  if (!lib.folder || !files?.length) return;
  for (const file of files) {
    uploads.items.push({ id: ++uploads.seq, file, name: file.name, size: file.size, folder: lib.path, state: "queued", sent: 0 });
  }
  renderQueue();
  runQueue();
}

async function runQueue() {
  if (uploads.running) return;
  uploads.running = true;
  try {
    for (let it; (it = uploads.items.find((x) => x.state === "queued"));) await uploadOne(it);
  } finally {
    uploads.running = false;
  }
}

async function uploadOne(it) {
  setItem(it, "checking");
  let a;
  try {
    a = await api(`api/library/upload/check?${new URLSearchParams({ folder: it.folder, name: it.name, size: it.size })}`);
  } catch (err) {
    return failItem(it, errorText(err));
  }
  if (it.state !== "checking") return;
  it.book = a.book;
  if (a.reason) return failItem(it, t(`err.name.${a.reason}`));
  if (!a.fits) return failItem(it, fill(t("up.noSpace"), { size: bytes(it.size), free: bytes(a.freeBytes) }));
  let replace = false;
  if (a.existing) {
    if (!a.replaceable) return failItem(it, fill(t("up.folderThere"), { name: a.existing }));
    replace = await ask({
      title: fill(t("dlg.replaceTitle"), { name: a.existing }),
      body: [fill(t("dlg.replaceBody"), { name: a.existing, folder: where(it.folder) }), t("dlg.replaceKeeps")],
      warn: a.openNow ? t("dlg.replaceOpen") : "",
      ok: t("dlg.replace"),
      cancel: t("dlg.skip"),
      okClass: "primary",
    });
    if (it.state !== "checking") return;
    if (!replace) {
      setItem(it, "skipped");
      return log(fill(t("log.skipped"), { name: it.name, to: where(it.folder) }));
    }
  }
  it.replace = replace;
  await send(it, { replace, confirmOpen: replace && a.openNow });
}

function send(it, { replace, confirmOpen }) {
  return new Promise((resolve) => {
    const q = new URLSearchParams({ folder: it.folder, name: it.name, view: lib.path });
    if (replace) q.set("replace", "1");
    if (confirmOpen) q.set("confirmOpen", "1");
    const xhr = new XMLHttpRequest();
    it.xhr = xhr;
    it.started = performance.now();
    setItem(it, "sending");
    xhr.open("POST", `api/library/upload?${q}`);
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.upload.onprogress = (e) => {
      it.sent = e.loaded;
      const secs = (performance.now() - it.started) / 1000;
      if (secs > 0.25) it.speed = e.loaded / secs;
      setItem(it, e.loaded >= it.size ? "finishing" : "sending");
    };
    xhr.onload = () => {
      let res = {};
      try { res = JSON.parse(xhr.responseText); } catch {}
      if (xhr.status >= 200 && xhr.status < 300) {
        it.sent = it.size;
        setItem(it, "done");
        if (res.folder?.path === lib.path) applyChange(res);
        else refreshAfterChange();
        const key = it.replace ? "log.replaced" : "log.uploaded";
        log(fill(t(key), { name: baseName(res.path ?? it.name), to: where(it.folder) }) + (it.book ? "" : " · " + t("up.notBookHint")) + warnings(res));
      } else {
        failItem(it, errorText(new APIError(res.error ?? "internal", res.message ?? "", res)));
      }
      resolve();
    };
    xhr.onerror = () => {
      failItem(it, t("up.lost"));
      resolve();
    };
    xhr.onabort = () => {
      setItem(it, "cancelled");
      log(fill(t("log.cancelled"), { name: it.name }));
      resolve();
    };
    xhr.send(it.file);
  });
}

function failItem(it, why) {
  it.note = why;
  setItem(it, "failed");
  log(fill(t("log.uploadFailed"), { name: it.name, why }));
}

function cancelUpload(id) {
  const it = uploads.items.find((x) => x.id === id);
  if (!it || FINISHED.has(it.state)) return;
  if (it.state === "sending" || it.state === "finishing") return it.xhr.abort();
  setItem(it, "cancelled");
  log(fill(t("log.cancelled"), { name: it.name }));
}

function clearQueue() {
  uploads.items = uploads.items.filter((x) => !FINISHED.has(x.state));
  renderQueue();
}

function setItem(it, state) {
  it.state = state;
  if (it.row) fillRow(it);
  else renderQueue();
  renderQueueHead();
}

function renderQueueHead() {
  const done = uploads.items.filter((x) => FINISHED.has(x.state)).length;
  $("queue-count").textContent = `${done}/${uploads.items.length}`;
  $("queue-clear").disabled = done === 0;
}

// Rows are built once and updated in place, so a cancel button stays under
// the pointer while progress events arrive.
function renderQueue() {
  $("queue").hidden = uploads.items.length === 0;
  $("queue-rows").replaceChildren(...uploads.items.map((it) => {
    if (!it.row) {
      it.row = el("li", { class: "qrow" });
      it.cells = {
        name: el("span", { class: "qname", title: it.name }),
        bar: el("span"),
        pct: el("span", { class: "num" }),
        speed: el("span", { class: "num qspeed" }),
        state: el("span", { class: "qstate" }),
        note: el("span", { class: "qnote" }),
      };
      it.row.append(it.cells.name, el("span", { class: "bar" }, it.cells.bar), it.cells.pct, it.cells.speed, it.cells.state, it.cells.note);
    }
    fillRow(it);
    return it.row;
  }));
  renderQueueHead();
}

function fillRow(it) {
  const c = it.cells;
  it.row.dataset.state = it.state;
  const name = [it.name, el("span", { class: "dim" }, bytes(it.size))];
  if (it.book === false) name.push(el("span", { class: "tag notbook", title: t("up.notBookHint") }, t("up.notBook")));
  c.name.replaceChildren(...name);
  const pct = it.size ? Math.floor((100 * it.sent) / it.size) : (it.state === "done" ? 100 : 0);
  c.bar.style.width = `${pct}%`;
  c.pct.textContent = `${pct}%`;
  c.speed.textContent = it.state === "sending" && it.speed ? `${bytes(it.speed)}/s` : "";
  if (FINISHED.has(it.state)) {
    c.state.replaceChildren(t(`up.${it.state}`));
  } else {
    c.state.replaceChildren(el("button", { type: "button", "data-action": "cancel-upload", "data-id": it.id, title: t("up.cancel") }, `${t(`up.${it.state}`)} ×`));
  }
  c.note.textContent = it.note ?? "";
  c.note.hidden = !it.note;
}

// Rows drag onto folder rows, the ".." row and folders in the tree.
let dragging = null;

$("rows").addEventListener("dragstart", (e) => {
  const row = e.target.closest?.("tr[data-select]");
  if (!row || lib.busy || lib.edit) return e.preventDefault();
  const p = row.dataset.select;
  if (!lib.sel.has(p)) {
    lib.sel = new Set([p]);
    lib.anchor = p;
    markSelection();
  }
  dragging = [...lib.sel];
  e.dataTransfer.effectAllowed = "move";
  e.dataTransfer.setData("text/plain", dragging.map(baseName).join("\n"));
});

document.addEventListener("dragend", () => {
  dragging = null;
  for (const d of document.querySelectorAll(".drop")) d.classList.remove("drop");
});

// Files dragged in from the computer upload into the open folder when
// dropped on the Files pane. Dropped anywhere else they are ignored, rather
// than the browser leaving the dashboard to open them.
const filesDrag = (e) => !dragging && [...(e.dataTransfer?.types ?? [])].includes("Files");

function dropping(on) {
  const pane = document.querySelector(".files");
  if (on) $("drop-hint").textContent = fill(t("drop.hint"), { folder: where(lib.path) });
  pane.classList.toggle("dropping", on);
}

document.addEventListener("dragleave", (e) => {
  if (filesDrag(e) && !e.relatedTarget) dropping(false);
});

function dropTarget(e) {
  const target = e.target.closest?.("[data-drop]");
  if (!target || !dragging) return null;
  const to = target.dataset.drop;
  // Not where they already are, and no folder into itself.
  if (to === lib.path || dragging.some((p) => to === p || to.startsWith(p + "/"))) return null;
  return target;
}

document.addEventListener("dragover", (e) => {
  if (filesDrag(e)) {
    e.preventDefault();
    const on = !!e.target.closest?.(".files") && !!lib.folder && !$("library").hidden;
    e.dataTransfer.dropEffect = on ? "copy" : "none";
    dropping(on);
    return;
  }
  const target = dropTarget(e);
  for (const d of document.querySelectorAll(".drop")) if (d !== target) d.classList.remove("drop");
  if (!target) return;
  e.preventDefault();
  e.dataTransfer.dropEffect = "move";
  target.classList.add("drop");
});

document.addEventListener("drop", (e) => {
  if (filesDrag(e)) {
    e.preventDefault();
    dropping(false);
    if (e.target.closest?.(".files") && lib.folder && !$("library").hidden) enqueue(e.dataTransfer.files);
    return;
  }
  const target = dropTarget(e);
  const paths = dragging;
  if (!target) return;
  e.preventDefault();
  target.classList.remove("drop");
  dragging = null;
  move(paths, target.dataset.drop);
});

$("upload-input").addEventListener("change", (e) => {
  enqueue(e.target.files);
  e.target.value = "";
});

$("manual").addEventListener("submit", (e) => {
  e.preventDefault();
  const address = $("manual-address").value.trim();
  if (address) search(address);
});

$("settings-form").addEventListener("submit", (e) => {
  e.preventDefault();
  saveSettings();
});

$("set-lang").addEventListener("change", (e) => {
  setLang(e.target.value).catch((err) => log(err.message));
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
    Object.assign(conn, { attempt: 0, address: "", failure: "", scanning: false, keepAwake: "" }, JSON.parse(e.data), { online: true });
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
