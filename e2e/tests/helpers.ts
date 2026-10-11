import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

export const PASSWORD = "rahasia123";
export const USERS = {
  traveler: "sari@e2e.test",
  manager: "pengelola@e2e.test",
  admin: "admin@e2e.test",
} as const;

/** Logs in through the real form; `next` is the page to return to. */
export async function login(page: Page, email: string, next = "/") {
  await page.goto(`/masuk?next=${encodeURIComponent(next)}`);
  await page.fill("#email", email);
  await page.fill("#password", PASSWORD);
  await Promise.all([
    page.waitForURL((u) => !u.pathname.startsWith("/masuk")),
    page.locator("form[action='/masuk'] button[type=submit]").click(),
  ]);
}

/** Returns the path of the first package card on the home page. */
export async function firstPackagePath(page: Page, name = "Menanam Padi") {
  await page.goto("/");
  const href = await page.getByRole("link", { name: new RegExp(name) }).first().getAttribute("href");
  expect(href).toBeTruthy();
  return href!;
}

/** Fails the test with a readable list of WCAG 2.1 A/AA violations. */
export async function expectNoA11yViolations(page: Page, label: string) {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"])
    .analyze();
  const summary = results.violations.map(
    (v) => `${v.id} (${v.impact}): ${v.help}\n    ${v.nodes.map((n) => n.target.join(" ")).slice(0, 5).join("\n    ")}`,
  );
  expect(summary, `axe violations on ${label}`).toEqual([]);
}
