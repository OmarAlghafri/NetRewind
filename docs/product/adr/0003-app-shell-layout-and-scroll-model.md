# ADR 0003 — App shell layout: independent scroll regions, no document scroll

**Status:** Accepted, implemented (`desktop/src/App.css`, `App.tsx`,
`components/Sidebar.tsx`, `onboarding/Wizard.tsx`).
**Date:** 2026-09-19.

## Context

`desktop/src/App.css:82-86` gives `.app-shell` `display: grid;
grid-template-columns: 220px 1fr; min-height: 100vh;` and nothing else - no
`overflow`, no `position: sticky`, no `min-height: 0` on any child. Because
`min-height` is a floor, not a ceiling, and CSS Grid stretches row-tracks to
fit the tallest item by default, `.sidebar` (a grid item) is forced to the
same height as `.main`'s content instead of the viewport. When `.main`'s
content (e.g. the Timeline page, which renders all recorded events with no
virtualization) is taller than the window, the grid row grows past the
viewport height, the browser adds a whole-document vertical scrollbar, and
`.sidebar` - including its bottom-anchored `.lang-toggle` - grows and scrolls
with it instead of staying pinned.

This was measured directly, not just read from the CSS. With the demo
recording (44 events) loaded, Timeline page, viewport 900x600:

```
document.documentElement.scrollHeight  = 1692
document.documentElement.clientHeight  = 600
getComputedStyle('.app-shell').overflow = "visible"
```

1092 of those 1692 pixels - 65% of the document - are unreachable without
scrolling past the sidebar entirely; after scrolling to the bottom of the
timeline, the sidebar column is empty (its own content ended at ~320px) and
every nav item, including the language toggle, requires scrolling back to
the top to reach. This is the mechanism behind the user's report: a shorter
window, or a longer page, reproduces it; a short page in a tall window does
not, which is why casual testing on the Overview page alone missed it.

## Decision

- `.app-frame` (rename to make the change explicit and avoid partial-CSS
  collisions with the old `.app-shell`) gets `block-size: 100dvh;
  min-block-size: 0; overflow: hidden;` - a ceiling, not a floor, and the
  grid can never grow past the viewport.
- `.sidebar` becomes its own grid: `header` (brand + recorder state, fixed),
  `nav` (the scrollable region, `min-block-size: 0; overflow: auto;`),
  `footer` (language/theme/collapse, fixed, always reachable regardless of
  nav length).
  `.workspace` gets a sticky `.workspace-header` and exactly one scrollable
  region, `.workspace-body` (`min-block-size: 0; overflow: auto;`) - this is
  the only place a normal vertical scrollbar may appear inside the main
  window.
- Every grid/flex child that can hold arbitrary-length text also gets
  `min-inline-size: 0`, which is what actually lets `overflow`/`text-overflow`
  work on a grid/flex item - its absence is a second, independent reason
  long content (a long incident title, a long path) can force a container
  wider than intended.
- Breakpoints: sidebar collapses to a 68-76px icon rail with tooltips at
  720-899px width; under 640px height, the sidebar's own header/footer stay
  pinned and only its nav list scrolls (mirroring the outer frame's fix at
  the component's own scale).
- `Sidebar.tsx`'s nav items move from `<div role="button" tabIndex={0}>` to
  real `<button>` elements inside `<nav aria-label="...">` - native focus,
  native activation, no more manual Enter/Space handling to keep in sync
  with browser behavior.

## Consequences

- No CSS framework is introduced; this is restructured hand-written CSS plus
  the existing custom-property tokens, kept internally consistent with
  ADR 0001's original logical-property RTL approach (`border-inline-end`,
  not `border-right`).
- Every page component that assumed "the page scrolls" (nothing today
  assumes otherwise, since nothing scrolls independently yet) needs no
  change; only `App.css`, `App.tsx`'s root markup, and `Sidebar.tsx` change
  for this ADR specifically. Later phases restructure individual pages for
  other reasons.
- A data table (e.g. a wide technical detail view) may still declare its own
  local horizontal scroll; the page-level rule is that `.workspace-body`
  itself never needs two-dimensional scrolling to reach primary actions.

## Verification

- `desktop/tests/visual/shell-layout.spec.ts`: 22 cases (5 widths x 2
  languages x 2 color schemes, plus a 320x256 WCAG 1.4.10 reflow check in
  each language) - all pass. Each case first asserts `.workspace-body`
  still genuinely overflows (Timeline's 44 rows against a 600px-tall
  viewport), then asserts the language toggle and every sidebar nav button
  are fully inside the viewport, then asserts neither axis of
  `document.documentElement` needs scrolling.
- `desktop/tests/visual/wizard-layout.spec.ts`: found and fixed the same
  defect class in `.wizard-overlay`/`.wizard-card` while verifying this fix
  (not originally scoped to this ADR) - the privacy step's body text
  overflowed a 720x480 window by 15px vertically. 1 case, passes.
- `desktop/tests/visual/accessibility.spec.ts`: axe-core against the
  restructured `.sidebar` (native `<button>`s, a real `<nav aria-label>`),
  both languages - 0 serious/critical violations.
- Visually inspected directly (not only via the automated assertions): the
  Timeline page at 900x600 scrolled to its last row, English and Arabic -
  the sidebar and its language toggle stay exactly in place, unscrolled,
  confirmed independently via `getBoundingClientRect()` in the live page
  (`top: 559, bottom: 588` inside a 600px viewport); the wizard's privacy
  step at 720x480 - full body text and all three actions (skip/back/next)
  visible with no clipping or scrolling.
- Regression: `npm test` 15/15, `npx tsc --noEmit` clean, `npm run build`
  (94.59KB gzip JS, 2.22KB gzip CSS - both essentially unchanged from the
  330.15KB/94.47KB gzip JS, 7.13KB/1.96KB gzip CSS baseline), `cargo test
  --lib` 8/8 (untouched by this change, confirmed still passing).
- Full account: `docs/evidence/30-gui-modernization-phase1-shell-fix.log`.
