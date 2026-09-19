import { expect, test, type Page } from "@playwright/test";
import { openShell } from "./helpers";

/**
 * Execution order P0-02 / ADR 0004's actual Phase 4 gate: "an automated
 * scan of the rendered Arabic app that flags any Latin-script sentence
 * not inside a `<bdi dir="ltr">`/`TechnicalValue` wrapper."
 *
 * This is not "no Latin characters anywhere" - the writing rules (execution
 * order §10.4) deliberately keep short technical terms inline in Latin
 * script (DHCP, MTU, VPN, ICMP, kernel, netlink, BTF, and occasionally a
 * one-or-two-word gloss in parentheses right after the Arabic term it
 * explains, e.g. "منفذ المبدّل (switch port)"), which is standard practice
 * in Arabic technical writing, not a translation gap. What actually
 * indicates a gap is a run of Latin words that reads as an English
 * sentence or clause - which in practice always contains at least one
 * plain English function word ("the", "is", "which", ...) that a short
 * technical noun or acronym never does. `latinViolations` below finds the
 * maximal runs of Latin-script text inside each non-technical text node
 * and flags a run only if it both has several words AND contains one of
 * those function words, so "VPN", "BTF", "(switch port)", "(failover)"
 * and "NetRewind" all pass, while "An interface went down." does not.
 */
const STOPWORDS = new Set([
  "a", "an", "the", "is", "are", "was", "were", "been", "being", "be",
  "and", "or", "but", "nor", "so", "yet",
  "this", "that", "these", "those", "which", "who", "whom", "whose",
  "has", "have", "had", "do", "does", "did",
  "on", "at", "to", "from", "with", "for", "of", "in", "into", "onto", "by", "as",
  "it", "its", "not", "no", "if", "when", "while", "after", "before", "because",
  "than", "then", "there", "their", "they", "them",
  "he", "she", "we", "you", "your", "his", "her", "our",
  "will", "would", "should", "could", "can", "may", "might", "must",
  "one", "any", "some", "all", "each", "every", "other", "another",
]);

// A page/wizard step this scan visited and the Latin runs it flagged on it.
interface Violation {
  where: string;
  text: string;
  latinRun: string;
}

/** Finds every maximal run of Latin-script words in `text` that also
 *  contains at least one English function word - i.e. reads like a
 *  sentence fragment rather than a technical token or acronym. */
function sentenceLikeLatinRuns(text: string): string[] {
  const runs = text.match(/[A-Za-z][A-Za-z0-9'.,()/_-]*(?:\s+[A-Za-z0-9'.,()/_-]+)*/g) ?? [];
  const hits: string[] = [];
  for (const run of runs) {
    const words = run.match(/[A-Za-z']+/g) ?? [];
    if (words.length < 3) continue;
    if (words.some((w) => STOPWORDS.has(w.toLowerCase()))) hits.push(run.trim());
  }
  return hits;
}

/** Runs the scan against whatever is currently rendered in `page`, tagging
 *  every violation with `where` (the page/step name) for a readable report. */
async function scanCurrentPage(page: Page, where: string): Promise<Violation[]> {
  const nodes = await page.evaluate(() => {
    const EXCLUDED = "bdi, code, pre, script, style, [dir='ltr'], .ltr-field, .technical-value";
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const out: string[] = [];
    let node: Node | null;
    while ((node = walker.nextNode())) {
      const text = node.textContent?.trim();
      if (!text) continue;
      const parent = node.parentElement;
      if (parent && parent.closest(EXCLUDED)) continue;
      out.push(text);
    }
    return out;
  });

  const violations: Violation[] = [];
  for (const text of nodes) {
    for (const latinRun of sentenceLikeLatinRuns(text)) {
      violations.push({ where, text, latinRun });
    }
  }
  return violations;
}

test("proves the scanner itself catches an untranslated sentence, not just passes trivially", () => {
  const clean = sentenceLikeLatinRuns("الوصلة منقطعة l2.neighbor_failed nrlab0 (LAN) VPN");
  expect(clean).toEqual([]);

  const dirty = sentenceLikeLatinRuns("النص: An interface went down. باقي الجملة");
  expect(dirty).toEqual(["An interface went down."]);

  const glossOnly = sentenceLikeLatinRuns("حدّد منفذ المبدّل (switch port) الذي يتعطل");
  expect(glossOnly).toEqual([]);
});

const PAGES = ["overview", "incidents", "timeline", "host", "rules", "evidence", "diagnostics", "settings"];

/**
 * One exact, closed, tracked exception - not a general escape hatch.
 *
 * desktop/src/demo/demo-incidents.json used to predate internal/incident.
 * Link's `Clause` field, so every chain link's `why` on the Incidents page
 * (and the wizard step that reuses IncidentCard against it) fell back to
 * English - `rulesCatalogue.ts`'s `whyFor` working exactly as designed
 * against a fixture it could not yet translate, not a code defect. Fixed
 * by regenerating the fixture through internal/correlate/replaydemo, which
 * needed its own fix first (see the tool's doc comments and
 * docs/evidence/38-gui-modernization-phase4-latin-scanner.log and
 * docs/evidence/53-replaydemo-fold-aware-regeneration.log): the naive
 * replay this tool started as offered the engine one Offer() call per
 * already-folded JSON entry instead of one per raw occurrence, which
 * silently changed what several rules concluded - fixed, verified against
 * the original fixture field-by-field, and confirmed here by every entry
 * that used to be in this list no longer reproducing.
 *
 * Empty for now: every previously-known gap is gone. Kept, rather than
 * replaced with a bare `expect(violations).toEqual([])`, because a real
 * future gap (a new rule shipped without its `i18n.ar` block, say) should
 * be recorded here deliberately while it is being fixed, the same way this
 * one was - not left for `assertOnlyKnownGaps` to fail without context on
 * whoever hits it next.
 */
const KNOWN_GAPS: Record<string, string[]> = {};

/** Asserts a scanned page/step against KNOWN_GAPS[where]: nothing beyond
 *  the exact tracked exceptions (a new or different violation still
 *  fails), and every tracked exception must still actually reproduce (one
 *  that stops fails too - see KNOWN_GAPS's own comment). */
function assertOnlyKnownGaps(where: string, violations: Violation[]) {
  const found = new Set(violations.map((v) => v.latinRun));
  const known = new Set(KNOWN_GAPS[where] ?? []);

  const unexpected = violations.filter((v) => !known.has(v.latinRun));
  expect(unexpected, `${where}: unexpected violations:\n${JSON.stringify(unexpected, null, 2)}`).toEqual([]);

  const stale = [...known].filter((k) => !found.has(k));
  expect(
    stale,
    `${where}: these KNOWN_GAPS entries no longer reproduce - remove them:\n${JSON.stringify(stale, null, 2)}`,
  ).toEqual([]);
}

test("the rendered Arabic app has no untagged Latin sentence on any page", async ({ page }) => {
  await openShell(page, { lang: "ar" });

  for (const p of PAGES) {
    const where = `page:${p}`;
    await page.goto(`/#/${p}`);
    await page.waitForSelector(".page-title, .empty-state, .card");
    assertOnlyKnownGaps(where, await scanCurrentPage(page, where));
  }
});

test("the Arabic onboarding wizard has no untagged Latin sentence on any step", async ({ page }) => {
  await page.addInitScript(() => {
    window.localStorage.removeItem("netrewind.onboardingComplete");
    window.localStorage.setItem("netrewind.lang", "ar");
  });
  await page.goto("/");
  await page.waitForSelector(".wizard-card");

  for (let step = 0; step < 6; step++) {
    const where = `wizard-step:${step}`;
    assertOnlyKnownGaps(where, await scanCurrentPage(page, where));
    const nextBtn = page.getByRole("button", { name: "التالي" });
    if (await nextBtn.isVisible()) await nextBtn.click();
  }
});
