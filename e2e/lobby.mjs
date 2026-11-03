// No public tables: the home page lists none, the socket returns none, and getting in takes the room number.
import { URL, btn, check, confirmCreate, finish, has, launch, open, sit, visit } from "./lib.mjs";
const browser = await launch();
const table = `lobhidden${Date.now()}`;

// the create dialog has no way to list a table
const host = await open(browser);
await visit(host, "ann");
await host.fill("#tablename", table);
await host.click(btn("create"));
check("no public switch", (await host.locator("#public").count()) === 0);
await confirmCreate(host);
await host.waitForSelector("text=Open");
await sit(host, 1);

// someone else sees no list, even asking the socket
const v = await open(browser);
await visit(v, "vic");
check("no table list on the home page", (await v.locator('[aria-label="public tables"], [data-table]').count()) === 0);
const listed = await v.evaluate(
    () =>
        new Promise((resolve) => {
            const ws = new WebSocket(location.origin.replace("http", "ws") + "/poker/ws");
            ws.onopen = () => ws.send(JSON.stringify({ action: "list-tables" }));
            ws.onmessage = (m) => resolve(JSON.parse(m.data));
        })
);
check("the socket lists nothing", listed.action === "tables" && (listed.tables ?? []).length === 0, JSON.stringify(listed));

// the room number gets them in
await v.fill("#tablename", table);
await v.press("#tablename", "Enter");
check("the room number joins", await has(v, "text=Open"));

await browser.close();
finish();
