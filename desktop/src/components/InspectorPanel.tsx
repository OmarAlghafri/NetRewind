import { useLanguage } from "../i18n/LanguageContext";
import { IconButton } from "./Button";

/**
 * A secondary detail panel docked beside (or, on a narrow window, below)
 * the page's main content - execution order §4.1's shell diagram: "optional
 * InspectorPanel" inside `WorkspaceBody`.
 *
 * The diagram calls for this to "become a drawer under 900px width" - a
 * modal overlay with a focus trap. That needs correctness this project
 * cannot fully verify yet (no screen reader in this environment, and
 * axe-core does not exercise dynamic focus-trap/keyboard-modal behaviour),
 * and the sidebar's own rail-at-720-899px collapse is already documented
 * as deferred for the same shape of reason (`App.css`'s `@media
 * (max-width: 899px)` block: "Not yet an icon rail... needs the icon
 * system Phase 2 adds"). This is deliberately the simpler, fully-correct
 * alternative instead: a non-modal, always-visible `role="region"` that
 * CSS docks beside the list at >=900px and stacks below it under that -
 * never covering or hiding the list, so there is no focus trap to get
 * wrong because nothing is ever made inert. Upgrading to a true modal
 * drawer is tracked separately, once the icon-rail work gives this
 * project a real pattern for that to follow rather than a first, unverified
 * attempt at one.
 */
export function InspectorPanel({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  const { t } = useLanguage();
  return (
    <aside className="inspector-panel" role="region" aria-label={title}>
      <div className="inspector-panel-header">
        <strong>{title}</strong>
        <IconButton label={t("inspector_close_label")} onClick={onClose}>
          ✕
        </IconButton>
      </div>
      <div className="inspector-panel-body">{children}</div>
    </aside>
  );
}
