import { expect, test } from "@playwright/test";
import { login, USERS } from "./helpers";

// Admin verifies a queued village; the decision appears in the audit log and the village
// becomes public.
test("admin memverifikasi desa dan tindakannya tercatat di log audit", async ({ page, browser }) => {
  await login(page, USERS.admin, "/admin");
  await expect(page.getByRole("heading", { name: "Antrean verifikasi" })).toBeVisible();
  await page.getByRole("link", { name: /Desa Wisata Menunggu/ }).click();

  // Rejecting needs a reason.
  await page.getByRole("button", { name: "Tolak pendaftaran" }).click();
  await expect(page.getByText("Alasan wajib diisi")).toBeVisible();

  await page.getByRole("button", { name: "Verifikasi desa" }).click();
  await expect(page.getByText("Desa terverifikasi dan kini tampil")).toBeVisible();
  const history = page.getByRole("region", { name: "Riwayat tindakan admin" });
  await expect(history.getByText("Desa diverifikasi")).toBeVisible();
  await expect(history.getByText("Admin DesaKita")).toBeVisible();

  await page.getByRole("link", { name: "Log audit" }).click();
  await page.selectOption("#aksi", "village.verify");
  await page.getByRole("button", { name: "Terapkan" }).click();
  await expect(page).toHaveURL(/aksi=village\.verify/);
  await expect(page.locator("ol").getByText("Desa diverifikasi").first()).toBeVisible();

  // Public now.
  const anon = await browser.newContext();
  const pub = await anon.newPage();
  const resp = await pub.goto("/desa/desa-wisata-menunggu");
  expect(resp?.status()).toBe(200);
  await anon.close();
});
