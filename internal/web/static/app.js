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
    $("path").textContent = d.libraryRoot;
    $("free").textContent = bytes(d.survey.freeBytes);
    $("device-line").textContent = `${d.user}@${address} · ${d.fingerprint}`;
  }
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
  if (!b) return;
  switch (b.dataset.action) {
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
    if (was !== "linked" && conn.phase === "linked") log(`${t("log.linked")} ${conn.address}`);
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
