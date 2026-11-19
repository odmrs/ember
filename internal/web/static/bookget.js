/* Ebook catalog. Every network request stays on ember's own origin. */
(() => {
  "use strict";
  const form = document.getElementById("catalog-search");
  const query = document.getElementById("catalog-query");
  const language = document.getElementById("catalog-language");
  const format = document.getElementById("catalog-format");
  const results = document.getElementById("catalog-results");
  const notice = document.getElementById("catalog-status");
  let busy = false;
 let statusTimer;
 let polling=false;
  const message = (key) => typeof t === "function" ? t(key) : key;
  function setBusy(value) {
    busy = value;
    if(value){statusTimer=setInterval(async()=>{
      if(polling||!busy)return;polling=true;
      try{const response=await fetch("api/books/status");if(response.ok){const state=await response.json();if(busy&&state.busy&&state.message)notice.textContent=state.message;}}catch{}finally{polling=false}
    },1500);}else{clearInterval(statusTimer);}
    form.querySelectorAll("input, select, button").forEach(el => { el.disabled = value; });
    results.querySelectorAll("button").forEach(el => { el.disabled = value; });
    results.setAttribute("aria-busy", String(value));
  }
  async function request(path, body) {
    const options = body ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {};
    const response = await fetch(path, options);
    const data = await response.json();
    if (!response.ok) throw new Error(data.message || message("catalog.failed"));
    return data;
  }
  form.addEventListener("submit", async event => {
    event.preventDefault();
    if (busy || !query.value.trim()) return;
    results.replaceChildren();
    setBusy(true);
    notice.textContent = message("catalog.searching");
    try {
      const params = new URLSearchParams({ q: query.value.trim() });
      if (language.value) params.set("language", language.value);
      const data = await request(`api/books/search?${params}`);
      notice.textContent = `${data.books.length ? message("catalog.choose") : message("catalog.empty")}${data.warnings ? ` · ${data.warnings}` : ""}`;
      data.books.forEach(book => {
        const li = document.createElement("li");
        const detail = document.createElement("span");
        // Metadata is untrusted text, never HTML.
        detail.textContent = `${book.Title} — ${book.Author || "—"} · ${book.Year || "—"} · ${book.Publisher || book.Source} · ${Object.keys(book.Formats || {}).sort().join(", ")}`;
        const meta = document.createElement("small");
        meta.textContent = book.Details || "";
        detail.append(document.createElement("br"), meta);
        const button = document.createElement("button");
        button.type = "button";
        button.className = "secondary";
        button.textContent = message("catalog.send");
        button.addEventListener("click", () => download(book.token));
        li.append(detail, button);
        results.append(li);
      });
    } catch (error) { notice.textContent = error.message; }
    finally { setBusy(false); }
  });
  async function download(token) {
    if (busy) return;
    setBusy(true);
    notice.textContent = message("catalog.downloading");
    try {
      const data = await request("api/books/get", { token, format: format.value });
      notice.textContent = `${message(data.sent ? "catalog.sent" : "catalog.saved")} ${data.path}${data.messages?.length ? ` · ${data.messages.join(" · ")}` : ""}`;
      if (data.sent && typeof refreshLibrary === "function") refreshLibrary();
    } catch (error) { notice.textContent = error.message; }
    finally { setBusy(false); }
  }
})();
