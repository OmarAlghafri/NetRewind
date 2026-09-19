import type { Page } from "@playwright/test";

/**
 * Loads the app straight into the shell, skipping the first-run wizard and
 * selecting the demo source (the only source that works outside the Tauri
 * shell - see `isTauri()` in `src/data/tauri.ts`). Demo mode is exactly what
 * a browser-driven visual test can exercise: real recorded data (44 events,
 * 16 incidents from `src/demo/*.json`), no live recorder required.
 */
export async function openShell(page: Page, opts: { lang?: "ar" | "en" } = {}): Promise<void> {
  const lang = opts.lang ?? "ar";
  // Setting localStorage before any script runs (via an init script) avoids
  // a flash of the wizard on first paint, which would make the geometry
  // assertions below race the wizard's own layout instead of the shell's.
  await page.addInitScript(
    ([lang]) => {
      window.localStorage.setItem("netrewind.onboardingComplete", "1");
      window.localStorage.setItem("netrewind.lang", lang);
    },
    [lang],
  );
  await page.goto("/");
  await page.waitForSelector(".app-shell, .app-frame", { state: "attached" });
}

/** Navigates to the Timeline page - the layout-matrix reference page because
 *  it renders the demo recording's full 44 events with no virtualization
 *  today, making it the tallest page `.main` can produce. A defect that
 *  only appears once page content exceeds the viewport (the actual shape of
 *  the P0-01 bug - see ADR 0003) will not reproduce on a short page like
 *  Overview, which is why Overview alone gave earlier manual testing a false
 *  negative. */
export async function goToTimeline(page: Page): Promise<void> {
  await page.getByRole("button", { name: /الخط الزمني|Timeline/ }).click();
  await page.waitForSelector(".timeline-row, [data-testid='timeline-row']");
}
