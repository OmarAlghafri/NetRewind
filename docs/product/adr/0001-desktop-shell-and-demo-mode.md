# ADR 0001 — Desktop shell: Tauri v2 + React/TypeScript, Demo mode first

**Status:** Accepted, implemented (`desktop/`).
**Date:** 2026-09-13.

## Context

`PRODUCT_RELEASE_PLAN_AR.md` §4.2 names Tauri v2 + React/TypeScript as the
starting choice and requires the first working slice to be a **Demo/Import**
mode against a fixed corpus, not a live agent connection (§5, Phase 2) - the
deterministic core, the agent's IPC, and a live connection did not all exist
yet when this app needed to start proving the UX.

## Decision

- **Shell:** Tauri v2, scaffolded with `npm create tauri-app` (react-ts
  template), living at `desktop/`. Confirmed to actually build on this
  machine: `cargo check` and `cargo build` both succeed against the MSVC
  toolchain (Visual Studio Build Tools, already present). Rust itself was
  not present on this machine and was installed via
  `winget install Rustlang.Rustup` - a plain, reversible toolchain install,
  not a business or legal decision, so it was done rather than treated as a
  blocker.
- **Frontend:** React + TypeScript + Vite, no router pulled in yet (five
  pages, switched by local state - PRD's use cases do not yet need
  deep-linkable URLs, and a dependency should earn its place, not be
  assumed). No i18n library either: two languages, a hand-written
  dictionary (`src/i18n/translations.ts`) is smaller and more auditable
  than a library for exactly two keys.
- **Types mirror the Go side exactly:** `src/types.ts`'s `NetRewindEvent` and
  `Incident` are field-for-field copies of `internal/event.Event` and
  `internal/incident.Incident` (docs/schema.md), so the same JSON the CLI's
  `-o json` and the future `internal/api/v1` already produce needs no
  translation layer to render.
- **Demo data is real, not synthetic:** `src/demo/demo-events.json` and
  `demo-incidents.json` are an actual export (`netrewind events/incidents -o
  json`) from a real run of `lab/inject.sh all` on this machine
  (`docs/evidence/`), not hand-written fixtures shaped to look good.
- **Data-source boundary:** every page (`Overview`, `Incidents`, `Timeline`,
  `Host`, `Settings`) takes plain `events`/`incidents` props and has no idea
  where they came from. `App.tsx` is the only place that currently calls
  `loadDemoEvents()`/`loadDemoIncidents()` - swapping in a live source read
  through `internal/api/v1` over `internal/ipc` later is a change to that one
  call site, not to any page.
- **Capability honesty in Demo mode:** the Health/Overview page does **not**
  claim a live "up/down" status, since there is no `registry.Snapshot` to
  read without a running agent. It instead reports which `source` values are
  *present in the recording* - a different, honestly-labelled claim,
  matching the plan's own rule against implying a capability just because
  the app is open.
- **RTL:** logical CSS properties (`border-inline-end`, `margin-inline-start`,
  `inset-inline-start`) throughout, no `left`/`right` physical properties in
  the layout CSS, and the sidebar is the first DOM child with `dir` set on
  `<html>` - this is what puts it on the conventional "start" side (right in
  Arabic, left in English) automatically, without a per-direction override.
  An earlier draft used an explicit `order` + column-swap override for RTL
  and it put the sidebar on the *physical* left in Arabic (i.e., the LTR
  convention, unchanged) - wrong, and only caught by actually looking at a
  screenshot in both languages, not by reading the CSS.
- **Relation distinction is not color-only:** `causes` (solid), `correlates`
  (dashed), `precedes` (dotted) are different `border-style` values on the
  connecting line, per §3's explicit requirement, verified visually.

## Consequences

- A new dependency was added on the Go side for later Windows IPC work
  (`github.com/Microsoft/go-winio`) and a new toolchain (Rust) on this
  machine for the desktop shell - both plain, reversible installs, not
  license or business decisions.
- Rule text (`why`, `advice`, incident `title`) inside `rules/*.yaml` is
  English-only today. The Arabic UI chrome (nav, labels, buttons) is fully
  bilingual, but incident narrative text is not yet translated - visible
  directly in the running app (Arabic UI, English incident prose) and
  tracked here rather than hidden. Translating 19 rule files' prose is a
  separate, larger piece of work, out of scope for this slice.
- No router, no state-management library, no live connection yet - all
  deliberate scope limits for a first slice, not oversights, listed in
  `docs/evidence/` alongside what was actually tested.

## Verification

- `npx tsc --noEmit`: clean.
- `npm run build` (Vite production build): succeeds, 30 modules, ~81 KB
  gzipped JS.
- `cargo check` / `cargo build` in `desktop/src-tauri`: succeed on this
  machine (Windows, MSVC toolchain already present).
- Visual verification, both languages, all five pages, via a real running
  Vite dev server viewed in a browser: RTL/LTR both correct after the
  sidebar-placement fix above, causes/correlates line styles both visible
  and distinguishable, real incident and event data rendered correctly,
  host search filters real events by a real IP address.
