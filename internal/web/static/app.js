"use strict";

const LANGS = { en: "en", pt: "pt-BR" };
let messages = {};

function t(key) {
  return messages[key] ?? key;
}

async function getJSON(path) {
  const res = await fetch(path);
  if (!res.ok) throw new Error(`${path}: ${res.status}`);
  return res.json();
}

function savedLang() {
  try {
    const v = localStorage.getItem("ember.lang");
    if (v in LANGS) return v;
  } catch {}
  return navigator.language?.toLowerCase().startsWith("pt") ? "pt" : "en";
}

async function setLang(lang) {
  messages = await getJSON(`i18n/${lang}.json`);
  try { localStorage.setItem("ember.lang", lang); } catch {}
  document.documentElement.lang = LANGS[lang];
  for (const el of document.querySelectorAll("[data-i18n]")) el.textContent = t(el.dataset.i18n);
  for (const b of document.querySelectorAll("[data-lang]")) b.setAttribute("aria-pressed", b.dataset.lang === lang);
  render();
}

const status = { version: "", state: "unpaired" };
const STATE_LABEL = { unpaired: "state.unpaired", searching: "state.searching", linked: "state.linked" };

function render() {
  document.getElementById("link").dataset.state = status.state;
  document.getElementById("link-text").textContent = t(STATE_LABEL[status.state]);
  if (status.version) document.getElementById("version-line").textContent = `ember ${status.version} · ${location.host}`;
}

function log(text) {
  const el = document.getElementById("log");
  delete el.dataset.i18n;
  el.textContent = text;
}

document.addEventListener("click", (e) => {
  const b = e.target.closest("[data-lang]");
  if (b) setLang(b.dataset.lang).catch((err) => log(err.message));
});

setLang(savedLang())
  .then(() => getJSON("api/status"))
  .then((s) => { Object.assign(status, s); render(); })
  .catch((err) => log(err.message));
