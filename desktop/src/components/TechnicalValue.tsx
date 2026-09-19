import type { ReactNode } from "react";

/**
 * Wraps a technical value (IP, MAC, path, event/rule ID, timestamp) so it
 * stays left-to-right and isolated from the surrounding paragraph
 * direction, in both languages - what `.ltr-field` did as a class applied
 * by hand at each call site (`IncidentCard.tsx`, `CapabilityTable.tsx`,
 * `Settings.tsx`, ...), now one component so the rule ("technical values
 * only, never a whole mixed-language sentence" - execution order §4.6)
 * has one place to hold, instead of depending on every call site applying
 * the class to the right span.
 *
 * A real `<bdi>` element, not a `<span>` styled to look isolated: `<bdi>`
 * isolates bidi algorithm behavior at the DOM level, which a styled span
 * does not, on top of the explicit `direction: ltr` this project's
 * technical values specifically need (they are always LTR content -
 * addresses, paths, IDs - not bidi-neutral text `<bdi>` alone would be
 * enough for).
 *
 * Copy-to-clipboard and truncation/tooltip for long values (the rest of
 * §4.9's spec for this component) are Phase 5 work (`docs/desktop.md`'s
 * Diagnostics page explicitly wants a copy button with feedback) - not
 * added here yet, so as not to half-build a feature this phase doesn't
 * need.
 *
 * `dateTime` (execution order §4.6): renders a real `<time>` element
 * instead of `<bdi>` for an actual point in time, so it carries a
 * machine-readable ISO value alongside the formatted display text - the
 * one variant of "technical value" that has its own semantic HTML
 * element rather than being purely visual isolation.
 */
export function TechnicalValue({
  children,
  className = "",
  dateTime,
}: {
  children: ReactNode;
  className?: string;
  dateTime?: string;
}) {
  const cls = `technical-value${className ? ` ${className}` : ""}`;
  if (dateTime !== undefined) {
    return (
      <time dateTime={dateTime} className={cls}>
        {children}
      </time>
    );
  }
  return <bdi className={cls}>{children}</bdi>;
}
