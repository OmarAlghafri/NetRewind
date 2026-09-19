import type { ButtonHTMLAttributes } from "react";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

type Props = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant };

/**
 * The one button primitive the app uses everywhere a button is a button -
 * previously each area invented its own (`.wizard-btn`, `.wizard-btn-ghost`,
 * `.wizard-btn-primary`, none of it shared with anything outside the
 * wizard). `secondary` is the bordered default (`.btn` with no extra
 * class); the others map straight to a `.btn-<variant>` class.
 */
export function Button({ variant = "secondary", className = "", type = "button", ...props }: Props) {
  const variantClass = variant === "secondary" ? "" : ` btn-${variant}`;
  return <button type={type} className={`btn${variantClass}${className ? ` ${className}` : ""}`} {...props} />;
}

type IconButtonProps = Omit<Props, "children"> & { label: string; children: React.ReactNode };

/** A square, icon-sized Button that requires an accessible label - there is
 *  no icon set to render yet (P2: "no icon system"), so today this renders
 *  its children (e.g. a short glyph or abbreviation) the same way, and
 *  swapping in real icons later is a change inside this one component, not
 *  at every call site. */
export function IconButton({ variant = "ghost", label, className = "", children, ...props }: IconButtonProps) {
  const variantClass = variant === "secondary" ? "" : ` btn-${variant}`;
  return (
    <button
      type="button"
      className={`btn icon-btn${variantClass}${className ? ` ${className}` : ""}`}
      aria-label={label}
      title={label}
      {...props}
    >
      {children}
    </button>
  );
}
