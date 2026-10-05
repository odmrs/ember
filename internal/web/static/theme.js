"use strict";

// Loaded in <head> without defer so the theme is on <html> before the first
// paint. "auto" follows the system; "light" and "dark" are the user's
// override, kept per browser.
const THEME_KEY = "ember.theme";
const THEME_MODES = ["auto", "light", "dark"];
const systemLight = matchMedia("(prefers-color-scheme: light)");
let themeMode = savedThemeMode();

function savedThemeMode() {
  try {
    const v = localStorage.getItem(THEME_KEY);
    if (v === "light" || v === "dark") return v;
  } catch {}
  return "auto";
}

function applyTheme() {
  const theme = themeMode === "auto" ? (systemLight.matches ? "light" : "dark") : themeMode;
  document.documentElement.dataset.theme = theme;
}

// nextThemeMode moves auto → light → dark → auto and remembers the choice.
function nextThemeMode() {
  themeMode = THEME_MODES[(THEME_MODES.indexOf(themeMode) + 1) % THEME_MODES.length];
  try {
    if (themeMode === "auto") localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, themeMode);
  } catch {}
  applyTheme();
  return themeMode;
}

systemLight.addEventListener("change", applyTheme);
applyTheme();
