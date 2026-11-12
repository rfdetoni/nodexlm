#!/usr/bin/env node
'use strict';
// Real browser checks against the Host and model started by e2e-rpc.py.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');

(async () => {
  const [base, token] = process.argv.slice(2);
  assert(base && token, 'Usage: test-console.cjs HOST_URL ADMIN_TOKEN');
  const out = path.resolve(process.env.NODEXLM_UI_ARTIFACTS || 'ui-artifacts');
  await fs.mkdir(out, {recursive: true});
  const browser = await chromium.launch({headless: true});
  try {
    const page = await browser.newPage({viewport: {width: 1280, height: 900}});
    const errors = [];
    const chatRequests = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => {
      if(request.url().endsWith('/api/chat') && request.method()==='POST') {
        try { chatRequests.push(request.postDataJSON()); } catch { /* invalid test payload is caught by assertions */ }
      }
    });
    await page.goto(base);
    await page.locator('#secret').fill(token);
    await page.locator('#login button').click();
    await page.locator('#app').waitFor({state: 'visible'});
    // The desktop and mobile consoles use separate, hidden workspaces.
    // A field existing in the DOM does not make it visible to Playwright.
    await page.locator('#nav-chat').click();
    await page.locator('#view-chat').waitFor({state: 'visible'});
    await page.waitForFunction(() => document.querySelector('#chatModel').options.length > 0);
    await page.locator('#chatTokens').fill('8');
    await page.locator('#chatEffort').selectOption('none');
    for (const prompt of ['Once upon a time', 'Then the little bird']) {
      await page.locator('#prompt').fill(prompt);
      await page.locator('#sendChat').click();
      await page.waitForFunction(() => document.querySelector('#prompt').value === '' && !document.querySelector('#sendChat').disabled);
    }
    assert.equal(await page.evaluate(() => JSON.parse(localStorage.getItem('nodexlm-chat')).length), 4);
    assert.equal(chatRequests.length,2,'both conversations must reach the native backend');
    assert(chatRequests.every(x=>x.reasoning_effort==='none' && x.reasoning_budget_tokens===undefined),
      'disabling thinking must be forwarded as reasoning_effort none without an active token budget');
    await page.locator('#chatEffort').selectOption('max');
    await page.locator('#chatThinkingBudget').fill('4');
    await page.locator('#chatThinkingBudget').dispatchEvent('change');
    await page.reload();
    await page.locator('#app').waitFor({state: 'visible'});
    await page.locator('#nav-chat').click();
    await page.locator('#view-chat').waitFor({state: 'visible'});
    await page.locator('#chatOutput').getByText('Then the little bird', {exact:true}).waitFor();
    assert.equal(await page.locator('#chatEffort').inputValue(), 'max',
      'reasoning effort must persist across browser reload');
    assert.equal(await page.locator('#chatThinkingBudget').inputValue(), '4',
      'thinking token budget must persist across browser reload');
    await page.locator('#nav-inference').click();
    await page.locator('#view-inference').waitFor({state: 'visible'});
    await page.locator('#previewPlan').click();
    await page.locator('#planOutput').getByText('KV F16 estimado:', {exact: false}).waitFor();
    await page.locator('#nav-observability').click();
    await page.locator('#view-observability').waitFor({state: 'visible'});
    await page.locator('#refreshObservations').click();
    await page.locator('#observations').getByText('tokens/s', {exact: false}).first().waitFor();
    assert.equal(await page.evaluate(() => JSON.parse(localStorage.getItem('nodexlm-chat')).length), 4);
    await page.locator('#nav-chat').click();
    await page.locator('#view-chat').waitFor({state: 'visible'});
    await page.locator('#clearChat').click();
    await page.locator('#confirmation button[value="cancel"]').click();
    assert.equal(await page.evaluate(() => JSON.parse(localStorage.getItem('nodexlm-chat')).length), 4);
    await page.locator('#clearChat').click();
    await page.locator('#confirmAction').click();
    await page.waitForFunction(() => JSON.parse(localStorage.getItem('nodexlm-chat')).length === 0);
    // Abort before the tiny model can finish: exercise the actual submit/stop handlers.
    await page.locator('#prompt').fill('Once upon a time');
    await page.evaluate(() => {
      document.querySelector('#chatForm').requestSubmit();
      document.querySelector('#stopChat').click();
    });
    await page.waitForFunction(() => !document.querySelector('#sendChat').disabled);
    assert.match(await page.locator('#chatMetrics').textContent(), /interrompida/);
    for (const [name, width, height] of [['desktop',1280,900], ['mobile',390,844]]) {
      await page.setViewportSize({width,height});
      await page.locator('#chatForm').scrollIntoViewIfNeeded();
      const overflow=await page.evaluate(() => [...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1&&getComputedStyle(e).position!=='fixed').map(e=>e.tagName+'#'+e.id+'.'+e.className).slice(0,15));
      await page.screenshot({path: path.join(out, name+'.png'), fullPage: true});
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${name}: horizontal overflow: ${overflow.join(', ')}`);
      await page.screenshot({path: path.join(out, name+'.png'), fullPage: true});
    }
    assert.deepEqual(errors, [], 'Browser JavaScript errors');
    console.log('PASS: real browser login, multi-turn streaming, history, plan, observations, cancellation and responsive layout');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
