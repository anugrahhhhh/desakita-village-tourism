import { expect, test, type Page } from "@playwright/test";
import { login, USERS } from "./helpers";

// Records CSP violations and any request that leaves this origin (tiles must come from us).
async function watch(page: Page) {
  const external: string[] = [];
  const csp: string[] = [];
  const app = new URL(test.info().project.use.baseURL!).host;
  page.on("request", (req) => {
    const u = new URL(req.url());
    if (u.protocol.startsWith("http") && u.host !== app) external.push(req.url());
  });
  page.on("console", (msg) => {
    if (msg.type() === "error" && /Content Security Policy/i.test(msg.text())) csp.push(msg.text());
  });
  return { external, csp };
}

test("peta semua desa: penanda, popup, ubin dari server sendiri", async ({ page }) => {
  const seen = await watch(page);
  await page.goto("/peta");
  const map = page.locator("[data-map=list]");
  await expect(map).toBeVisible();
  await expect(page.locator(".leaflet-tile-loaded").first()).toBeVisible();
  const pin = page.getByRole("button", { name: "Desa Wisata Sukamaju" }).or(page.locator("img.leaflet-marker-icon[alt='Desa Wisata Sukamaju']"));
  await expect(pin.first()).toBeVisible();
  await pin.first().click();
  const popup = page.locator(".leaflet-popup-content");
  await expect(popup).toContainText("Desa Wisata Sukamaju");
  await popup.getByRole("link", { name: "Buka halaman desa" }).click();
  await expect(page).toHaveURL(/\/desa\/desa-wisata-sukamaju$/);

  // The village page shows its own map with the same marker.
  await expect(page.locator("[data-map=point] img.leaflet-marker-icon")).toBeVisible();
  await expect(page.getByRole("link", { name: /Petunjuk arah/ })).toHaveAttribute("href", /openstreetmap\.org\/directions\?to=-7\.797068/);

  const tiles = await page.locator("img.leaflet-tile").evaluateAll((imgs) => imgs.map((i) => (i as HTMLImageElement).src));
  expect(tiles.length).toBeGreaterThan(0);
  for (const src of tiles) expect(new URL(src).pathname).toMatch(/^\/tiles\/\d+\/\d+\/\d+\.png$/);
  expect(seen.external, "requests to other origins").toEqual([]);
  expect(seen.csp, "CSP violations").toEqual([]);
});

test("tanpa JavaScript daftar desa tetap tampil dan peta disembunyikan", async ({ browser }) => {
  const ctx = await browser.newContext({ javaScriptEnabled: false });
  const page = await ctx.newPage();
  await page.goto("/peta");
  await expect(page.locator("[data-map=list]")).toBeHidden();
  await expect(page.getByRole("link", { name: "Desa Wisata Sukamaju" })).toBeVisible();
  await ctx.close();
});

test("pengelola memilih lokasi dengan mengeklik peta", async ({ page }) => {
  const seen = await watch(page);
  await login(page, USERS.manager, "/kelola");
  await page.getByRole("link", { name: /Desa Wisata Sukamaju/ }).click();
  await page.goto(page.url() + "/profil");
  const picker = page.locator("[data-map=picker]");
  await expect(picker).toBeVisible();
  await expect(page.locator("#lat")).toHaveValue("-7.797068");

  // Typing coordinates moves the marker; clicking the map writes new ones.
  await page.fill("#lat", "-8.5");
  await page.fill("#lng", "115.2");
  await page.locator("#lng").blur();
  await expect(picker.locator("img.leaflet-marker-icon")).toBeVisible();
  const box = (await picker.boundingBox())!;
  await page.mouse.click(box.x + box.width * 0.3, box.y + box.height * 0.6);
  const lat = Number(await page.inputValue("#lat"));
  const lng = Number(await page.inputValue("#lng"));
  expect(lat).not.toBe(-8.5);
  expect(lat).toBeGreaterThan(-11.5);
  expect(lat).toBeLessThan(6.5);
  expect(lng).toBeGreaterThan(94);
  expect(lng).toBeLessThan(141.5);
  expect(seen.csp, "CSP violations").toEqual([]);
  // Not saved: the shared fixture keeps its original location for other tests.
});
