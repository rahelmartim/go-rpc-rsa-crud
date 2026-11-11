/* The RPC Gazette. The Go server and client run in WebAssembly (see
   cmd/gazette-wasm). This file only draws their state. */

const $ = (selector) => document.querySelector(selector);

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") node.className = value;
    else if (key.startsWith("on")) node.addEventListener(key.slice(2), value);
    else node.setAttribute(key, value === true ? "" : String(value));
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

let state = null;

/* Talking to Go */

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const script = el("script", { src });
    script.onload = resolve;
    script.onerror = () => reject(new Error(`cannot load ${src}`));
    document.head.append(script);
  });
}

async function start() {
  $("#date").textContent = new Date().toLocaleDateString("en-US", { weekday: "long", year: "numeric", month: "long", day: "numeric" });
  // Making the RSA keys keeps the page busy for a moment. Wait until the
  // page has loaded and painted, so that the reader sees why.
  if (document.readyState !== "complete") await new Promise((r) => window.addEventListener("load", r, { once: true }));
  await new Promise((r) => setTimeout(r, 60));
  try {
    await loadScript("wasm_exec.js");
    const go = new window.Go();
    const ready = new Promise((resolve) => window.addEventListener("gazette-ready", resolve, { once: true }));
    const result = await WebAssembly.instantiateStreaming(fetch("gazette.wasm"), go.importObject);
    go.run(result.instance);
    await ready;
  } catch (error) {
    $("#presses").textContent = `The presses stopped: ${error.message}`;
    return;
  }
  $("#presses").hidden = true;
  $("#paper").hidden = false;
  await act("state");
}

// act runs an action in Go and draws the new state. It returns the error
// text, or "" when the action worked.
async function act(action, args = {}) {
  // Go reads the arguments as a map of strings.
  const strings = Object.fromEntries(Object.entries(args).map(([k, v]) => [k, String(v)]));
  const reply = JSON.parse(await window.gazette.do(action, JSON.stringify(strings)));
  state = reply.state;
  render();
  return reply.ok ? "" : reply.error;
}

/* Drawing */

const clock = (iso) => new Date(iso).toLocaleTimeString("en-GB", { hour12: false });

function render() {
  if (!state) return;
  $("#issue").textContent = `No. ${state.calls} · ${state.bytes.toLocaleString("en-US")} bytes printed`;
  $("#client-key").textContent = state.clientKey;
  $("#server-key").textContent = state.serverKey;
  $("#client-knows").textContent = state.clientKnows || "no handshake yet";
  const same = state.clientKnows === state.serverKey;
  $("#match").textContent = same ? "✓ the same key as the server" : "✗ not the key of the server";
  $("#match").className = `match${same ? "" : " bad"}`;
  renderLogin();
  renderProducts();
  renderDesk();
  renderWire();
}

function renderLogin() {
  const login = state.login;
  $("#sealed").hidden = !login;
  if (!login) return;
  $("#plaintext").textContent = login.plaintext || "(nothing was sent)";
  $("#ciphertext").textContent = login.ciphertext ? login.ciphertext.match(/.{1,48}/g).join("\n") : "";
  $("#cipher-size").textContent = login.bytes ? `· ${login.bytes} bytes` : "";
  const verdict = $("#verdict");
  verdict.className = `verdict ${login.ok ? "ok" : "bad"}`;
  verdict.textContent = login.ok
    ? `The server opened the envelope with its private key, and ${state.user} is logged in on this connection.`
    : `The server refused it: ${login.error}.`;
}

function renderProducts() {
  $("#session-line").textContent = state.user
    ? `Logged in as ${state.user}. The list shows the catalog as the server stores it.`
    : "You are not logged in. The list shows the catalog as the server stores it, but the server refuses your calls. Try one, then read the wire.";
  const body = $("#products");
  body.replaceChildren();
  for (const p of state.products) {
    body.append(
      el("tr", {},
        el("td", { class: "no" }, `#${p.id}`),
        el("td", { class: "item" }, p.name),
        el("td", { class: "num" }, (p.price / 100).toFixed(2)),
        el("td", { class: "actions" },
          el("button", { type: "button", title: "Call Products.Read", onclick: () => run("read", { id: p.id }, `Read #${p.id}.`) }, "read"),
          el("button", { type: "button", title: "Change this product", onclick: () => edit(p) }, "edit"),
          el("button", { type: "button", title: "Call Products.Delete", onclick: () => run("delete", { id: p.id }, `Removed #${p.id}.`) }, "remove"))),
    );
  }
  if (!state.products.length) body.append(el("tr", { class: "empty" }, el("td", { colspan: 4 }, "No ads today.")));
}

function renderDesk() {
  const list = $("#desk");
  list.replaceChildren(
    ...[...state.desk].reverse().map((line) =>
      el("li", { class: /refused/.test(line.text) ? "refused" : "" },
        el("time", {}, `${clock(line.time)} · connection ${line.conn}`), line.text)),
  );
}

function fields(map) {
  const entries = Object.entries(map ?? {});
  if (!entries.length) return [el("dt", {}, "fields"), el("dd", {}, "none")];
  return entries.flatMap(([k, v]) => [el("dt", {}, k), el("dd", {}, String(v))]);
}

// dump writes bytes like hexdump: offset, 16 bytes, and their text.
function dump(hex, total) {
  const bytes = hex.match(/../g) ?? [];
  const lines = [];
  for (let i = 0; i < bytes.length && i < 512; i += 16) {
    const row = bytes.slice(i, i + 16);
    const text = row.map((b) => {
      const c = parseInt(b, 16);
      return c >= 32 && c < 127 ? String.fromCharCode(c) : "·";
    }).join("");
    lines.push(`${i.toString(16).padStart(4, "0")}  ${row.join(" ").padEnd(47)}  ${text}`);
  }
  if (total > Math.min(bytes.length, 512)) lines.push(`… ${total - Math.min(bytes.length, 512)} more bytes`);
  return lines.join("\n");
}

function renderWire() {
  const box = $("#stories");
  box.replaceChildren(
    ...[...state.exchanges].reverse().map((x) =>
      el("article", { class: "story" },
        el("h3", {}, x.method),
        el("p", { class: "meta" }, `No. ${x.seq} · ${clock(x.time)} · ${x.sent} B out · ${x.received} B in · ${x.millis.toFixed(1)} ms`),
        el("dl", {}, el("dt", {}, "sent"), el("dd", {}, ""), ...fields(x.args)),
        x.error
          ? el("p", { class: "refused" }, `Refused: ${x.error}`)
          : el("dl", {}, el("dt", {}, "reply"), el("dd", {}, ""), ...fields(x.reply)),
        el("details", {},
          el("summary", {}, "Bytes on the wire"),
          el("p", { class: "label" }, "client → server"),
          el("pre", { class: "dump" }, dump(x.sentHex, x.sent)),
          el("p", { class: "label" }, "server → client"),
          el("pre", { class: "dump" }, dump(x.recvHex, x.received))))),
  );
}

/* Actions */

function notice(text, bad = false) {
  $("#notice").textContent = text;
  $("#notice").className = `notice${bad ? " bad" : ""}`;
}

async function run(action, args, done) {
  const error = await act(action, args);
  notice(error ? `The server refused it: ${error}.` : done, Boolean(error));
  return error;
}

function edit(p) {
  const { elements } = $("#product-form");
  elements.id.value = p.id;
  elements.name.value = p.name;
  elements.price.value = (p.price / 100).toFixed(2);
  $("#form-title").textContent = `Change ad #${p.id}`;
  $("#product-save").textContent = "Reprint it";
  $("#product-cancel").hidden = false;
  elements.name.focus();
}

function resetForm() {
  const form = $("#product-form");
  form.reset();
  form.elements.id.value = "";
  $("#form-title").textContent = "Place an ad";
  $("#product-save").textContent = "Print it";
  $("#product-cancel").hidden = true;
}

$("#product-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const { id, name, price } = Object.fromEntries(new FormData(form));
  const error = id
    ? await run("update", { id, name, price }, `Ad #${id} was reprinted.`)
    : await run("create", { name, price }, `The ad for ${name.trim()} was printed.`);
  if (!error) resetForm();
});

$("#product-cancel").addEventListener("click", resetForm);

$("#login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const { user, password } = Object.fromEntries(new FormData(event.currentTarget));
  await act("login", { user, password });
});

$("#logout").addEventListener("click", () => run("logout", {}, "You logged out."));

$("#reset").addEventListener("click", async () => {
  resetForm();
  notice("");
  $("#tape").replaceChildren("A new connection. Type .help for the list of commands.");
  await act("reset");
});

$("#telegraph-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const input = event.currentTarget.line;
  const line = input.value.trim();
  if (!line) return;
  input.value = "";
  const reply = JSON.parse(await window.gazette.exec(line));
  state = reply.state;
  render();
  const tape = $("#tape");
  tape.append("\n", el("span", { class: "cmd" }, `> ${line}`), "\n",
    reply.ok ? reply.output : el("span", { class: "err" }, `error: ${reply.error}`));
  tape.scrollTop = tape.scrollHeight;
});

start();
