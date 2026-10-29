import {
  type EnvironmentId,
  ProviderConsumeResetCreditInput,
  ServerProvider,
  ServerProviderResetCredits,
  ServerProviderUsageWindow,
} from "@t3tools/contracts";

import {
  type LimitPace,
  paceOf,
  paceRemainingPercent,
  remainingPercent,
} from "@t3tools/shared/usageLimits";
import { GaugeIcon, TrendingDownIcon, TrendingUpIcon } from "lucide-react";
import { Fragment, useLayoutEffect, useState } from "react";

import { observeResize } from "~/lib/observeResize";
import { usePrimarySettings } from "../src/usage/host";
import { formatDuration, formatResetsIn, msg, t } from "../src/i18n";

import { formatUpcomingTimestamp } from "../src/usage/host";
import {
  AlertDialog,
  AlertDialogClose,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogPopup,
  AlertDialogTitle,
} from "./ui/alert-dialog";
import {
  Button,
  InlineButton,
} from "../../tools/parity/upstream/apps/web/src/components/ui/button";
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

import { PROVIDER_PRESENTATION } from "./usageProviders";

const PACE: Record<
  LimitPace,
  { readonly label: string; readonly icon: typeof GaugeIcon }
> = {
  ahead: {
    label: msg("Ahead of pace: spending faster than the window elapses"),
    icon: TrendingUpIcon,
  },
  on: { label: msg("On pace with the window"), icon: GaugeIcon },
  under: {
    label: msg("Under pace: headroom left for the rest of the window"),
    icon: TrendingDownIcon,
  },
};

/** The series colour the cost chart uses for this driver, so the two views read as one. */
export function barColor(driver: ServerProvider["driver"]): string {
  const presentation = Object.values(PROVIDER_PRESENTATION).find(
    (entry) => entry.driverKind === driver,
  );
  return presentation?.color ?? "var(--foreground)";
}

/** Pace as a glyph with the words on hover, tap or keyboard press. */
export function PaceIcon({ pace }: { readonly pace: LimitPace }) {
  const Icon = PACE[pace].icon;
  return (
    <Popover>
      <PopoverTrigger
        openOnHover
        render={<InlineButton tone="muted" />}
        aria-label={t(PACE[pace].label)}
      >
        <Icon className="size-3.5" aria-hidden />
      </PopoverTrigger>
      <PopoverPopup side="top" tooltipStyle>
        {t(PACE[pace].label)}
      </PopoverPopup>
    </Popover>
  );
}

/**
 * Styles for an even-pace tick drawn in a transparent gap cut from the bar.
 *
 * The tick is clamped so it stays inside the bar at 0% and 100%. The gap is a
 * clip-path slot rather than a mask, so the bar's rounded ends keep their
 * antialiasing; the polygon reaches 1px past the box so only the slot edges clip.
 * `gappedTrack` paints the track on an element that also draws focus rings,
 * which a clip would cut.
 *
 * @param timeLeft - Share of the window still to run, or null without a clock.
 * @returns The tick, gap and gapped-track styles, or null when there is no mark.
 */
function paceMarkStyles(timeLeft: number | null) {
  if (timeLeft === null) return null;
  const at = `clamp(3px, ${timeLeft}%, calc(100% - 3px))`;
  const [left, right] = [`calc(${at} - 3px)`, `calc(${at} + 3px)`];
  const [near, far] = ["-1px", "calc(100% + 1px)"];
  return {
    tick: { left: at },
    gap: {
      clipPath: `polygon(${near} ${near}, ${left} ${near}, ${left} ${far}, ${right} ${far}, ${right} ${near}, ${far} ${near}, ${far} ${far}, ${near} ${far})`,
    },
    gappedTrack: {
      backgroundColor: "transparent",
      backgroundImage: `linear-gradient(to right, var(--color-muted) ${left}, transparent ${left}, transparent ${right}, var(--color-muted) ${right})`,
    },
  };
}

/**
 * Pace mark styles for the bar given the returned ref. Below 6px there is no
 * room for the gap and tick, so the bar draws neither.
 *
 * @param timeLeft - Share of the window still to run, or null without a clock.
 * @returns A ref callback for the bar and its pace mark styles, if any.
 */
export function usePaceMark(timeLeft: number | null) {
  const [bar, setBar] = useState<HTMLElement | null>(null);
  const [isRoomy, setIsRoomy] = useState(false);
  useLayoutEffect(() => {
    if (!bar) return;
    const measure = () => setIsRoomy(bar.getBoundingClientRect().width >= 6);
    measure();
    return observeResize(bar, measure);
  }, [bar]);
  return [setBar, isRoomy ? paceMarkStyles(timeLeft) : null] as const;
}

/**
 * One window as a full-width bar from the moment it opened to its reset.
 * The fill is the share of quota left; the tick in a gap is how much of the
 * window is left, which is where even spending would have put the fill.
 * Hover for the exact figures and reset time.
 */
function WindowBar({
  color,
  window,
  now,
}: {
  readonly color: string;
  readonly window: ServerProviderUsageWindow;
  readonly now: number;
}) {
  const timestampFormat = usePrimarySettings(
    (settings) => settings.timestampFormat,
  );
  const remaining = remainingPercent(window);
  const timeLeft = paceRemainingPercent(window, now);
  const [barRef, paceMark] = usePaceMark(timeLeft);
  const resetsIn = formatResetsIn(window, now);
  const resetsAt = window.resetsAt
    ? formatUpcomingTimestamp(window.resetsAt, timestampFormat, now)
    : null;
  const summary = `${t(window.label)}: ${t("{percent}% left", { percent: remaining })}${
    timeLeft === null
      ? ""
      : `, ${t("{percent}% of the window left", { percent: timeLeft })}`
  }${resetsIn ? `, ${resetsIn}` : ""}`;

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <div
            ref={barRef}
            role="img"
            aria-label={summary}
            tabIndex={0}
            className="relative h-6 cursor-default rounded-full outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          />
        }
      >
        <div className="absolute inset-0" style={paceMark?.gap}>
          <div className="absolute inset-x-0 inset-y-1.5 rounded-full bg-muted" />
          {remaining > 0 ? (
            <div
              className="absolute inset-y-1.5 left-0 rounded-full"
              style={{ width: `${remaining}%`, backgroundColor: color }}
            />
          ) : null}
        </div>
        {paceMark ? (
          <span
            aria-hidden
            className="absolute inset-y-1 w-0.5 -translate-x-1/2 rounded-full bg-foreground"
            style={paceMark.tick}
          />
        ) : null}
      </TooltipTrigger>
      <TooltipPopup side="top">
        <div className="flex flex-col gap-0.5">
          <span className="text-foreground">
            {t("{percent}% left", { percent: remaining })}
            {timeLeft !== null
              ? ` · ${t("{percent}% of the window left", { percent: timeLeft })}`
              : ""}
          </span>
          {timeLeft !== null ? (
            <span className="text-muted-foreground">
              {t("The line is where even spending would be.")}
            </span>
          ) : null}
          {resetsAt ? (
            <span className="text-muted-foreground">
              {t("Resets {time}", { time: resetsAt })}
              {resetsIn ? ` · ${resetsIn}` : ""}
            </span>
          ) : null}
        </div>
      </TooltipPopup>
    </Tooltip>
  );
}

/**
 * One account's windows as rows: label and percent, bar, pace and countdown.
 * Compact rows fit the composer panel with narrower columns.
 */
export function LimitWindows({
  driver,
  windows,
  now,
  compact = false,
}: {
  readonly driver: ServerProvider["driver"];
  readonly windows: ReadonlyArray<ServerProviderUsageWindow>;
  readonly now: number;
  readonly compact?: boolean;
}) {
  const color = barColor(driver);
  return (
    <div
      className={
        compact
          ? "grid grid-cols-[minmax(0,9rem)_minmax(3rem,1fr)_auto] gap-x-3 gap-y-0.5"
          : "grid grid-cols-[11rem_minmax(0,1fr)_7rem] gap-x-4 gap-y-1"
      }
    >
      {windows.map((window) => {
        const pace = paceOf(window, now);
        const resetsIn = formatResetsIn(window, now);
        return (
          <Fragment key={window.id}>
            <span className="flex min-w-0 items-center gap-2 text-xs">
              <span className="truncate text-muted-foreground">
                {t(window.label)}
              </span>
              <span className="ms-auto shrink-0 font-medium text-foreground tabular-nums">
                {t("{percent}% left", { percent: remainingPercent(window) })}
              </span>
            </span>
            <WindowBar color={color} window={window} now={now} />
            <span className="flex items-center gap-2 text-xs whitespace-nowrap text-muted-foreground tabular-nums">
              {pace ? <PaceIcon pace={pace} /> : null}
              <span className="ms-auto shrink-0">{resetsIn ?? ""}</span>
            </span>
          </Fragment>
        );
      })}
    </div>
  );
}

import { useResetCredit } from "../src/usage/host";
export { useResetCredit } from "../src/usage/host";

/**
 * The confirm for a redeem. Redeeming spends a credit the provider granted the
 * user, so it never fires on a bare click. Mount it outside any popover that
 * holds the button: dialogs stack under popovers, and closing the popover
 * would unmount a dialog rendered inside it.
 */
export function ResetCreditDialog({
  open,
  onOpenChange,
  onConfirm,
}: {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly onConfirm: () => void;
}) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogPopup>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("Use a reset credit?")}</AlertDialogTitle>
          <AlertDialogDescription>
            {t(
              "This redeems one credit on your account and clears the current rate-limit windows. It cannot be undone.",
            )}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogClose render={<Button variant="outline" />}>
            {t("Cancel")}
          </AlertDialogClose>
          <Button onClick={onConfirm}>{t("Use credit")}</Button>
        </AlertDialogFooter>
      </AlertDialogPopup>
    </AlertDialog>
  );
}

/** `2 reset credits banked · next expires in 27d 23h`, or the short form for a popover. */
export function resetCreditsSummary(
  credits: ServerProviderResetCredits,
  now: number,
  compact = false,
): string {
  const expiresIn = credits.nextExpiresAt
    ? formatDuration(Date.parse(credits.nextExpiresAt) - now)
    : null;
  if (credits.availableCount === 0) return t("No reset credits banked");
  const count = credits.availableCount;
  if (compact)
    return `${t("{count} banked", { count })}${expiresIn ? ` · ${t("expires in {duration}", { duration: expiresIn })}` : ""}`;
  return `${count === 1 ? t("{count} reset credit banked", { count }) : t("{count} reset credits banked", { count })}${
    expiresIn
      ? ` · ${t("next expires in {duration}", { duration: expiresIn })}`
      : ""
  }`;
}

/** Banked reset credits with the redeem button and its confirm, self-contained. */
export function ResetCredits({
  environmentId,
  input,
  credits,
  now,
}: {
  readonly environmentId: EnvironmentId;
  readonly input: ProviderConsumeResetCreditInput;
  readonly credits: ServerProviderResetCredits;
  readonly now: number;
}) {
  const { confirming, setConfirming, busy, redeem } = useResetCredit(
    environmentId,
    input,
  );
  if (credits.availableCount === 0) return null;
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
      <span className="tabular-nums">{resetCreditsSummary(credits, now)}</span>
      {credits.availableCount > 0 ? (
        <Button
          size="xs"
          variant="outline"
          disabled={busy}
          onClick={() => setConfirming(true)}
        >
          {busy ? t("Using…") : t("Use reset")}
        </Button>
      ) : null}
      <ResetCreditDialog
        open={confirming}
        onOpenChange={setConfirming}
        onConfirm={() => void redeem()}
      />
    </div>
  );
}
