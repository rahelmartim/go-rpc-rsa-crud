/* The RPC Gazette. The Go server and client run in WebAssembly (see
   cmd/gazette-wasm). This file types their story on the sheet. */

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

const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
const EXAMPLES = [".list", ".create tea 3.50", ".read 1", ".update 1 brown_rice 5", ".delete 3", ".read 99", ".help", ".clear"];

let state = null;
let known = new Map(); // product id → "name price", to mark changed rows
const history = [];
let back = 0;

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
    $("#presses").textContent = `The typewriter jammed: ${error.message}`;
    return;
  }
  $("#presses").hidden = true;
  $("#desk").hidden = false;
  setupChips();
  const reply = JSON.parse(await window.gazette.state());
  show(reply.state);
  printHandshake();
  $("#line").focus();
}

/* The keys and the catalog */

function show(next) {
  state = next;
  $("#issue").textContent = `No. ${state.calls} · ${state.bytes.toLocaleString("en-US")} bytes on the wire`;
  $("#client-key").textContent = state.clientKey;
  $("#server-key").textContent = state.serverKey;
  $("#client-knows").textContent = state.clientKnows || "no handshake yet";
  $("#block-size").textContent = state.blockSize;
  const same = state.clientKnows === state.serverKey;
  $("#match").textContent = same ? "✓ it arrived intact" : "✗ not the key of the server";
  $("#match").className = `match${same ? "" : " bad"}`;

  const body = $("#products");
  body.replaceChildren();
  const seen = new Map();
  for (const p of state.products) {
    const line = `${p.name} ${p.price}`;
    seen.set(p.id, line);
    const changed = known.size > 0 && known.get(p.id) !== line;
    body.append(
      el("tr", { class: changed ? "changed" : "" },
        el("td", { class: "no" }, `#${p.id}`),
        el("td", {}, p.name),
        el("td", { class: "num" }, (p.price / 100).toFixed(2))),
    );
  }
  if (!state.products.length) body.append(el("tr", {}, el("td", { colspan: 3, class: "no" }, "The catalog is empty.")));
  known = seen;
}

/* Typing on the sheet */

const randomHex = (n) => Array.from({ length: n }, () => "0123456789abcdef"[Math.floor(Math.random() * 16)]).join("");

// scramble shows random hex that settles into the real ciphertext.
function scramble(node, text) {
  if (still) {
    node.textContent = text;
    return;
  }
  const start = performance.now();
  const step = (now) => {
    const done = Math.min(1, (now - start) / 450);
    const fixed = Math.floor(text.length * done);
    node.textContent = text.slice(0, fixed) + randomHex(text.length - fixed);
    if (done < 1) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}

// typeOut types the text one letter at a time, like a typewriter.
function typeOut(node, text) {
  if (still || text.length > 600) {
    node.textContent = text;
    return;
  }
  let i = 0;
  const each = Math.max(4, Math.min(14, 500 / Math.max(1, text.length)));
  const tick = () => {
    i += 1;
    node.textContent = text.slice(0, i);
    if (i < text.length) setTimeout(tick, each);
  };
  tick();
}

function sealedCell(blocks, key, animate) {
  if (!blocks?.length) return [el("span", { class: "stamp open" }, "not sealed"), el("span", { class: "cipher" }, "")];
  const preview = blocks[0].slice(0, 32) + "…";
  const text = el("span", {});
  if (animate) scramble(text, preview);
  else text.textContent = preview;
  return [
    el("span", { class: "stamp", title: `RSA-OAEP with the ${key} key` }, `sealed · ${key} key`),
    el("span", { class: "cipher" }, text, el("small", {}, `${blocks.length} block${blocks.length > 1 ? "s" : ""} · ${blocks.length * 256} bytes`)),
  ];
}

// dump writes bytes like hexdump: offset, 16 bytes, and their text.
function dump(hex, total) {
  const bytes = hex.match(/../g) ?? [];
  const lines = [];
  for (let i = 0; i < bytes.length && i < 768; i += 16) {
    const row = bytes.slice(i, i + 16);
    const text = row.map((b) => {
      const c = parseInt(b, 16);
      return c >= 32 && c < 127 ? String.fromCharCode(c) : "·";
    }).join("");
    lines.push(`${i.toString(16).padStart(4, "0")}  ${row.join(" ").padEnd(47)}  ${text}`);
  }
  const shown = Math.min(bytes.length, 768);
  if (total > shown) lines.push(`… ${total - shown} more bytes`);
  return lines.join("\n");
}

// clip keeps a line of the sheet short. The full text is in its tooltip.
const clip = (text) => (text.length > 100 ? `${text.slice(0, 100)}…` : text);

function callView(x, animate) {
  const handshake = x.method === "Products.Handshake";
  return [
    el("div", { class: "leg out" },
      el("span", { class: "arrow", title: "client to server" }, "→"),
      el("span", { class: "plain", title: x.request }, el("span", { class: "method" }, x.method), clip(x.request) || "(nothing)"),
      ...sealedCell(x.sealed, "server", animate && !handshake)),
    el("div", { class: "leg back" },
      el("span", { class: "arrow", title: "server to client" }, "←"),
      el("span", { class: "plain", title: x.reply }, clip(x.reply) || (x.error ? `error: ${x.error}` : "(nothing)")),
      ...sealedCell(x.replySealed, "client", animate && !handshake)),
    el("details", {},
      el("summary", {}, `bytes on the wire · ${x.sent} out, ${x.received} in · ${x.millis.toFixed(1)} ms`),
      x.sealed.length ? el("pre", { class: "dump" }, `the request, sealed (${x.sealed.length} × 256 bytes):\n`, el("b", {}, x.sealed.join("\n"))) : null,
      el("pre", { class: "dump" }, `client → server, as gob:\n${dump(x.sentHex, x.sent)}`),
      el("pre", { class: "dump" }, `server → client, as gob:\n${dump(x.recvHex, x.received)}`)),
  ];
}

function print(entry, { system = false } = {}) {
  const output = el("p", { class: `output${entry.error ? " err" : ""}` });
  const node = el("article", { class: "entry" },
    el("p", { class: `typed${system ? " system" : ""}` }, system ? null : el("span", { class: "prompt" }, ">"), entry.line),
    ...entry.calls.flatMap((x) => callView(x, true)),
    output);
  $("#entries").append(node);
  typeOut(output, entry.error ? `error: ${entry.error}` : entry.output);
  node.scrollIntoView({ block: "nearest", behavior: still ? "auto" : "smooth" });
  $("#carriage").scrollIntoView({ block: "nearest" });
}

function printHandshake() {
  if (!state.handshake) return;
  print({ line: "The client connects, and the two sides trade public keys.", output: "Keys exchanged. Everything from here on is sealed.", calls: [state.handshake] }, { system: true });
}

/* The carriage */

async function type(line) {
  line = line.trim();
  if (!line) return;
  history.push(line);
  back = history.length;
  if (line.toLowerCase() === ".clear") {
    $("#entries").replaceChildren();
    return;
  }
  const reply = JSON.parse(await window.gazette.exec(line));
  show(reply.state);
  print(reply.entry);
}

$("#carriage").addEventListener("submit", async (event) => {
  event.preventDefault();
  const input = $("#line");
  const line = input.value;
  input.value = "";
  await type(line);
});

$("#line").addEventListener("keydown", (event) => {
  if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
  event.preventDefault();
  back = Math.max(0, Math.min(history.length, back + (event.key === "ArrowUp" ? -1 : 1)));
  event.currentTarget.value = history[back] ?? "";
});

function setupChips() {
  $("#chips").replaceChildren(
    ...EXAMPLES.map((line) =>
      el("button", { type: "button", onclick: () => { $("#line").value = line; $("#line").focus(); } }, line)),
  );
}

$("#reset").addEventListener("click", async () => {
  const reply = JSON.parse(await window.gazette.reset());
  known = new Map();
  $("#entries").replaceChildren();
  show(reply.state);
  printHandshake();
});

start();
