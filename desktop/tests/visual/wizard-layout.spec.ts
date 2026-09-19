import { expect, test } from "@playwright/test";

/**
 * Found while verifying P0-01's fix, not originally in the execution
 * order's explicit scope - the same defect class exists in the first-run
 * wizard. `.wizard-overlay` used `min-height: 100vh` (a floor) with no
 * bound, and the privacy step's body text alone is long enough to overflow
 * a 720x480 window (the minimum supported size): measured before the fix,
 * document 495px vs. a 480px viewport - small (15px), but real, and the
 * review explicitly asks for the wizard's back/next/skip actions to stay
 * reachable without scrolling the whole card at a short window (product
 * release plan §3: "أزرار السابق/التالي/تخطي تبقى sticky داخل البطاقة عند
 * قصر النافذة"). Fixed with the same containment pattern as the app shell:
 * `.wizard-overlay` bounded to 100dvh, `.wizard-step-body` is the only
 * scroll region inside `.wizard-card`, `.wizard-actions` stays pinned.
 */
test("wizard actions stay reachable on the longest step at the minimum window size", async ({ page }) => {
  await page.setViewportSize({ width: 720, height: 480 });
  await page.addInitScript(() => {
    window.localStorage.removeItem("netrewind.onboardingComplete");
    window.localStorage.setItem("netrewind.lang", "ar");
  });
  await page.goto("/");
  await page.waitForSelector(".wizard-card");

  // Advance to the privacy step - the longest body text of the six steps.
  await page.getByRole("button", { name: "التالي" }).click();
  await page.waitForSelector(".wizard-step-body");

  const doc = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
    scrollHeight: document.documentElement.scrollHeight,
    clientHeight: document.documentElement.clientHeight,
  }));
  expect(doc.scrollWidth, "no document-level horizontal scroll").toBeLessThanOrEqual(doc.clientWidth);
  expect(doc.scrollHeight, "no document-level vertical scroll").toBeLessThanOrEqual(doc.clientHeight);

  // The actions bar (skip / back / next) must be visible without scrolling
  // anything, even on the longest step.
  const actions = page.locator(".wizard-actions");
  await expect(actions).toBeVisible();
  const box = await actions.boundingBox();
  expect(box).not.toBeNull();
  if (box) {
    expect(box.y + box.height).toBeLessThanOrEqual(480);
  }
  await expect(page.getByRole("button", { name: "تخطّ الآن" })).toBeVisible();
});
