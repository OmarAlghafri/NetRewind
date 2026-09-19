import { describe, expect, it } from "vitest";
import generated from "./generated/kinds.json";
import { familyLabels, kindLabels, labelForFamily, labelForKind } from "./kindCatalogue";

// ADR 0004: "a new Go kind cannot silently ship without a matching GUI
// entry." internal/event/kind_catalogue_test.go proves generated/kinds.json
// itself stays in step with internal/event/kinds.go; this test proves the
// other half of the chain, that kindCatalogue.ts's hand-written labels stay
// in step with generated/kinds.json - together the two tests mean a kind
// added to the Go source and never translated fails a test, not silently
// renders as a bare technical code with no human name.
describe("kindCatalogue completeness against the generated kind list", () => {
  it("has an entry for every generated kind, and no extra ones", () => {
    const generatedSet = new Set(generated.kinds);
    const labelSet = new Set(Object.keys(kindLabels));

    const missing = generated.kinds.filter((k) => !labelSet.has(k));
    const extra = Object.keys(kindLabels).filter((k) => !generatedSet.has(k));

    expect(missing, `kinds.json has kinds with no kindCatalogue entry: ${missing.join(", ")}`).toEqual([]);
    expect(extra, `kindCatalogue has entries for kinds kinds.json no longer lists: ${extra.join(", ")}`).toEqual([]);
  });

  it("has an entry for every generated family, and no extra ones", () => {
    const generatedSet = new Set(generated.families);
    const labelSet = new Set(Object.keys(familyLabels));

    const missing = generated.families.filter((f) => !labelSet.has(f));
    const extra = Object.keys(familyLabels).filter((f) => !generatedSet.has(f));

    expect(missing, `kinds.json has families with no familyLabels entry: ${missing.join(", ")}`).toEqual([]);
    expect(extra, `familyLabels has entries for families kinds.json no longer lists: ${extra.join(", ")}`).toEqual([]);
  });

  it("gives every generated kind a non-empty label in both languages", () => {
    for (const kind of generated.kinds) {
      const entry = kindLabels[kind];
      expect(entry, `${kind} has no entry`).toBeDefined();
      expect(entry.en.trim(), `${kind}'s English label is empty`).not.toBe("");
      expect(entry.ar.trim(), `${kind}'s Arabic label is empty`).not.toBe("");
      // A label that is just the raw technical code is not a human name -
      // the whole point of this catalogue is to give the reader something
      // the code itself does not already say.
      expect(entry.en, `${kind}'s English label is the raw code, not a name`).not.toBe(kind);
    }
  });
});

describe("labelForKind", () => {
  it("returns the known human name for a real kind", () => {
    expect(labelForKind("link.down", "en")).toEqual({ name: "Link down", known: true });
    expect(labelForKind("link.down", "ar")).toEqual({ name: "الوصلة منقطعة", known: true });
  });

  it("falls back to the raw code for a kind this build does not know, and says so", () => {
    expect(labelForKind("l9.made_up_kind", "en")).toEqual({ name: "l9.made_up_kind", known: false });
    expect(labelForKind("l9.made_up_kind", "ar")).toEqual({ name: "l9.made_up_kind", known: false });
  });
});

describe("labelForFamily", () => {
  it("returns the known human name for a real family", () => {
    expect(labelForFamily("l2", "en")).toEqual({ name: "Layer 2", known: true });
    expect(labelForFamily("l2", "ar")).toEqual({ name: "الطبقة الثانية", known: true });
  });

  it("falls back to the raw code for a family this build does not know, and says so", () => {
    expect(labelForFamily("l9", "en")).toEqual({ name: "l9", known: false });
  });
});
