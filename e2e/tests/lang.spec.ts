import { expect, test } from "@playwright/test";
import { expectNoA11yViolations, login, USERS } from "./helpers";

test("pemilih bahasa di footer mengganti seluruh antarmuka", async ({ page }) => {
  await page.goto("/peta");
  await expect(page.locator("html")).toHaveAttribute("lang", "id");
  await page.getByRole("button", { name: "English" }).click();
  await expect(page).toHaveURL(/\/peta$/);
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Tourism village map");
  await expect(page.getByRole("link", { name: "Map", exact: true }).first()).toBeVisible();

  // The choice sticks across pages and Leaflet controls follow it.
  await page.goto("/desa/desa-wisata-sukamaju");
  await expect(page.getByRole("button", { name: "Zoom in" }).or(page.getByTitle("Zoom in")).first()).toBeVisible();
  for (const path of ["/", "/peta", "/desa/desa-wisata-sukamaju", "/masuk"]) {
    await page.goto(path);
    await expectNoA11yViolations(page, `${path} (en)`);
  }

  await page.getByRole("button", { name: "Bahasa Indonesia" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "id");
});

test("statistik pengelola dalam bahasa Inggris", async ({ page }) => {
  await login(page, USERS.manager, "/kelola");
  await page.getByRole("link", { name: /Desa Wisata Sukamaju/ }).click();
  await page.getByRole("link", { name: /Statistik/ }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Statistik");
  await page.getByRole("link", { name: "30 hari terakhir" }).click();
  await expect(page.getByRole("link", { name: "30 hari terakhir" })).toHaveAttribute("aria-current", "page");
  await page.getByRole("button", { name: "English" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Statistics");
  await expect(page.getByRole("link", { name: "Last 30 days" })).toHaveAttribute("aria-current", "page");
  await expectNoA11yViolations(page, "statistics (en)");
  await page.getByRole("button", { name: "Bahasa Indonesia" }).click();
});
