import { msg, t, useI18n } from "../src/i18n";
import { memo, useCallback, useEffect, useRef, useState } from "react";
import {
  isThemeColor,
  themeColorToHex,
  type ThemeColorRole,
} from "./themePalette";
import { cn } from "../../tools/parity/src/bridges/utils";
import {
  hexToHsv,
  hsvToHex,
  type HsvColor,
} from "../../tools/parity/upstream/apps/web/src/lib/color";
import { ColorHueSlider, ColorSaturationValuePlane } from "./color-picker";
import { Input } from "../../tools/parity/upstream/apps/web/src/components/ui/input";
import {
  Popover,
  PopoverPopup,
  PopoverTrigger,
} from "../../tools/parity/upstream/apps/web/src/components/ui/popover";
import {
  Tooltip,
  TooltipPopup,
  TooltipTrigger,
} from "../../tools/parity/upstream/apps/web/src/components/ui/tooltip";
export function getThemeRoleLabel(role: ThemeColorRole): string {
  const labels: Partial<Record<ThemeColorRole, string>> = {
    canvas: msg("Background"),
    toolbar: msg("Toolbar background"),
    toolbarForeground: msg("Toolbar text"),
    toolbarBorder: msg("Toolbar border"),
    toolbarControl: msg("Toolbar control"),
    toolbarControlForeground: msg("Toolbar control text"),
    toolbarControlHover: msg("Toolbar control hover"),
    accent: msg("Accent color"),
    errorForeground: msg("Error text"),
    errorSurface: msg("Error background"),
    warningForeground: msg("Warning text"),
    warningSurface: msg("Warning background"),
    updateForeground: msg("Update text"),
    updateSurface: msg("Update background"),
  };
  const label = labels[role];
  if (label) return t(label);
  return t(
    role
      .replace(/([A-Z])/g, " $1")
      .replace(/^./, (character) => character.toUpperCase()),
  );
}

/**
 * The picker remains an sRGB/hex adapter over the OKLCH palette engine. Alpha
 * is preserved separately and re-attached on commit so adjusting hue or
 * brightness cannot change transparency.
 */
function themePickerAlphaSuffix(value: string): string {
  const normalized = themeColorToHex(value) ?? "";
  const alpha = normalized.length === 9 ? normalized.slice(7) : "";
  return alpha === "ff" ? "" : alpha;
}

function normalizeThemePickerColor(value: string): string {
  return (themeColorToHex(value) ?? "#000000").slice(0, 7);
}

function themeHexToRgb(hex: string) {
  const numeric = Number.parseInt(normalizeThemePickerColor(hex).slice(1), 16);
  return [numeric >> 16, (numeric >> 8) & 255, numeric & 255] as const;
}

function themeRgbToHex(value: string): string | null {
  const normalized = value
    .trim()
    .replace(/^rgb\(\s*/i, "")
    .replace(/\s*\)$/, "");
  const channels = normalized
    .split(/[,\s]+/)
    .filter(Boolean)
    .map(Number);
  if (
    channels.length !== 3 ||
    channels.some(
      (channel) => !Number.isInteger(channel) || channel < 0 || channel > 255,
    )
  ) {
    return null;
  }

  return `#${channels.map((channel) => channel.toString(16).padStart(2, "0")).join("")}`;
}

function themeRgbValue(hex: string) {
  return themeHexToRgb(hex).join(", ");
}

/**
 * Create a batcher that commits at most one picked color per animation frame.
 * Local picker state updates immediately for a smooth thumb, while the parent
 * commit, which can regenerate a whole guided palette, waits for the frame.
 * The popover flushes it on close, drag surfaces on release, and the picker on
 * unmount, so the final frame is never lost.
 *
 * @returns The `schedule` and `flush` operations for one picker.
 */
function createFrameCommitter() {
  let pending: {
    readonly color: string;
    readonly commit: (color: string) => void;
  } | null = null;
  let frame: number | null = null;
  const flush = () => {
    if (frame !== null) {
      cancelAnimationFrame(frame);
      frame = null;
    }
    const next = pending;
    pending = null;
    next?.commit(next.color);
  };
  return {
    schedule(color: string, commit: (color: string) => void) {
      pending = { color, commit };
      if (frame !== null) return;
      frame = requestAnimationFrame(() => {
        frame = null;
        flush();
      });
    },
    flush,
  };
}

type FrameCommitter = ReturnType<typeof createFrameCommitter>;

function ThemeColorPickerPanel({
  label,
  value,
  committer,
  onChange,
}: {
  label: string;
  value: string;
  committer: FrameCommitter;
  onChange: (value: string) => void;
}) {
  const normalizedValue = normalizeThemePickerColor(value);
  const alphaSuffix = themePickerAlphaSuffix(value);
  const [hsv, setHsv] = useState(() => hexToHsv(normalizedValue));
  const [hexDraft, setHexDraft] = useState(normalizedValue);
  const [rgbDraft, setRgbDraft] = useState(() =>
    themeRgbValue(normalizedValue),
  );
  const isEditingTextRef = useRef(false);
  const currentColor = hsvToHex(hsv.h, hsv.s, hsv.v);
  const currentRgb = themeRgbValue(currentColor);

  useEffect(() => {
    // While a text field is focused, the incoming value may be the guided
    // editor's readability-adjusted echo of what is being typed; rewriting the
    // draft would fight the keystrokes. The swatch still tracks via hsv.
    if (!isEditingTextRef.current) {
      setHexDraft(normalizedValue);
      setRgbDraft(themeRgbValue(normalizedValue));
    }
    // Keep the current hue/saturation when the incoming value is just our own
    // change echoed back; hex → HSV is lossy for greys, white, and black.
    setHsv((current) =>
      hsvToHex(current.h, current.s, current.v) === normalizedValue
        ? current
        : hexToHsv(normalizedValue),
    );
  }, [normalizedValue]);

  const commitHsv = useCallback(
    (nextHsv: HsvColor) => {
      setHsv(nextHsv);
      const nextColor = hsvToHex(nextHsv.h, nextHsv.s, nextHsv.v);
      setHexDraft(nextColor);
      setRgbDraft(themeRgbValue(nextColor));
      committer.schedule(nextColor + alphaSuffix, onChange);
    },
    [alphaSuffix, committer, onChange],
  );

  const handleHexChange = (nextValue: string) => {
    setHexDraft(nextValue);
    if (!/^#[0-9a-f]{6}$/i.test(nextValue)) return;
    const nextHsv = hexToHsv(nextValue);
    setHsv(nextHsv);
    setRgbDraft(themeRgbValue(nextValue));
    onChange(nextValue.toLowerCase());
  };

  const handleRgbChange = (nextValue: string) => {
    setRgbDraft(nextValue);
    const nextColor = themeRgbToHex(nextValue);
    if (!nextColor) return;
    setHsv(hexToHsv(nextColor));
    setHexDraft(nextColor);
    // RGB cannot express alpha, so a commit keeps the incoming suffix just
    // like the plane and hue controls do.
    onChange(nextColor + alphaSuffix);
  };

  return (
    <div className="w-72">
      <div className="flex items-center justify-between border-b border-border/70 px-4 py-3">
        <div className="min-w-0">
          <p className="truncate text-xs font-semibold text-foreground">
            {label}
          </p>
          <p className="text-2xs text-muted-foreground">Choose a color</p>
        </div>
        <span
          className="size-7 shrink-0 rounded-full shadow-sm"
          style={{ backgroundColor: currentColor }}
        />
      </div>
      <div className="grid gap-3 px-3 pb-3 pt-3">
        <ColorSaturationValuePlane
          label={label}
          value={hsv}
          onChange={commitHsv}
          onInteractionEnd={committer.flush}
        />
        <ColorHueSlider
          label={`${label} hue`}
          value={hsv.h}
          onChange={(h) => commitHsv({ ...hsv, h })}
          onInteractionEnd={committer.flush}
        />
        <div className="grid grid-cols-[1fr_1.2fr] gap-2">
          <label className="grid min-w-0 gap-1">
            <span className="px-1 text-3xs font-semibold uppercase tracking-widest text-muted-foreground">
              HEX
            </span>
            <span className="flex min-w-0 items-center gap-2 rounded-lg border border-input bg-background px-2 focus-within:border-ring">
              <span
                className="size-3.5 shrink-0 rounded-full"
                style={{ backgroundColor: currentColor }}
              />
              <input
                aria-label={t("{label} picker hex value", { label })}
                className="h-8 min-w-0 flex-1 bg-transparent font-mono text-xs text-foreground outline-none"
                onBlur={() => {
                  isEditingTextRef.current = false;
                  setHexDraft(currentColor);
                  setRgbDraft(currentRgb);
                }}
                onChange={(event) => handleHexChange(event.currentTarget.value)}
                onFocus={() => {
                  isEditingTextRef.current = true;
                }}
                spellCheck={false}
                value={hexDraft}
              />
            </span>
          </label>
          <label className="grid min-w-0 gap-1">
            <span className="px-1 text-3xs font-semibold uppercase tracking-widest text-muted-foreground">
              RGB
            </span>
            <span className="flex min-w-0 items-center rounded-lg border border-input bg-background px-2 focus-within:border-ring">
              <input
                aria-label={t("{label} picker RGB value", { label })}
                className="h-8 min-w-0 flex-1 bg-transparent font-mono text-xs text-foreground outline-none"
                onBlur={() => {
                  isEditingTextRef.current = false;
                  setHexDraft(currentColor);
                  setRgbDraft(currentRgb);
                }}
                onChange={(event) => handleRgbChange(event.currentTarget.value)}
                onFocus={() => {
                  isEditingTextRef.current = true;
                }}
                spellCheck={false}
                value={rgbDraft}
              />
            </span>
          </label>
        </div>
      </div>
    </div>
  );
}

function ThemeColorPicker({
  label,
  value,
  onChange,
  onInteract,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  onInteract?: () => void;
}) {
  const [committer] = useState(createFrameCommitter);
  useEffect(() => committer.flush, [committer]);
  return (
    <Popover
      onOpenChange={(isOpen) => {
        if (!isOpen) committer.flush();
      }}
    >
      <Tooltip>
        <TooltipTrigger
          render={
            <PopoverTrigger
              render={
                <button
                  aria-label={t("Choose {label} color", { label })}
                  className="relative flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-full border border-foreground/30 transition-transform hover:scale-105 focus-visible:outline-2 focus-visible:outline-solid focus-visible:-outline-offset-2 focus-visible:outline-ring"
                  onFocus={onInteract}
                  onPointerDown={onInteract}
                  type="button"
                >
                  <span
                    className="absolute inset-0 rounded-full shadow-sm"
                    style={{ backgroundColor: value }}
                  />
                </button>
              }
            />
          }
        />
        <TooltipPopup side="top">{`Choose ${label} color`}</TooltipPopup>
      </Tooltip>
      <PopoverPopup
        align="end"
        padding="none"
        data-theme-editor-panel=""
        side="bottom"
        sideOffset={10}
      >
        <ThemeColorPickerPanel
          committer={committer}
          label={label}
          onChange={onChange}
          value={value}
        />
      </PopoverPopup>
    </Popover>
  );
}

export const ThemeColorField = memo(function ThemeColorField({
  role,
  value,
  onChange,
  onSelect,
  onToggleSelected,
  selected = false,
  label: customLabel,
}: {
  role: ThemeColorRole;
  value: string;
  onChange: (role: ThemeColorRole, value: string) => void;
  onSelect?: (role: ThemeColorRole) => void;
  onToggleSelected?: (role: ThemeColorRole) => void;
  selected?: boolean;
  label?: string;
}) {
  useI18n();
  const label = customLabel ?? getThemeRoleLabel(role);
  const isColorValue = isThemeColor(value);
  const swatchValue = isColorValue ? value : "#000000";
  const editorValue = value.trim().toLowerCase().startsWith("oklch(")
    ? (themeColorToHex(value) ?? value)
    : value;

  return (
    <div
      className={cn(
        "flex min-h-11 min-w-0 items-center gap-2 rounded-lg px-2 py-1.5 transition-[background-color,box-shadow]",
        selected && "bg-accent/60 inset-ring inset-ring-ring",
      )}
      data-theme-color-role={role}
    >
      <Tooltip>
        <TooltipTrigger
          render={
            <button
              aria-label={
                selected
                  ? t("Hide {label} usage", { label })
                  : t("Show {label} usage", { label })
              }
              aria-pressed={selected}
              className="flex min-w-0 flex-1 cursor-pointer items-center rounded-md text-left text-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
              onClick={() => onToggleSelected?.(role)}
              type="button"
            >
              <span className="min-w-0 flex-1 truncate">{label}</span>
            </button>
          }
        />
        <TooltipPopup side="top">
          {selected
            ? t("Hide where {label} is used", { label })
            : t("Show where {label} is used", { label })}
        </TooltipPopup>
      </Tooltip>
      <div className="ml-auto flex shrink-0 items-center gap-2">
        <ThemeColorPicker
          label={label}
          onChange={(nextValue) => onChange(role, nextValue)}
          onInteract={() => onSelect?.(role)}
          value={swatchValue}
        />
        <Input
          aria-invalid={!isColorValue}
          aria-label={t("{label} hex value", { label })}
          className="w-28 shrink-0"
          font="mono"
          id={`${role}-hex`}
          nativeInput
          onChange={(event) => onChange(role, event.currentTarget.value)}
          onFocus={() => onSelect?.(role)}
          onPointerDown={() => onSelect?.(role)}
          size="compact"
          value={editorValue}
        />
      </div>
    </div>
  );
});
