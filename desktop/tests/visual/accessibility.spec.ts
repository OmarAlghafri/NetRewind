import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { openShell } from "./helpers";

/**
 * A first, deliberately small axe pass on the restructured shell (Phase 1),
 * not the full every-route/every-theme sweep the execution order's §7
 * requires by Phase 2's gate. This exists now specifically to check the
 * Sidebar.tsx rewrite (`<div role="button">` -> native `<button>`, a real
 * `<nav aria-label>`) did not introduce a new violation while nothing else
 * about the shell's accessibility has changed - a narrow regression check,
 * not the DoD item itself.
 */
for (const lang of ["ar", "en"] as const) {
  test(`shell has no serious/critical axe violations - ${lang}`, async ({ page }) => {
    await openShell(page, { lang });
    const results = await new AxeBuilder({ page })
      .include(".sidebar")
      .withTags(["wcag2a", "wcag2aa"])
      .analyze();
    const serious = results.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
    expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
  });
}
