import { expect, test } from "@playwright/test";
import { goToTimeline, openShell } from "./helpers";

/**
 * P0-01 (docs/agent-state/GUI_MODERNIZATION_EXECUTION_ORDER.md §3): the app
 * shell has no bound on its own height and no independent scroll regions
 * (`App.css:82-86` - `.app-shell` uses `min-height: 100vh` with no
 * `overflow`, no `position: sticky`, no `min-width`/`min-height: 0`
 * anywhere in the file). When the workspace's content is taller than the
 * viewport, the whole document scrolls as one unit and the sidebar - which
 * is a grid item stretched to match that height - scrolls away with it,
 * taking the language toggle and every nav item below the fold.
 *
 * Measured directly against this exact build, viewport 900x600, Timeline
 * page (44 demo events, the tallest page today):
 *   document.documentElement.scrollHeight = 1692
 *   document.documentElement.clientHeight = 600
 *   getComputedStyle('.app-shell').overflow = "visible"
 * i.e. 1092px (65%) of the document is unreachable without scrolling past
 * the sidebar, and past the ~320px point where the sidebar's own content
 * ends, the entire nav column is blank until the page is scrolled back to
 * the top.
 *
 * This suite is the Phase 0 baseline reproduction. It MUST fail against
 * today's shell and MUST pass once Phase 1 (ADR 0003) lands. Phase 1 also
 * expands this into the full size/language/theme/zoom matrix required by
 * the execution order's §7 - this file is that matrix's foundation, not a
 * throwaway repro.
 */

const SIZES = [
  { width: 720, height: 480, label: "min-supported" },
  { width: 900, height: 600, label: "small" },
] as const;

for (const lang of ["ar", "en"] as const) {
  for (const size of SIZES) {
    test(`sidebar footer stays reachable without scrolling - ${lang} @ ${size.label} (${size.width}x${size.height})`, async ({
      page,
    }) => {
      await page.setViewportSize({ width: size.width, height: size.height });
      await openShell(page, { lang });
      await goToTimeline(page);

      // The reproduction condition: content taller than the viewport.
      const overflowsViewport = await page.evaluate(
        () => document.documentElement.scrollHeight > document.documentElement.clientHeight,
      );
      expect(
        overflowsViewport,
        "test setup invalid: Timeline's content must exceed the viewport for this to be a real reproduction, not a tautology",
      ).toBe(true);

      // The actual assertion: regardless of that overflow, the language
      // toggle (the sidebar's bottom-anchored footer control) must have a
      // bounding box fully inside the viewport, unscrolled.
      const langToggle = page.locator(".lang-toggle");
      await expect(langToggle).toBeVisible();
      const box = await langToggle.boundingBox();
      expect(box, "lang-toggle must be laid out").not.toBeNull();
      if (box) {
        expect(box.y).toBeGreaterThanOrEqual(0);
        expect(box.y + box.height).toBeLessThanOrEqual(size.height);
        expect(box.x).toBeGreaterThanOrEqual(0);
        expect(box.x + box.width).toBeLessThanOrEqual(size.width);
      }

      // No document-level horizontal scroll either (the reflow half of
      // P0-01/P1-04).
      const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth);
      const clientWidth = await page.evaluate(() => document.documentElement.clientWidth);
      expect(scrollWidth).toBeLessThanOrEqual(clientWidth);
    });
  }
}
