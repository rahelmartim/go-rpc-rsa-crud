// Tests The RPC Gazette in a headless Chrome, on the WebAssembly build in
// site/ (run "make web" first).

import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { after, before, describe, test } from "node:test";
import { findChrome, launchChrome, serveApp } from "./browser.js";

const chromePath = findChrome();
const built = existsSync(resolve(import.meta.dirname, "..", "..", "site", "gazette.wasm"));
const skip = !chromePath ? "Chrome is not installed" : !built ? "run make web first" : false;

describe("The RPC Gazette in the browser", { skip }, () => {
  let chrome;
  let app;
  let page;

  before(async () => {
    app = await serveApp();
    chrome = await launchChrome(chromePath);
    page = await chrome.newPage({ width: 1400, height: 1200 });
    await page.goto(app.url);
    await page.waitFor("!document.querySelector('#paper').hidden", 60000);
  });

  after(async () => {
    page?.close();
    await chrome?.close();
    app?.server.close();
  });

  // textContent, not innerText: innerText applies the uppercase of the CSS.
  const text = (selector) => page.eval(`document.querySelector(${JSON.stringify(selector)})?.textContent ?? ""`);
  const count = (selector) => page.eval(`document.querySelectorAll(${JSON.stringify(selector)}).length`);
  const stories = () => page.eval("[...document.querySelectorAll('.story h3')].map((h) => h.textContent)");
  const submit = (form, values) =>
    page.eval(`(() => {
      const form = document.querySelector(${JSON.stringify(form)});
      for (const [k, v] of Object.entries(${JSON.stringify(values)})) form.elements[k].value = v;
      form.requestSubmit();
      return true;
    })()`);
  const telegraph = async (line) => {
    const before = await page.eval("document.querySelector('#tape').textContent.length");
    await submit("#telegraph-form", { line });
    await page.waitFor(`document.querySelector('#tape').textContent.length > ${before}`);
    return page.eval("document.querySelector('#tape').textContent");
  };

  test("prints the handshake on the front page", async () => {
    assert.equal(await count("#products tr"), 3);
    assert.deepEqual(await stories(), ["Products.Handshake"]);
    const client = await text("#client-key");
    const server = await text("#server-key");
    assert.match(client, /^([0-9a-f]{2}:){11}[0-9a-f]{2}$/);
    assert.notEqual(client, server);
    assert.equal(await text("#client-knows"), server);
    assert.match(await text("#match"), /the same key/);
    assert.match(await text("#desk"), new RegExp(`handshake: client key ${client}`));
    assert.match(await text("#issue"), /No\. 1/);
  });

  test("the server refuses calls before the login", async () => {
    await page.eval("document.querySelector('#products .actions button:last-child').click(), true");
    await page.waitFor("document.querySelector('#notice').textContent.includes('log in first')");
    assert.equal(await count("#products tr"), 3);
    assert.match(await text(".story"), /Products\.Delete[\s\S]*Refused: log in first/);
    assert.match(await text("#desk"), /delete refused: log in first/);
  });

  test("a wrong password is refused, the right one opens the envelope", async () => {
    await submit("#login-form", { user: "admin", password: "nope" });
    await page.waitFor("document.querySelector('#verdict').textContent.includes('refused')");
    assert.match(await text("#verdict"), /wrong user or password/);

    await submit("#login-form", { user: "admin", password: "chatuba" });
    await page.waitFor("document.querySelector('#verdict').classList.contains('ok')");
    assert.equal(await text("#plaintext"), '{"user":"admin","password":"chatuba"}');
    const cipher = (await text("#ciphertext")).replaceAll("\n", "");
    assert.match(cipher, /^[0-9a-f]{512}$/);
    assert.match(await text("#cipher-size"), /256 bytes/);
    assert.match(await text("#session-line"), /Logged in as admin/);
    assert.match(await text("#desk"), /decrypted the login, admin is logged in/);
    assert.doesNotMatch(await text("#desk"), /chatuba|nope/);
  });

  test("the bytes of the login on the wire are the ciphertext", async () => {
    const cipher = (await text("#ciphertext")).replaceAll("\n", "");
    const dump = await page.eval(`(() => {
      const story = [...document.querySelectorAll('.story')].find((s) => s.querySelector('h3').textContent === 'Products.Login' && !s.querySelector('.refused'));
      story.querySelector('details').open = true;
      return story.querySelector('.dump').textContent;
    })()`);
    const hex = dump.split("\n").map((l) => l.slice(6, 53).replaceAll(" ", "")).join("");
    assert.ok(hex.includes(cipher.slice(0, 64)), "the dump has the start of the ciphertext");
    assert.ok(!dump.includes("chatuba"), "the dump has no password");
  });

  test("places, changes, reads, and removes an ad", async () => {
    await submit("#product-form", { name: "tea", price: "3,5" });
    await page.waitFor("document.querySelectorAll('#products tr').length === 4");
    assert.match(await text("#products"), /#4tea3\.50/);

    await page.eval("[...document.querySelectorAll('#products tr')][3].querySelector('button[title=\"Change this product\"]').click(), true");
    assert.equal(await text("#form-title"), "Change ad #4");
    await submit("#product-form", { name: "green tea", price: "4" });
    await page.waitFor("document.querySelector('#products').textContent.includes('green tea')");
    assert.equal(await text("#form-title"), "Place an ad");
    assert.equal(await count("#products tr"), 4);

    await page.eval("[...document.querySelectorAll('#products tr')][3].querySelector('button').click(), true");
    await page.waitFor("document.querySelector('.story h3').textContent === 'Products.Read'");
    assert.match(await text(".story"), /green tea[\s\S]*4\.00/);

    await page.eval("[...document.querySelectorAll('#products tr')][3].querySelector('.actions button:last-child').click(), true");
    await page.waitFor("document.querySelectorAll('#products tr').length === 3");
  });

  test("bad input comes back as a refusal, not a crash", async () => {
    await submit("#product-form", { name: "tea", price: "free" });
    await page.waitFor("document.querySelector('#notice').classList.contains('bad')");
    assert.match(await text("#notice"), /a price is a number/);
    await page.eval("document.querySelector('#product-cancel').click(), true");
  });

  test("the telegraph runs the commands of the original client", async () => {
    let tape = await telegraph(".list");
    assert.match(tape, /#2 {2}coffee {2}12\.90/);
    tape = await telegraph(".read 99");
    assert.match(tape, /error: no product has this id/);
    tape = await telegraph("hello");
    assert.match(tape, /commands start with a dot/);
    tape = await telegraph(".help");
    assert.match(tape, /\.update <id> <name> <price>/);
    tape = await telegraph(".logout");
    assert.match(tape, /logged out/);
    assert.match(await text("#session-line"), /not logged in/);
  });

  test("the first call of a kind carries the gob type definitions", async () => {
    await page.click("#reset");
    await page.waitFor("document.querySelectorAll('.story').length === 1");
    await telegraph(".login admin chatuba");
    await telegraph(".read 1");
    await telegraph(".read 2");
    const sizes = await page.eval(`[...document.querySelectorAll('.story')]
      .filter((s) => s.querySelector('h3').textContent === 'Products.Read' && !s.querySelector('.refused'))
      .map((s) => Number(s.querySelector('.meta').textContent.match(/(\\d+) B in/)[1]))`);
    // Newest first: the second read is smaller than the first.
    assert.ok(sizes.length >= 2 && sizes[0] < sizes.at(-1), `sizes ${sizes}`);
  });

  test("start over opens a new connection", async () => {
    await page.click("#reset");
    await page.waitFor("document.querySelectorAll('.story').length === 1");
    assert.equal(await count("#products tr"), 3);
    assert.equal(await page.eval("document.querySelector('#sealed').hidden"), true);
    assert.match(await text("#session-line"), /not logged in/);
  });

  test("the page had no JavaScript errors", () => {
    assert.deepEqual(page.errors, []);
  });
});
