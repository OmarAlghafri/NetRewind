import { defineConfig, devices } from "@playwright/test";

// Uses the system-installed Chrome (`channel: "chrome"`) rather than a
// Playwright-managed browser download: this project's build/dev machines
// are not guaranteed direct network access to Playwright's CDN, and Chrome
// is already a dependency-free constant on both the Windows dev machine and
// any CI runner that also needs Chrome for other tooling. Ubuntu CI installs
// google-chrome-stable explicitly for the same reason (see
// .github/workflows/ci.yml's desktop-visual job).
export default defineConfig({
  testDir: "./tests/visual",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"], ["html", { outputFolder: "playwright-report", open: "never" }]],
  outputDir: "test-results",
  use: {
    baseURL: "http://localhost:1420",
    channel: "chrome",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], channel: "chrome" },
    },
  ],
  webServer: {
    command: "npm run dev",
    url: "http://localhost:1420",
    reuseExistingServer: !process.env.CI,
    timeout: 30_000,
  },
});
