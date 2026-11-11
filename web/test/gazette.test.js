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
    await page.waitFor("!document.querySelector('#desk').hidden", 60000);
  });

  after(async () => {
    page?.close();
    await chrome?.close();
    app?.server.close();
  });

  // textContent, not innerText: innerText applies the uppercase of the CSS.
  const text = (selector) => page.eval(`document.querySelector(${JSON.stringify(selector)})?.textContent ?? ""`);
  const count = (selector) => page.eval(`document.querySelectorAll(${JSON.stringify(selector)}).length`);
  const last = (selector) => page.eval(`[...document.querySelectorAll('.entry')].at(-1)?.querySelector(${JSON.stringify(selector)})?.textContent ?? ""`);

  // type sends one command and waits until its answer is typed out.
  async function type(line) {
    const before = await count(".entry");
    await page.eval(`(() => {
      document.querySelector('#line').value = ${JSON.stringify(line)};
      document.querySelector('#carriage').requestSubmit();
      return true;
    })()`);
    await page.waitFor(`document.querySelectorAll('.entry').length > ${before}`);
    await new Promise((r) => setTimeout(r, 900)); // the typing and the scramble
  }

  test("starts with the handshake, not sealed", async () => {
    assert.equal(await count(".entry"), 1);
    assert.match(await text(".entry"), /Products\.Handshake/);
    assert.equal(await count(".entry .stamp.open"), 2);
    const client = await text("#client-key");
    assert.match(client, /^([0-9a-f]{2}:){11}[0-9a-f]{2}$/);
    assert.equal(await text("#client-knows"), await text("#server-key"));
    assert.match(await text("#match"), /intact/);
    assert.equal(await count("#products tr"), 3);
  });

  test("a command goes sealed and comes back sealed", async () => {
    await type(".create tea 3.50");
    assert.equal(await last(".typed"), ">.create tea 3.50");
    assert.match(await last(".leg.out .plain"), /Products\.Create\{"name":"tea","price":"3\.50"\}/);
    assert.match(await last(".leg.out .stamp"), /sealed · server key/);
    assert.match(await last(".leg.back .stamp"), /sealed · client key/);
    assert.match(await last(".leg.out .cipher"), /^[0-9a-f]{32}…1 block · 256 bytes$/);
    assert.equal(await last(".output"), "created #4  tea  3.50");
    assert.equal(await count("#products tr"), 4);
    assert.match(await text("#products"), /#4tea3\.50/);
  });

  test("the bytes on the wire hold the ciphertext, not the plaintext", async () => {
    const result = await page.eval(`(() => {
      const entry = [...document.querySelectorAll('.entry')].at(-1);
      entry.querySelector('details').open = true;
      const dumps = [...entry.querySelectorAll('.dump')].map((d) => d.textContent);
      return { sealed: dumps[0], wire: dumps[1] + dumps[2] };
    })()`);
    const block = result.sealed.split("\n")[1];
    assert.match(block, /^[0-9a-f]{512}$/);
    const hexOfWire = result.wire.split("\n").map((l) => l.slice(6, 53).replaceAll(" ", "")).join("");
    assert.ok(hexOfWire.includes(block.slice(0, 64)), "the block is on the wire");
    // The hexdump wraps the text column every 16 bytes, so look in the hex.
    const hexOf = (s) => [...s].map((c) => c.charCodeAt(0).toString(16).padStart(2, "0")).join("");
    assert.ok(hexOfWire.includes(hexOf("Products.Create")), "the method name is on the wire");
    assert.ok(!hexOfWire.includes(hexOf('"tea"')), "the plaintext is not on the wire");
  });

  test("errors come back sealed too", async () => {
    await type(".read 99");
    assert.equal(await last(".output"), "error: no product has this id");
    assert.match(await last(".leg.back .plain"), /\{"error":"no product has this id"\}/);
    assert.match(await last(".leg.back .stamp"), /sealed/);
  });

  test("a typing error never reaches the wire", async () => {
    await type(".create tea");
    assert.equal(await last(".output"), "error: usage: .create <name> <price>");
    assert.equal(await page.eval("[...document.querySelectorAll('.entry')].at(-1).querySelectorAll('.leg').length"), 0);
    await type("hello");
    assert.match(await last(".output"), /commands start with a dot/);
  });

  test("updates, deletes, and lists", async () => {
    await type(".update 1 brown_rice 5");
    assert.equal(await last(".output"), "updated #1  brown_rice  5.00");
    await type(".delete 3");
    assert.equal(await last(".output"), "deleted #3");
    await type(".list");
    assert.match(await last(".output"), /#1 {2}brown_rice {2}5\.00\n#2 {2}coffee {2}12\.90\n#4 {2}tea {2}3\.50/);
    assert.equal(await count("#products tr"), 3);
  });

  test("the chips fill the carriage, and the arrows recall commands", async () => {
    await page.eval("[...document.querySelectorAll('#chips button')].find((b) => b.textContent === '.help').click(), true");
    assert.equal(await page.eval("document.querySelector('#line').value"), ".help");
    await page.eval("document.querySelector('#line').value = ''; true");
    await page.eval("document.querySelector('#line').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp' })), true");
    assert.equal(await page.eval("document.querySelector('#line').value"), ".list");
  });

  test(".clear empties the sheet, and Start over opens a new connection", async () => {
    await type(".help");
    await page.eval(`(() => {
      document.querySelector('#line').value = '.clear';
      document.querySelector('#carriage').requestSubmit();
      return true;
    })()`);
    await page.waitFor("document.querySelectorAll('.entry').length === 0");
    await page.click("#reset");
    await page.waitFor("document.querySelectorAll('.entry').length === 1");
    assert.equal(await count("#products tr"), 3);
    assert.match(await text("#issue"), /No\. 1 /);
  });

  test("the page had no JavaScript errors", () => {
    assert.deepEqual(page.errors, []);
  });
});
