// The compile-time gate ADR 0006 describes: the local-AI panel is built
// but stays entirely invisible until a model profile actually passes its
// pre-registered evaluation gate (execution order §4.10). This is
// deliberately not the same thing as aiSettings.enabled (data/aiSettings.ts)
// - that is a plain, already-off-by-default runtime setting an operator
// who could already reach the feature turns on for themselves; this is the
// switch that decides whether they can reach it at all. Mirrors
// desktop-tauri's ai::AI_FEATURE_ENABLED - both flip in exactly one place
// when a profile's gate actually passes.

declare const __AI_FEATURE_ENABLED__: boolean;

export const AI_FEATURE_ENABLED: boolean = __AI_FEATURE_ENABLED__;
