import { expect, test, type Locator } from "@playwright/test";
import { expectNoA11yViolations, firstPackagePath, login, USERS } from "./helpers";

async function outline(el: Locator) {
  return el.evaluate((node) => {
    const cs = getComputedStyle(node);
    return { style: cs.outlineStyle, width: parseFloat(cs.outlineWidth) };
  });
}

test("tautan lewati navigasi muncul saat difokus dan fokus selalu terlihat", async ({ page }) => {
  await page.goto("/");
  await page.keyboard.press("Tab");
  const skip = page.getByRole("link", { name: "Langsung ke isi halaman" });
  await expect(skip).toBeFocused();
  const box = await skip.boundingBox();
  expect(box!.width).toBeGreaterThan(40);
  expect(box!.height).toBeGreaterThan(20);

  // Every following tab stop shows a visible focus indicator.
  for (let i = 0; i < 8; i++) {
    await page.keyboard.press("Tab");
    const focused = page.locator(":focus");
    const o = await outline(focused);
    const tag = await focused.evaluate((n) => n.tagName.toLowerCase() + (n.textContent ?? "").trim().slice(0, 20));
    expect(o.style, `tab stop ${i + 1}: ${tag}`).not.toBe("none");
    expect(o.width, `tab stop ${i + 1}: ${tag}`).toBeGreaterThanOrEqual(2);
  }
});

test("tanggal di kalender bisa dipilih dengan keyboard", async ({ page }) => {
  const pkg = await firstPackagePath(page);
  await login(page, USERS.traveler, `/pesan/paket/${pkg.split("/").pop()}`);
  const radios = page.locator("input[name=start_date]");
  await radios.first().focus();
  await page.keyboard.press("Space");
  await expect(radios.first()).toBeChecked();
  await page.keyboard.press("ArrowRight");
  await expect(radios.nth(1)).toBeChecked();
  await expect(radios.nth(1)).toBeFocused();
  // The visible focus ring sits on the cell (label) around the visually hidden radio.
  const cell = page.locator("label:has(input[name=start_date])").nth(1);
  expect((await outline(cell)).style).not.toBe("none");
});

test.describe("menu HP", () => {
  test.use({ viewport: { width: 375, height: 740 } });

  test("menu tersembunyi sampai dibuka, bisa dibuka dengan keyboard", async ({ page }) => {
    await page.goto("/");
    const menuLogin = page.locator("details a[href='/masuk']");
    await expect(menuLogin).toBeHidden();
    // Desktop links are not shown on phones either.
    await expect(page.locator("nav > div a[href='/masuk']")).toBeHidden();

    const toggle = page.getByLabel("Buka menu");
    await toggle.focus();
    await page.keyboard.press("Enter");
    await expect(menuLogin).toBeVisible();
    await expectNoA11yViolations(page, "menu HP terbuka");

    await page.keyboard.press("Enter"); // close again
    await expect(menuLogin).toBeHidden();
  });

  test("bar Pesan menempel di bawah layar halaman paket", async ({ page }) => {
    const pkg = await firstPackagePath(page);
    await page.goto(pkg);
    const bar = page.getByRole("link", { name: "Pesan", exact: true });
    await expect(bar).toBeVisible();
    const box = await bar.boundingBox();
    expect(box!.y + box!.height).toBeGreaterThan(740 - 80);
    expect(box!.height).toBeGreaterThanOrEqual(44);
  });

  test("sel kalender di 320 px sekitar 44 px", async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 });
    const pkg = await firstPackagePath(page);
    await login(page, USERS.traveler, `/pesan/paket/${pkg.split("/").pop()}`);
    const cell = page.locator("label:has(input[name=start_date])").first();
    const box = await cell.boundingBox();
    expect(box!.width).toBeGreaterThanOrEqual(42);
    expect(box!.height).toBeGreaterThanOrEqual(44);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBe(0);
  });
});
