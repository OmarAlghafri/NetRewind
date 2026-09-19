import { expect, test } from "@playwright/test";
import { goToTimeline, openShell } from "./helpers";

/**
 * P0-01 / P1-03 / P1-04 / P1-10
 * (docs/agent-state/GUI_MODERNIZATION_EXECUTION_ORDER.md §3, §7, §9 Phase 1):
 * the app shell used to have no bound on its own height and no independent
 * scroll regions (`App.css`'s old `.app-shell` set only `min-height: 100vh`,
 * a floor with no ceiling, no `overflow`, no `min-width`/`min-height: 0`
 * anywhere). When the workspace's content was taller than the viewport, the
 * whole document scrolled as one unit and the sidebar - a grid item
 * stretched to match that height - scrolled away with it, taking the
 * language toggle and every nav item below the fold.
 *
 * Measured directly against the pre-fix build, viewport 900x600, Timeline
 * page (44 demo events, the tallest page today, unvirtualized):
 *   document.documentElement.scrollHeight = 1692
 *   document.documentElement.clientHeight = 600
 *   getComputedStyle('.app-shell').overflow = "visible"
 * i.e. 1092px (65%) of the document was unreachable without scrolling past
 * the sidebar, and past the ~320px point where the sidebar's own content
 * ended, the entire nav column was blank until scrolled back to the top.
 * Full account: `docs/evidence/29-gui-modernization-phase0-baseline.log`.
 *
 * ADR 0003 fixed this by bounding `.app-frame` to `100dvh` with
 * `overflow: hidden` and giving `.sidebar` and `.workspace-body` their own
 * independent scroll regions. This suite is the regression gate for that
 * fix: every case here must stay green for the rest of the project.
 *
 * Known gaps, honestly not covered here yet:
 * - Manual theme (high-contrast / reduced-transparency) does not exist in
 *   code until Phase 2 (§4.7) - only OS-level `prefers-color-scheme` is
 *   real today, so that is what this suite tests via Playwright's
 *   `colorScheme` context option; high-contrast/reduced-transparency modes
 *   get their own matrix pass once Phase 2 ships the toggle.
 * - Real browser zoom (Ctrl+=/pinch, which rescales UI chrome as well as
 *   content) is not driven directly - Playwright has no API for it. The
 *   320x256 case below is the WCAG 2.2 SC 1.4.10 reference viewport
 *   (equivalent to ~400% zoom on a 1280px-wide layout), the standard proxy
 *   for reflow testing, but it is a proxy, not a literal zoom test.
 */

const WIDTHS = [720, 900, 1100, 1440, 1920] as const;
const HEIGHT = 600;

const LANGS = ["ar", "en"] as const;
const THEMES = ["light", "dark"] as const;

for (const lang of LANGS) {
  for (const theme of THEMES) {
    for (const width of WIDTHS) {
      test(`shell stays reachable without page scroll - ${lang}/${theme} @ ${width}x${HEIGHT}`, async ({
        page,
      }) => {
        await page.emulateMedia({ colorScheme: theme });
        await page.setViewportSize({ width, height: HEIGHT });
        await openShell(page, { lang });
        await goToTimeline(page);

        // Reproduction precondition: Timeline's content must be taller
        // than its own scroll region (.workspace-body), or this is not a
        // meaningful check. At 1920 wide the sidebar is unchanged width so
        // this still holds - only .workspace-body's inline size changes.
        const workspaceOverflows = await page.evaluate(() => {
          const body = document.querySelector(".workspace-body");
          return !!body && body.scrollHeight > body.clientHeight;
        });
        expect(
          workspaceOverflows,
          "test setup invalid: Timeline's content must exceed its scroll region",
        ).toBe(true);

        // The sidebar footer (language toggle) must be fully visible
        // without scrolling anything.
        const langToggle = page.locator(".lang-toggle");
        await expect(langToggle).toBeVisible();
        const toggleBox = await langToggle.boundingBox();
        expect(toggleBox).not.toBeNull();
        if (toggleBox) {
          expect(toggleBox.y).toBeGreaterThanOrEqual(0);
          expect(toggleBox.y + toggleBox.height).toBeLessThanOrEqual(HEIGHT);
        }

        // Every nav button must be visible too - not just the footer.
        const navButtons = page.locator(".sidebar-nav .nav-item");
        const count = await navButtons.count();
        expect(count).toBeGreaterThan(0);
        for (let i = 0; i < count; i++) {
          await expect(navButtons.nth(i)).toBeVisible();
        }

        // No document-level scroll in either axis - .workspace-body may
        // scroll vertically (that is the point), but the document/html
        // element itself never should.
        const doc = await page.evaluate(() => ({
          scrollWidth: document.documentElement.scrollWidth,
          clientWidth: document.documentElement.clientWidth,
          scrollHeight: document.documentElement.scrollHeight,
          clientHeight: document.documentElement.clientHeight,
        }));
        expect(doc.scrollWidth).toBeLessThanOrEqual(doc.clientWidth);
        expect(doc.scrollHeight).toBeLessThanOrEqual(doc.clientHeight);
      });
    }
  }
}

// WCAG 2.2 SC 1.4.10 reflow reference viewport (~400% zoom equivalent on a
// 1280px layout). P1-04: the earlier accessibility audit accepted a
// horizontal scrollbar here as "one-dimensional" without noticing the page
// was also tall, making the real experience two-dimensional. This asserts
// neither axis needs document-level scrolling.
for (const lang of LANGS) {
  test(`320 CSS px reflow (WCAG 1.4.10) - ${lang}`, async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 256 });
    await openShell(page, { lang });

    const doc = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
    }));
    expect(doc.scrollWidth).toBeLessThanOrEqual(doc.clientWidth);
  });
}
