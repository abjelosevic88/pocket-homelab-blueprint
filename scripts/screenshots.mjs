// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds "Northwind Shop", scanned twice):
//
//   DATA_DIR=/tmp/bp-demo DEMO_DATA=true ADMIN_EMAIL=demo@example.com ADMIN_PASSWORD=demodemo1 ADMIN_NAME=Maya PORT=18306 ./pocket-blueprint
//   CDP=http://127.0.0.1:19271 BASE=http://127.0.0.1:18306 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                  # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18306'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'demo@example.com', password: 'demodemo1' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path, wait = 1500, still = true) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  // headless Chrome freezes CSS fade-ins: show everything at once
  if (still) await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
const has = async (page, text, what) => assert.ok((await page.locator('body').innerText()).includes(text), what)

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/')
  await has(page, 'Northwind Shop', 'the demo codebase is listed')
  await shot(page, 'desktop-codebases')
  await go(page, '/c/1')
  await has(page, 'Worth a look', 'the overview shows what to look at')
  await shot(page, 'desktop-overview')
  await go(page, '/c/1/graph?depth=2', 2500)
  assert.ok(await page.locator('.graph-canvas canvas').count() > 0, 'the graph draws')
  await shot(page, 'desktop-graph')
  await go(page, '/c/1/structure?path=internal/store/orders.go', 1500)
  await has(page, 'CreateOrder', 'the file panel lists symbols')
  await shot(page, 'desktop-structure')
  await go(page, '/c/1/api', 1200)
  await has(page, '/api/v1/orders/{id}/pay', 'endpoints from chi routes')
  await shot(page, 'desktop-api')
  await go(page, '/c/1/data', 1200)
  await has(page, 'coupon_id', 'tables with columns from both migrations')
  await shot(page, 'desktop-data')
  await go(page, '/c/1/hotspots', 1000)
  await shot(page, 'desktop-hotspots')
  await go(page, '/c/1/scans', 1500)
  await has(page, 'What changed', 'two scans compare')
  await shot(page, 'desktop-scans')
  await go(page, '/c/1/structure?src=internal/billing/stripe.go&line=34', 1500)
  await shot(page, 'desktop-source')
  for (const p of ['/c/1/config', '/c/1/data', '/settings/general', '/settings/apps', '/settings/data', '/settings/about']) await go(page, p, 500)
  await ctx.close()
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true, dark: true })
  await go(page, '/c/1')
  await shot(page, 'phone-overview')
  await go(page, '/c/1/api')
  await shot(page, 'phone-api')
  await go(page, '/c/1/graph', 2500)
  await shot(page, 'phone-graph')
  await ctx.close()
}

await browser.close()
if (errors.length) {
  console.error('browser errors:\n' + errors.join('\n'))
  process.exit(1)
}
console.log('ok')
