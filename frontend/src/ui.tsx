/** Shared T3 Code-style page chrome, settings rows and compact form controls. */
import type { ReactNode } from "react";
import { ChevronRightIcon } from "lucide-react";
import { cn } from "../../tools/parity/src/bridges/utils";
import {
  Select,
  SelectItem,
  SelectPopup,
  SelectTrigger,
  SelectValue,
} from "../vendor/ui/select";

/**
 * Sticky page header with an optional breadcrumb parent and trailing controls.
 *
 * @param props - Title, optional parent label and trailing controls.
 * @returns The page header element.
 */
export function PageHeader({
  title,
  parent,
  status,
  children,
}: {
  title: ReactNode;
  parent?: string;
  status?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <header className="page-header sticky top-0 z-20 flex min-h-13 flex-wrap items-center gap-x-4 gap-y-2 border-b border-border bg-background/85 px-6 py-2 backdrop-blur-md max-md:px-4">
      <div className="flex min-w-0 flex-1 items-center gap-1.5 text-sm max-md:basis-full">
        {parent && (
          <>
            <span className="shrink-0 text-muted-foreground">{parent}</span>
            <ChevronRightIcon
              aria-hidden
              className="size-3.5 shrink-0 text-muted-foreground/70"
            />
          </>
        )}
        <h1 className="truncate font-medium text-foreground">{title}</h1>
        {status && (
          <span className="ms-1 truncate text-xs text-muted-foreground">
            {status}
          </span>
        )}
      </div>
      {children && (
        <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">
          {children}
        </div>
      )}
    </header>
  );
}

/**
 * Centered, density-aware page content column.
 *
 * @param props - Content and optional width preset.
 * @returns The page body element.
 */
export function PageBody({
  children,
  width = "wide",
}: {
  children: ReactNode;
  width?: "wide" | "narrow";
}) {
  return (
    <div
      className={cn(
        "page-body mx-auto flex w-full min-w-0 flex-col px-6 max-md:px-4",
        width === "wide" ? "max-w-6xl" : "max-w-3xl",
      )}
    >
      {children}
    </div>
  );
}

/**
 * Section heading shared by every settings group: a title and an optional trailing action.
 *
 * @param props - Heading text and trailing action.
 * @returns The heading row element.
 */
export function SectionHeading({
  title,
  action,
}: {
  title: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex min-h-7 items-center justify-between gap-4 px-1">
      <h2 className="min-w-0 truncate text-sm font-medium text-foreground">
        {title}
      </h2>
      {action && (
        <div className="flex shrink-0 items-center gap-2">{action}</div>
      )}
    </div>
  );
}

/**
 * Titled settings group whose rows share one bordered card.
 *
 * @param props - Heading, trailing action and rows.
 * @returns The settings section element.
 */
export function SettingsSection({
  title,
  action,
  children,
}: {
  title: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="flex min-w-0 flex-col gap-2.5">
      <SectionHeading title={title} action={action} />
      <div className="settings-card min-w-0 divide-y divide-border overflow-hidden rounded-xl border border-border bg-card text-card-foreground">
        {children}
      </div>
    </section>
  );
}

/**
 * One settings row: label and description on the left, control on the right.
 *
 * @param props - Row title, description, control and optional full-width content.
 * @returns The settings row element.
 */
export function SettingsRow({
  title,
  description,
  control,
  children,
  className,
}: {
  title?: ReactNode;
  description?: ReactNode;
  control?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("settings-row flex min-w-0 flex-col gap-3", className)}>
      {(title || control) && (
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-2">
          <div className="flex min-w-0 flex-1 basis-56 flex-col gap-0.5">
            {title && <div className="text-sm text-foreground">{title}</div>}
            {description && (
              <div className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
                {description}
              </div>
            )}
          </div>
          {control && (
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              {control}
            </div>
          )}
        </div>
      )}
      {children}
    </div>
  );
}

/**
 * Inline status or guidance message.
 *
 * @param props - Message tone, content and optional trailing action.
 * @returns The notice element.
 */
export function Notice({
  tone = "default",
  action,
  children,
}: {
  tone?: "default" | "error";
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div
      role="status"
      className={cn(
        "flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1.5 rounded-lg border px-3 py-2 text-xs [overflow-wrap:anywhere]",
        tone === "error"
          ? "border-destructive/30 bg-destructive/5 text-destructive-foreground"
          : "border-border bg-muted/40 text-muted-foreground",
      )}
    >
      <span className="min-w-0">{children}</span>
      {action}
    </div>
  );
}

/**
 * Centered placeholder for pages and charts without displayable data.
 *
 * @param props - Placeholder message.
 * @returns The empty-state element.
 */
export function EmptyState({ children }: { children: string }) {
  return (
    <p
      role="status"
      className="rounded-xl border border-dashed border-border px-6 py-16 text-center text-sm text-muted-foreground"
    >
      {children}
    </p>
  );
}

/**
 * Labelled vertical form field.
 *
 * @param props - Field label, optional hint and control.
 * @returns The field element.
 */
export function Field({
  label,
  hint,
  children,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <label className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      {children}
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
}

/** One option of an {@link OptionSelect}. */
export type SelectOption<Value> = { value: Value; label: string };

/**
 * Single-choice select built on the original Base UI select primitives.
 *
 * @param props - Current value, options, change handler and accessibility label.
 * @returns The select element.
 */
export function OptionSelect<Value extends string | number>({
  value,
  options,
  onChange,
  label,
  disabled,
  className,
}: {
  value: Value;
  options: readonly SelectOption<Value>[];
  onChange: (value: Value) => void;
  label: string;
  disabled?: boolean;
  className?: string;
}) {
  return (
    <Select
      items={options}
      value={value}
      disabled={disabled}
      onValueChange={(next) => {
        if (next !== null) onChange(next as Value);
      }}
    >
      <SelectTrigger
        aria-label={label}
        size="sm"
        className={cn("w-auto min-w-40", className)}
      >
        <SelectValue />
      </SelectTrigger>
      <SelectPopup>
        {options.map((option) => (
          <SelectItem key={String(option.value)} value={option.value}>
            {option.label}
          </SelectItem>
        ))}
      </SelectPopup>
    </Select>
  );
}
