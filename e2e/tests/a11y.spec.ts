import { test } from "@playwright/test";
import { expectNoA11yViolations, firstPackagePath, login, USERS } from "./helpers";

const viewports = [
  { name: "hp", width: 375, height: 740 },
  { name: "desktop", width: 1280, height: 900 },
];

for (const vp of viewports) {
  test.describe(`aksesibilitas (${vp.name})`, () => {
    test.use({ viewport: { width: vp.width, height: vp.height } });

    test("halaman publik", async ({ page }) => {
      const pkg = await firstPackagePath(page);
      for (const path of ["/", "/?q=padi", "/desa/desa-wisata-sukamaju", pkg, `/pesan/paket/${pkg.split("/").pop()}`, "/masuk", "/daftar", "/tidak-ada"]) {
        await page.goto(path);
        await expectNoA11yViolations(page, `${path} @${vp.name}`);
      }
    });

    test("halaman wisatawan", async ({ page }) => {
      const pkg = await firstPackagePath(page, "Belajar Membatik");
      const bookPath = `/pesan/paket/${pkg.split("/").pop()}`;
      await login(page, USERS.traveler, bookPath);
      await page.locator("label:has(input[name=start_date])").first().click();
      await expectNoA11yViolations(page, `${bookPath} (tanggal dipilih) @${vp.name}`);
      // The completed visit's review form (not yet submitted at this point of the run).
      await page.goto("/pemesanan");
      await page.getByRole("link", { name: /Menanam Padi/ }).last().click();
      await page.getByRole("link", { name: "Tulis ulasan" }).click();
      await expectNoA11yViolations(page, `form ulasan @${vp.name}`);
      for (const path of ["/pemesanan", "/akun"]) {
        await page.goto(path);
        await expectNoA11yViolations(page, `${path} @${vp.name}`);
      }
    });

    test("halaman pengelola dan admin", async ({ page }) => {
      await login(page, USERS.manager, "/kelola");
      await expectNoA11yViolations(page, `/kelola @${vp.name}`);
      await page.getByRole("link", { name: /Desa Wisata Sukamaju/ }).click();
      await page.waitForLoadState("networkidle"); // HTMX bookings panel
      await expectNoA11yViolations(page, `ringkasan desa @${vp.name}`);
      const overview = page.url();
      for (const sub of ["/pemesanan", "/paket", "/profil", "/foto"]) {
        await page.goto(overview + sub);
        await expectNoA11yViolations(page, `${sub} @${vp.name}`);
      }
      await page.goto(overview + "/paket");
      await page.getByRole("link", { name: "Kalender" }).first().click();
      await expectNoA11yViolations(page, `kalender @${vp.name}`);

      const adminCtx = await page.context().browser()!.newContext({ viewport: { width: vp.width, height: vp.height } });
      const admin = await adminCtx.newPage();
      await login(admin, USERS.admin, "/admin");
      await expectNoA11yViolations(admin, `/admin @${vp.name}`);
      for (const path of ["/admin/laporan", "/admin/laporan?status=resolved", "/admin/desa", "/admin/audit", "/admin/ulasan", "/admin/ulasan?status=hidden"]) {
        await admin.goto(path);
        await expectNoA11yViolations(admin, `${path} @${vp.name}`);
      }
      await admin.goto("/admin/desa?status=verified");
      await admin.getByRole("link", { name: /Desa Wisata Sukamaju/ }).click();
      await expectNoA11yViolations(admin, `detail desa admin @${vp.name}`);
      await adminCtx.close();
    });
  });
}
