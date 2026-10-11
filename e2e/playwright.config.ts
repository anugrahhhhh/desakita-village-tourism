import { defineConfig } from "@playwright/test";

// The fixture (e2e/fixture) recreates the desakita_e2e database with known demo data and
// serves the real app. Tests share that state, so they run serially.
const port = process.env.E2E_PORT ?? "18095";

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    // Locally: PW_CHANNEL=msedge (or chrome) uses an installed browser; CI installs chromium.
    channel: process.env.PW_CHANNEL || undefined,
    locale: "id-ID",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "go run ./e2e/fixture",
    cwd: "..",
    url: `http://127.0.0.1:${port}/healthz`,
    env: { E2E_ADDR: `127.0.0.1:${port}` },
    reuseExistingServer: false,
    timeout: 180_000,
    stdout: "pipe",
  },
});
