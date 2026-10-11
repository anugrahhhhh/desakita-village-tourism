import { expect, test } from "@playwright/test";
import { expectNoA11yViolations, firstPackagePath, login, USERS } from "./helpers";

// A traveler reviews a completed visit (rating chosen by keyboard); the review shows on the
// package page; the admin hides it and it disappears.
test("ulasan dari kunjungan selesai, lalu dimoderasi admin", async ({ page, browser }) => {
  await login(page, USERS.traveler, "/pemesanan");
  await page.getByRole("link", { name: /Menanam Padi/ }).last().click();
  await expect(page.getByText("Selesai").first()).toBeVisible();
  await page.getByRole("link", { name: "Tulis ulasan" }).click();

  // Keyboard: focus the first option (5 stars), move to 4 stars with the arrow key.
  const radios = page.locator("input[name=rating]");
  await radios.first().focus();
  await page.keyboard.press("Space");
  await page.keyboard.press("ArrowRight");
  await expect(radios.nth(1)).toBeChecked();
  await page.fill("#body", "Petani sabar mengajari kami menanam padi. Anak-anak senang sekali!");
  await page.getByRole("button", { name: "Kirim ulasan" }).click();
  await expect(page.getByText("Ulasan Anda sudah tampil")).toBeVisible();
  await expectNoA11yViolations(page, "ulasan terkirim");

  // Public package page: summary and review text.
  const anon = await browser.newContext();
  const pub = await anon.newPage();
  const pkg = await firstPackagePath(pub);
  await pub.goto(pkg);
  const reviews = pub.getByRole("region", { name: "Ulasan wisatawan" });
  await expect(reviews.getByText("Petani sabar mengajari kami")).toBeVisible();
  await expect(reviews.getByText("Sari W.")).toBeVisible();
  await expect(pub.getByText("1 ulasan").first()).toBeVisible();
  await expectNoA11yViolations(pub, "halaman paket dengan ulasan");

  // Admin hides it.
  const adminCtx = await browser.newContext();
  const admin = await adminCtx.newPage();
  await login(admin, USERS.admin, "/admin/ulasan");
  const item = admin.getByRole("listitem").filter({ hasText: "Petani sabar mengajari kami" });
  await item.getByLabel(/Alasan menyembunyikan/).fill("Uji moderasi");
  await item.getByRole("button", { name: "Sembunyikan ulasan" }).click();
  await expect(admin.getByText("Ulasan disembunyikan.")).toBeVisible();

  await pub.reload();
  await expect(pub.getByText("Petani sabar mengajari kami")).toHaveCount(0);
  await anon.close();
  await adminCtx.close();
});
