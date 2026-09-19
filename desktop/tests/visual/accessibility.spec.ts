import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "@playwright/test";
import { openShell } from "./helpers";

/**
 * A first, deliberately small axe pass on the restructured shell and the
 * new Button/TechnicalValue components (Phases 1-2), not the full
 * every-route/every-theme sweep the execution order's §7 requires by
 * Phase 2's gate proper. This exists now to check that neither the
 * Sidebar.tsx rewrite (`<div role="button">` -> native `<button>`, a real
 * `<nav aria-label>`) nor the wizard's button migration
 * (`.wizard-btn` -> `<Button>`) introduced a new violation - a narrow
 * regression check, not the DoD item itself.
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

  test(`wizard has no serious/critical axe violations on every step - ${lang}`, async ({ page }) => {
    // Six sequential full-page axe scans in one test - legitimately more
    // work than the default 30s budget assumes, and it showed: under this
    // suite's normal 4-worker parallelism this test alone occasionally
    // exceeded 30s (39.6s observed) purely from CPU contention, with the
    // failure output actually reading "Test timeout of 30000ms exceeded" -
    // not an axe violation. Not flaky in the sense of a real bug; the
    // budget was just wrong for what this test actually does.
    test.setTimeout(60_000);
    await page.addInitScript(
      ([lang]) => {
        window.localStorage.removeItem("netrewind.onboardingComplete");
        window.localStorage.setItem("netrewind.lang", lang);
      },
      [lang],
    );
    await page.goto("/");
    await page.waitForSelector(".wizard-card");

    const nextLabel = lang === "ar" ? "التالي" : "Next";
    for (let step = 0; step < 6; step++) {
      const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa"]).analyze();
      const serious = results.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
      expect(serious, `step ${step}: ${JSON.stringify(serious, null, 2)}`).toEqual([]);
      const nextBtn = page.getByRole("button", { name: nextLabel });
      if (await nextBtn.isVisible()) await nextBtn.click();
    }
  });

  // Incidents' master/detail (execution order §9 Phase 5) is opt-in - only
  // reached by selecting a row - so the shell-level scan above never
  // exercises it. A click-triggered UI state is exactly the kind of new
  // attack surface a once-per-page scan misses, so it gets its own check.
  test(`Incidents master/detail has no serious/critical axe violations - ${lang}`, async ({ page }) => {
    await openShell(page, { lang });
    await page.goto("/#/incidents");
    await page.waitForSelector(".incident-card-wrapper");
    await page.locator(".incident-focus-link").first().click();
    await page.waitForSelector(".inspector-panel");
    const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa"]).analyze();
    const serious = results.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
    expect(serious, JSON.stringify(serious, null, 2)).toEqual([]);
  });
}
