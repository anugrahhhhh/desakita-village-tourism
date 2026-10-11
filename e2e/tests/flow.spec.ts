import { expect, test } from "@playwright/test";
import { login, USERS } from "./helpers";

// Main journey: search → pick a date → send request → manager confirms → traveler cancels.
test("cari, pilih tanggal, kirim permintaan, dikonfirmasi, lalu dibatalkan", async ({ browser }) => {
  const travelerCtx = await browser.newContext();
  const page = await travelerCtx.newPage();

  // Search from the hero box.
  await page.goto("/");
  await page.fill("#q-hero", "padi");
  await page.locator("form[role=search] button[type=submit]").click();
  await expect(page).toHaveURL(/q=padi/);
  await expect(page.getByRole("link", { name: /Belajar Membatik/ })).toHaveCount(0);
  await page.getByRole("link", { name: /Menanam Padi/ }).first().click();

  // Package page → booking form; anonymous visitors are asked to log in first.
  await expect(page.getByRole("heading", { level: 1, name: /Menanam Padi/ })).toBeVisible();
  await page.getByRole("link", { name: "Pilih tanggal dan pesan" }).click();
  await page.getByRole("link", { name: "Masuk untuk memesan" }).click();
  await page.fill("#email", USERS.traveler);
  await page.fill("#password", "rahasia123");
  await page.locator("form[action='/masuk'] button[type=submit]").click();
  await expect(page).toHaveURL(/\/pesan\/paket\/\d+$/);

  // Pick the first available date; the whole cell is the tap target.
  const firstDay = page.locator("label:has(input[name=start_date])").first();
  await firstDay.click();
  await expect(firstDay.locator("input")).toBeChecked();
  await page.fill("#party_size", "2");
  await expect(page.locator("[data-total-for=party_size]")).toHaveText("Rp 300.000");
  await page.getByRole("button", { name: "Kirim permintaan" }).click();

  await expect(page).toHaveURL(/\/pemesanan\/\d+$/);
  await expect(page.getByText("Menunggu konfirmasi").first()).toBeVisible();
  const bookingURL = page.url();

  // The manager sees it on the village overview (HTMX panel) and confirms.
  const managerCtx = await browser.newContext();
  const mpage = await managerCtx.newPage();
  await login(mpage, USERS.manager, "/kelola");
  await mpage.getByRole("link", { name: /Desa Wisata Sukamaju/ }).click();
  const pending = mpage.getByRole("link", { name: /Sari Wisatawan/ }).first();
  await expect(pending).toBeVisible();
  await pending.click();
  await mpage.getByRole("button", { name: "Konfirmasi" }).click();
  await expect(mpage.getByText("Dikonfirmasi").first()).toBeVisible();
  await expect(mpage.getByRole("link", { name: "Kabari via WhatsApp" })).toHaveAttribute("href", /^https:\/\/wa\.me\/62813/);

  // The traveler sees the confirmation and cancels; the cancel button disappears.
  await page.goto(bookingURL);
  await expect(page.getByText("Dikonfirmasi").first()).toBeVisible();
  await page.getByRole("button", { name: "Batalkan pemesanan" }).click();
  await expect(page.getByText("Dibatalkan").first()).toBeVisible();
  await expect(page.getByRole("button", { name: "Batalkan pemesanan" })).toHaveCount(0);

  await travelerCtx.close();
  await managerCtx.close();
});
