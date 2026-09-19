import { describe, expect, it } from "vitest";
import type { RuleSummary } from "../data/types";
import { adviceFor, findRule, getStaticRule, titleFor, whyFor } from "./rulesCatalogue";

// A rule genuinely shipped in rules/*.yaml (checked against the generated
// snapshot, not invented), used to prove the static fallback path actually
// returns real translated content, not just a well-typed empty object.
const REAL_RULE_ID = "address-changed-hands";
const REAL_CLAUSE = "handover";

describe("getStaticRule / findRule", () => {
  it("finds a rule that really shipped in rules/*.yaml from the static snapshot", () => {
    const rule = getStaticRule(REAL_RULE_ID);
    expect(rule).toBeDefined();
    expect(rule?.id).toBe(REAL_RULE_ID);
    expect(rule?.i18n?.ar?.title).toBeTruthy();
  });

  it("returns undefined for a rule id nothing shipped ever used", () => {
    expect(getStaticRule("no-such-rule-ever")).toBeUndefined();
  });

  it("findRule prefers a live rule with a matching id over the static snapshot", () => {
    const liveVersion: RuleSummary = {
      id: REAL_RULE_ID,
      title: "LIVE TITLE",
      severity: "warn",
      confidence: 75,
      window: "1m0s",
      root_cause: "handover",
      advice: "live advice",
    };
    const found = findRule(REAL_RULE_ID, [liveVersion]);
    expect(found).toBe(liveVersion);
    expect(found?.title).toBe("LIVE TITLE");
  });

  it("findRule falls back to the static snapshot when no live rule matches", () => {
    const found = findRule(REAL_RULE_ID, [{ ...getStaticRule("gateway-hijack")! }]);
    expect(found?.id).toBe(REAL_RULE_ID);
    expect(found?.i18n?.ar?.title).toBeTruthy();
  });

  it("findRule returns undefined when the rule is unknown to both live and static", () => {
    expect(findRule("no-such-rule-ever", [])).toBeUndefined();
  });
});

describe("titleFor / adviceFor / whyFor", () => {
  const rule = getStaticRule(REAL_RULE_ID)!;

  it("returns the Arabic translation when one exists", () => {
    expect(titleFor(rule, "ar", "fallback title")).toBe(rule.i18n!.ar!.title);
    expect(adviceFor(rule, "ar", "fallback advice")).toBe(rule.i18n!.ar!.advice);
    expect(whyFor(rule, REAL_CLAUSE, "ar", "fallback why")).toBe(rule.i18n!.ar!.clauses![REAL_CLAUSE]);
  });

  it("falls back to the given fallback text for English - a rule never translates into its own original language", () => {
    expect(titleFor(rule, "en", "incident's own English title")).toBe("incident's own English title");
    expect(adviceFor(rule, "en", "incident's own English advice")).toBe("incident's own English advice");
    expect(whyFor(rule, REAL_CLAUSE, "en", "incident's own English why")).toBe("incident's own English why");
  });

  it("falls back to the fallback text, never the rule's own current field, when rule is undefined", () => {
    expect(titleFor(undefined, "ar", "the incident's own title")).toBe("the incident's own title");
    expect(adviceFor(undefined, "ar", "the incident's own advice")).toBe("the incident's own advice");
    expect(whyFor(undefined, REAL_CLAUSE, "ar", "the incident's own why")).toBe("the incident's own why");
  });

  it("whyFor falls back when clause is absent - an incident built before Link.Clause existed", () => {
    expect(whyFor(rule, undefined, "ar", "the incident's own why")).toBe("the incident's own why");
  });

  it("whyFor falls back when the clause name does not match any translated clause", () => {
    expect(whyFor(rule, "no-such-clause", "ar", "the incident's own why")).toBe("the incident's own why");
  });

  it("never returns the rule's current English field as a stand-in for a missing translation - only ever the caller's fallback", () => {
    // Regression guard for the specific design decision in rulesCatalogue.ts:
    // an old bundle's incident.title (baked in at correlation time) must win
    // over rule.title (whatever the rule file says today) whenever there is
    // no actual translation to offer instead. Deliberately different from
    // the rule's real current title, so a bug that returned rule.title
    // instead of the fallback would be caught, not coincidentally match.
    const deliberatelyDifferentFromRuleTitle = "what this incident actually said at the time";
    expect(deliberatelyDifferentFromRuleTitle).not.toBe(rule.title);
    expect(titleFor(rule, "en", deliberatelyDifferentFromRuleTitle)).toBe(deliberatelyDifferentFromRuleTitle);
  });
});
