/** Sidebar and bottom-tab navigation row shared by the desktop app and the relay viewer. */
import type { LucideIcon } from "lucide-react";
import { t } from "./i18n";

/**
 * Sidebar navigation row that collapses to its icon in narrow windows.
 *
 * @param props - Icon, label, optional bottom-tab label, current/dirty state
 * and click handler.
 * @returns The navigation button.
 */
export function NavItem({
  icon: Icon,
  label,
  shortLabel,
  title,
  isCurrent,
  isDirty,
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  shortLabel?: string;
  title?: string;
  isCurrent?: boolean;
  isDirty?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="sidebar-item"
      aria-current={isCurrent ? "page" : undefined}
      aria-label={isDirty ? t("{label} (unsaved)", { label }) : label}
      title={title ?? label}
      onClick={onClick}
    >
      <Icon aria-hidden />
      <span
        className={
          shortLabel
            ? "sidebar-item-label sidebar-item-long"
            : "sidebar-item-label"
        }
      >
        {label}
      </span>
      {shortLabel && (
        <span className="sidebar-item-label sidebar-item-short">
          {shortLabel}
        </span>
      )}
      {isDirty && <span className="sidebar-dirty" aria-hidden />}
    </button>
  );
}
