// Accounts: registering with an invite, roles, renaming, taking over from another device, disabling.
import { accountNamed, adminPost, btn, check, confirmCreate, finish, launch, newInvite, shot, URL } from "./lib.mjs";

const phone = { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true };
const stamp = String(Date.now()).slice(-6);
const browser = await launch();
const fresh = async () => (await browser.newContext(phone)).newPage();
const noScroll = (p) => p.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);

async function registerForm(page, name, password, invite) {
    await page.click("#tab-register");
    await page.fill("#username", name);
    await page.fill("#password", password);
    await page.fill("#invite", invite);
    await page.click("#submit");
}

// a stranger gets nothing
const stranger = await fresh();
const res = await stranger.goto(URL);
check("a stranger lands on the login page", stranger.url().includes("/poker/login"));
check("the login page is ok", res.ok());
check("with no game code", (await stranger.content()).includes("_next") === false);
const blocked = await stranger.context().request.get(new globalThis.URL(URL).origin + "/poker/_next/nothing.js", { maxRedirects: 0 });
check("assets need a login", blocked.status() === 302, String(blocked.status()));
check("the root path is gone", (await stranger.context().request.get(new globalThis.URL(URL).origin + "/")).status() === 404);
check("the login page fits a phone", await noScroll(stranger));
await shot(stranger, "accounts-login-phone");

// a bad invite doesn't register, and a good one only once
const alice = await fresh();
await alice.goto(URL);
const aliceName = "alice" + stamp;
await registerForm(alice, aliceName, "password-1", "NOTACODE");
await alice.waitForSelector("text=邀请码无效");
check("a wrong invite is refused", alice.url().includes("/login"));
const code = await newInvite(browser);
await registerForm(alice, aliceName, "password-1", code);
await alice.waitForSelector("#tablename", { timeout: 10000 });
check("a good invite registers and logs in", !alice.url().includes("/login"));
check("a user has no create button", (await alice.locator(btn("create")).count()) === 0);
check("no name to type", (await alice.locator("#username").count()) === 0);
check("the home page fits a phone", await noScroll(alice));
await shot(alice, "accounts-home-user-phone");

const taken = await fresh();
await taken.goto(URL);
await registerForm(taken, "BOB" + stamp, "password-1", code);
await taken.waitForSelector("text=邀请码无效");
check("an invite only works once", taken.url().includes("/login"));
await registerForm(taken, aliceName.toUpperCase(), "password-1", await newInvite(browser));
await taken.waitForSelector("text=用户名已被占用");
check("a name is taken whatever its case", taken.url().includes("/login"));

// a user can't create, even by asking the socket directly
const forged = await alice.evaluate(
    () =>
        new Promise((resolve) => {
            const ws = new WebSocket(location.origin.replace("http", "ws") + "/poker/ws");
            ws.onopen = () => ws.send(JSON.stringify({ action: "create-table", tablename: "forged", username: "root" }));
            ws.onmessage = (m) => {
                const d = JSON.parse(m.data);
                if (d.action === "error" || d.action === "joined-table") resolve(d.action);
            };
        })
);
check("a user can't create over the socket", forged === "error", forged);

// promoted to admin, alice creates; bob joins by room number and they play a hand
const a = await accountNamed(browser, aliceName);
await adminPost(browser, `/admin/accounts/${a.id}/role`, { role: "admin" });
await alice.reload();
await alice.waitForSelector("#tablename");
const room = "room" + stamp;
await alice.fill("#tablename", room);
await alice.click(btn("create"));
await confirmCreate(alice);
await alice.waitForSelector('text=pick a seat', { timeout: 10000 });

const bobName = "bob" + stamp;
const bob = await fresh();
await bob.goto(URL + "?table=" + room);
await registerForm(bob, bobName, "password-1", await newInvite(browser));
await bob.waitForSelector("#tablename", { timeout: 10000 });
check("a table link survives logging in", await bob.waitForFunction((room) => document.querySelector("#tablename")?.value === room, room, { timeout: 5000 }).then(() => true, () => false));
await bob.click(btn("join"));
await bob.waitForSelector('text=pick a seat', { timeout: 10000 });

for (const [p, seat] of [[alice, 1], [bob, 2]]) {
    await p.click(`button:has-text("${seat}"):has-text("Open"), button:has-text("${seat}"):has-text("+ bot")`);
    await p.click(btn("sit down"));
}
await alice.click(btn("start"));
await alice.waitForSelector(btn("fold"), { timeout: 15000 }).catch(() => {});
await bob.waitForSelector("text=" + aliceName);
check("each sees the other by account name", await alice.locator("text=" + bobName).first().isVisible());
await shot(bob, "accounts-table-phone");

// no renaming at a table
const rename = (p, username) =>
    p.evaluate(
        async (username) =>
            (await fetch("/poker/api/auth/username", { method: "POST", body: JSON.stringify({ username }), headers: { "Content-Type": "application/json" } })).status,
        username
    );
check("no renaming at a table", (await rename(bob, "new" + bobName)) === 409);

// away from the table the name can change, but the table keeps knowing them by the old one
const carolName = "carol" + stamp;
const carol = await fresh();
await carol.goto(URL + "?table=" + room);
await registerForm(carol, carolName, "password-1", await newInvite(browser));
await carol.waitForSelector("#tablename", { timeout: 10000 });
await carol.click(btn("join"));
await carol.waitForSelector("text=" + aliceName);
await carol.click('button:has-text("GoPoker")');
await carol.waitForSelector("#tablename");
const carolNew = "cnew" + stamp;
check("renaming works away from the table", (await rename(carol, carolNew)) === 200);
await carol.goto(URL + "?table=" + room);
await carol.waitForSelector("#tablename");
await carol.click(btn("join"));
await carol.waitForSelector("text=" + aliceName);
const chat = await alice.evaluate(() => document.body.innerText);
check("nobody new appeared under the new name", !chat.includes(carolNew));

// a password in an invite link survives logging in
const pwRoom = "pw" + stamp;
await alice.context().newPage().then(async (host) => {
    await host.goto(URL);
    await host.waitForSelector("#tablename");
    await host.fill("#tablename", pwRoom);
    await host.click(btn("create"));
    await host.fill("#tablePassword", "s3cret");
    await confirmCreate(host);
    await host.waitForSelector("text=pick a seat", { timeout: 10000 });
});
const dan = await fresh();
await dan.goto(`${URL}?table=${pwRoom}#pw=s3cret`);
check("the invite's password stays through the login page", await dan.waitForFunction(() => location.hash === "#pw=s3cret").then(() => true, () => false));
await registerForm(dan, "dan" + stamp, "password-1", await newInvite(browser));
await dan.waitForSelector("#tablename", { timeout: 10000 });
await dan.click(btn("join"));
check("and joins without asking", await dan.waitForSelector("text=pick a seat", { timeout: 10000 }).then(() => true, () => false));

// the same account on another device takes the seat over
const bobPhone2 = await bob.context().newPage();
const cookies = await bob.context().cookies();
const device = await browser.newContext(phone);
await device.addCookies(cookies);
const second = await device.newPage();
await second.goto(URL + "?table=" + room);
await second.click(btn("join"));
await second.waitForSelector("text=" + aliceName, { timeout: 10000 });
check("another device takes the seat", await second.locator("text=" + bobName).first().isVisible());
await bobPhone2.close();

// disabling ends it at once
const b = await accountNamed(browser, bobName);
await adminPost(browser, `/admin/accounts/${b.id}/disabled`, { disabled: true });
await second.waitForURL(/\/poker\/login/, { timeout: 20000 });
check("a disabled account is sent to login", true);
const again = await (await browser.newContext()).request.post(new globalThis.URL(URL).origin + "/poker/api/auth/login", {
    data: { username: bobName, password: "password-1" },
    headers: { Origin: new globalThis.URL(URL).origin },
    failOnStatusCode: false,
});
check("and can't log in", again.status() === 401 || again.status() === 403, String(again.status()));

await browser.close();
finish();
