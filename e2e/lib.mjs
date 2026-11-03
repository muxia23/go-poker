// Shared helpers for the e2e scripts.
import { chromium } from "playwright-core";

export const URL = process.env.URL || "http://localhost:8099/poker";
export const ROOT_USER = process.env.ROOT_USERNAME || "root";
export const ROOT_PASSWORD = process.env.ROOT_PASSWORD || "e2e-root-password";
const TEST_PASSWORD = "e2e-password";

// btn selects a button by its exact text. :text-is only sees an element's own text, and a
// Mantine button keeps its label in a span, so it also matches a button holding one.
export const btn = (text) => `button:is(:text-is("${text}"), :has(:text-is("${text}")))`;

// launch uses CHROMIUM_PATH if set, else playwright's own chromium
export function launch() {
    const exe = process.env.CHROMIUM_PATH;
    return chromium.launch(exe ? { executablePath: exe } : {});
}

let passed = 0;
let failed = 0;

export function check(name, ok, detail = "") {
    if (ok) passed++;
    else failed++;
    console.log(`${ok ? "PASS" : "FAIL"} ${name}${detail ? " — " + detail : ""}`);
}

// finish prints the tally and exits nonzero on any failure
export function finish() {
    console.log(`\n${passed}/${passed + failed} passed`);
    process.exit(failed ? 1 : 0);
}

// shot saves a screenshot when SHOTS names a directory
export async function shot(page, name) {
    if (process.env.SHOTS) await page.screenshot({ path: `${process.env.SHOTS}/${name}.png` });
}

// open makes a page in a new browser context, so it's a separate person. The system is
// dark, and table animations are off so checks see where things end up, unless the
// context says otherwise.
export async function open(browser, context = {}) {
    return (await browser.newContext({ colorScheme: "dark", reducedMotion: "reduce", ...context })).newPage();
}

const api = (path) => `${new globalThis.URL(URL).origin}/poker/api${path}`;
const posting = (data) => ({ data, headers: { Origin: new globalThis.URL(URL).origin }, failOnStatusCode: false });

let rootSession;

// setRootPassword does root's first login once, before scripts run at the same
// time: setting a password ends root's other sessions.
export async function setRootPassword(browser) {
    await asRoot(browser);
    rootSession = undefined;
}

// asRoot is a browser context logged in as root, who's made to set a password
// on the first login, which then stays the one in ROOT_PASSWORD.
export async function asRoot(browser) {
    if (rootSession) return rootSession;
    const ctx = await browser.newContext();
    const r = await ctx.request.post(api("/auth/login"), posting({ username: ROOT_USER, password: ROOT_PASSWORD }));
    if (!r.ok()) throw new Error(`root login: ${r.status()} ${await r.text()}`);
    if ((await r.json()).account.mustChange) {
        const c = await ctx.request.post(api("/auth/password"), posting({ password: ROOT_PASSWORD }));
        if (!c.ok()) throw new Error(`root password: ${c.status()} ${await c.text()}`);
    }
    rootSession = ctx;
    return ctx;
}

export async function newInvite(browser) {
    const root = await asRoot(browser);
    return (await (await root.request.post(api("/admin/invites"), posting({}))).json()).code;
}

export async function accountNamed(browser, name) {
    const root = await asRoot(browser);
    const { accounts } = await (await root.request.get(api("/admin/accounts"))).json();
    return accounts.find((a) => a.username.toLowerCase() === name.toLowerCase());
}

export async function adminPost(browser, path, data = {}) {
    const root = await asRoot(browser);
    return root.request.post(api(path), posting(data));
}

// signIn logs page's context in as name, registering them the first time. They
// can create tables, as most scripts need; accounts.mjs tests the roles.
export async function signIn(page, name) {
    const ctx = page.context();
    if ((await ctx.request.get(api("/auth/me"), { failOnStatusCode: false })).ok()) return;
    const browser = ctx.browser();
    let r = await ctx.request.post(api("/auth/login"), posting({ username: name, password: TEST_PASSWORD }));
    if (r.ok()) return;
    r = await ctx.request.post(api("/auth/register"), posting({ username: name, password: TEST_PASSWORD, invite: await newInvite(browser) }));
    if (r.status() === 409) {
        // another script running alongside just registered this name
        r = await ctx.request.post(api("/auth/login"), posting({ username: name, password: TEST_PASSWORD }));
        if (r.ok()) return;
    }
    if (!r.ok()) throw new Error(`register ${name}: ${r.status()} ${await r.text()}`);
    const account = await accountNamed(browser, name);
    await adminPost(browser, `/admin/accounts/${account.id}/role`, { role: "admin" });
}

// visit logs page in as name and opens url at the join form, ready to join: the table from an
// invite link is filled in and the socket is up.
export async function visit(page, name, url = URL) {
    await signIn(page, name);
    await page.goto(url);
    await page.waitForSelector("#tablename");
    const table = new globalThis.URL(url).searchParams.get("table");
    if (table) await until(async () => (await page.inputValue("#tablename")) === table);
    if (table) await page.waitForSelector(`${btn("join")}:not([disabled])`);
}

// confirmCreate creates the table from the settings dialog
export async function confirmCreate(page) {
    await page.click(btn("create table"));
}

// enter fills in the join form and joins table, or creates it when setup is
// set: setup(page) adjusts the settings dialog first, true keeps the defaults.
// The page goes to the home page first unless it's already on one.
async function enter(page, name, table, setup) {
    await signIn(page, name);
    if (page.url() === "about:blank") await page.goto(URL);
    await page.fill("#tablename", table);
    await page.click(btn(setup ? "create" : "join"));
    if (typeof setup === "function") {
        await openRules(page);
        await setup(page);
    }
    if (setup) await confirmCreate(page);
}

// openRules opens the settings dialog's closed sections (pace, house rules, bomb
// pots), so their controls can be clicked
export async function openRules(page) {
    await page.waitForSelector("[role=dialog] [data-accordion-control]");
    const closed = page.locator('[role=dialog] [data-accordion-control][aria-expanded="false"]');
    while (await closed.count()) await closed.first().click();
}

// quick sets the shortest pause between hands in the create dialog, for scripts
// that play several. Not for ones that act between hands.
export const quick = (page) => page.click('fieldset:has(input[name="nextHandDelay"]) >> text="2s"');

// grace is the server's reconnect grace in ms, from RECONNECT_GRACE (as just
// e2e sets it) or the default
export const grace = (() => {
    const [, n, unit] = (process.env.RECONNECT_GRACE || "60s").match(/^(\d+)(ms|s|m)$/);
    return Number(n) * { ms: 1, s: 1000, m: 60000 }[unit];
})();

// create makes table as name and waits for the seats
export async function create(page, name, table, setup = true) {
    await enter(page, name, table, setup);
    await page.waitForSelector("text=Open");
}

// join joins table as name, watching, and waits for the seats
export async function join(page, name, table) {
    await enter(page, name, table, false);
    await page.waitForSelector("text=Open");
}

// sit takes the open seat with the default buy-in
export async function sit(page, seat) {
    await page.locator("button", { hasText: "Open" }).filter({ hasText: String(seat) }).click();
    await page.locator("form:has(#buyIn) button[type=submit]").click();
    await page.waitForSelector("#buyIn", { state: "detached" });
}

// seat creates table as the first player and seats everyone, in seats 1, 2, 3…
// unless they say otherwise, waiting for the server to seat each. A player is a
// name or { name, seat, context }. setup adjusts the create dialog. Returns the
// players' pages.
export async function seat(browser, table, players, setup = true) {
    const pages = [];
    for (const [i, player] of players.entries()) {
        const { name, seat = i + 1, context } = typeof player === "string" ? { name: player } : player;
        const page = await open(browser, context);
        if (i === 0) await create(page, name, table, setup);
        else await join(page, name, table);
        await sit(page, seat);
        await page.waitForSelector(`p:text-is("${name}")`);
        pages.push(page);
    }
    return pages;
}

// until polls cond, which may be async, until it's true or ms pass
export async function until(cond, ms = 3000) {
    const end = Date.now() + ms;
    for (;;) {
        if (await cond()) return true;
        if (Date.now() > end) return false;
        await new Promise((r) => setTimeout(r, 100));
    }
}

// has reports whether selector shows up on page within ms
export const has = (page, selector, ms = 3000) => page.waitForSelector(selector, { timeout: ms }).then(() => true, () => false);

// gone reports whether selector leaves page within ms
export const gone = (page, selector, ms = 3000) => page.waitForSelector(selector, { state: "detached", timeout: ms }).then(() => true, () => false);

// quiet waits ms, for checks that something doesn't happen
export const quiet = (ms) => new Promise((r) => setTimeout(r, ms));

// act clicks selector on whichever page has it, retrying until a click lands:
// the turn can move on between finding a button and clicking it
export function act(pages, selector, ms = 5000) {
    return until(async () => {
        for (const p of pages) {
            if (await p.click(selector, { timeout: 300 }).then(() => true, () => false)) return true;
        }
        return false;
    }, ms);
}

// fold folds for whoever is to act
export const fold = (pages) => act(pages, `${btn("fold")}:enabled`, 15000);

// checkDown checks or calls for whoever is to act until done(), which may be
// async: the showdown, say, or the next hand
export function checkDown(pages, done, ms = 15000) {
    const text = ':text-matches("^(check|call \\\\d+|all in \\\\d+)$")';
    const call = `button:is(${text}, :has(${text})):enabled`;
    return until(async () => {
        if (await done()) return true;
        for (const p of pages) await p.click(call, { timeout: 200 }).catch(() => {});
        return done();
    }, ms);
}

// record keeps what a page is sent: every view, log line and finished hand
// ({record, stats}), and any errors. Call it before the page connects.
export function record(page) {
    const rec = { views: [], logs: [], hands: [], errors: [], welcomes: 0 };
    page.on("websocket", (ws) => {
        ws.on("framereceived", ({ payload }) => {
            try {
                const m = JSON.parse(payload);
                if (m.action === "welcome") rec.welcomes++;
                if (m.action === "welcome" || m.action === "table-update") rec.views.push(m.view);
                if (m.action === "error") rec.errors.push(m.message);
                for (const e of m.events ?? []) {
                    if (e.type === "log") rec.logs.push(e.data.log.message);
                    if (e.type === "hand-done") rec.hands.push(e.data);
                }
            } catch {}
        });
    });
    return rec;
}

// last is the last view rec got
export const last = (rec) => rec.views.at(-1);

// panel opens tab in the table panel, opening the panel if it isn't
export async function panel(page, tab) {
    if (!(await page.locator('[aria-label="table"]').count())) await page.click('header button:has-text("menu")');
    await page.click(`[aria-label="table"] ${btn(tab)}`);
}

// seatMenu picks item from your seat's menu
export async function seatMenu(page, item) {
    await page.click('button[title="seat options"]');
    await page.click(btn(item));
}

// playerRow is the text of name's line in the open players tab
export const playerRow = (page, name) =>
    page.locator('ul[aria-label="players"] li').filter({ has: page.locator(`span.font-semibold:text-is("${name}")`) }).innerText().catch(() => "");
