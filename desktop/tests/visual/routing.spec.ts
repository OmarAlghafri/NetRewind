import { expect, test } from "@playwright/test";
import { openShell } from "./helpers";

/**
 * Real routing (desktop/src/routing/useRoute.ts) replaced local
 * useState<Page> in App.tsx: navigation is now a URL (#/<page>?...),
 * which is what makes a deep link, a bookmark, or the browser's own
 * back/forward button work at all - none of them did anything before this,
 * because there was nothing in the URL to go back *to*.
 */
test("navigating the sidebar updates the URL hash", async ({ page }) => {
  await openShell(page, { lang: "en" });
  // An empty hash resolves to Overview internally (parseRoute's default)
  // without navigate() ever being called to write it - no redundant
  // history entry for a page that was never actually navigated *to*.
  // The hash only appears once real navigation happens, asserted below.
  expect(page.url()).not.toContain("#");

  await page.getByRole("button", { name: "Timeline" }).click();
  await expect(page).toHaveURL(/#\/timeline$/);

  await page.getByRole("button", { name: "Incidents" }).click();
  await expect(page).toHaveURL(/#\/incidents$/);
});

test("opening a deep link lands directly on that page, not Overview first", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.setItem("netrewind.onboardingComplete", "1");
    window.localStorage.setItem("netrewind.lang", "en");
  });
  await page.goto("/#/rules");
  await page.waitForSelector(".app-frame");
  // The active nav item must reflect the URL that was opened, not the
  // default - a real deep link, not just a hash the app ignores.
  await expect(page.getByRole("button", { name: "Rules", exact: true })).toHaveClass(/active/);
});

test("browser back/forward moves between previously visited pages", async ({ page }) => {
  await openShell(page, { lang: "en" });
  await page.getByRole("button", { name: "Timeline" }).click();
  await expect(page).toHaveURL(/#\/timeline$/);
  await page.getByRole("button", { name: "Settings" }).click();
  await expect(page).toHaveURL(/#\/settings$/);

  await page.goBack();
  await expect(page).toHaveURL(/#\/timeline$/);
  await expect(page.getByRole("button", { name: "Timeline" })).toHaveClass(/active/);

  await page.goForward();
  await expect(page).toHaveURL(/#\/settings$/);
});
