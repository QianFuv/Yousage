import {
  CHATGPT_USAGE_URL,
  collectLimitAccounts,
  collectExternalUsageLinks,
  collectLimitNotices,
  collectLimitPools,
  displayLimitWindows,
  type LimitAccount,
  type LimitPool,
  type LimitPoolMember,
  type LimitPoolWindow,
  remainingPercent,
} from "@t3tools/shared/usageLimits";
import { AlertTriangleIcon, ExternalLinkIcon, TicketIcon } from "lucide-react";
import { formatUsd } from "@t3tools/shared/usageFormat";
import { type ReactNode, useState } from "react";
import { useIsReadOnly } from "../src/web/readOnly";

import {
  basisText,
  ConfidenceDot,
  confidenceLabel,
  type CycleValueIndex,
  CycleValueBadge,
  cycleValueNotice,
  memberCycleValue,
  historyValueText,
  precisionText,
  predictionConfidence,
  predictionPhrase,
  rangeText,
} from "../src/usage/CycleValue";
import type { CycleValue } from "../src/usage/data";

import { ensureLocalApi } from "../src/usage/host";
import { usePrimarySettings } from "../src/usage/host";
import { cn } from "../../tools/parity/src/bridges/utils";
import { formatResetDuration, formatResetsIn, t } from "../src/i18n";
import { formatUpcomingTimestamp } from "../src/usage/host";
import { ProviderInstanceIcon } from "./ProviderInstanceIcon";
import { getDriverOption } from "../src/usage/host";
import { RedactedSensitiveText } from "../src/usage/host";
import { Button } from "../../tools/parity/upstream/apps/web/src/components/ui/button";
import { OpenAI } from "../../tools/parity/upstream/apps/web/src/components/Icons";
import { Alert, AlertTitle } from "./ui/alert";
import {
  Popover,
  PopoverPopup,
  PopoverTrigger,
} from "../../tools/parity/upstream/apps/web/src/components/ui/popover";
import {
  PaceIcon,
  ResetCreditDialog,
  barColor,
  resetCreditsSummary,
  useResetCredit,
} from "./UsageLimits";

/**
 * Screen-reader phrase for banked reset credits.
 *
 * @param credits - Number of banked credits.
 * @returns The localized phrase.
 */
function creditsBanked(credits: number): string {
  return credits === 1
    ? t("{count} reset credit banked", { count: credits })
    : t("{count} reset credits banked", { count: credits });
}

/** `someone@example.com` → `SE`: enough to tell accounts apart, too little to identify one. */
function accountInitials(email: string): string {
  const [local = "", domain = ""] = email.split("@");
  return `${local[0] ?? ""}${domain[0] ?? ""}`.toUpperCase() || "?";
}

/** A stable hue per email, so the same account gets the same chip on every visit. */
function accountHue(email: string): number {
  let hash = 0;
  for (let index = 0; index < email.length; index += 1) {
    hash = (hash * 31 + email.charCodeAt(index)) | 0;
  }
  return Math.abs(hash) % 360;
}

/** The two-letter chip for an email, coloured by a stable hue per address. */
function AccountChip({ email }: { readonly email: string }) {
  const hue = accountHue(email);
  return (
    <span
      role="img"
      aria-label={t("Account {initials}", { initials: accountInitials(email) })}
      className="inline-flex size-4 shrink-0 items-center justify-center rounded-full text-3xs leading-none font-semibold"
      style={{
        backgroundColor: `oklch(0.85 0.08 ${hue})`,
        color: `oklch(0.35 0.1 ${hue})`,
      }}
    >
      {accountInitials(email)}
    </span>
  );
}

/**
 * The same mark the model picker uses for a native instance (provider glyph,
 * initials badge, accent); hub accounts have no instance, so they get the chip.
 */
function AccountAvatar({
  account,
  className,
}: {
  readonly account: LimitAccount;
  readonly className?: string;
}) {
  if (account.redeem) {
    return (
      <ProviderInstanceIcon
        driverKind={account.driver}
        displayName={
          account.displayName ??
          getDriverOption(account.driver)?.label ??
          String(account.driver)
        }
        accentColor={account.accentColor}
        showBadge={Boolean(account.displayName)}
        indicatorBackground="var(--popover)"
        className={cn("size-5", className)}
        iconClassName="size-4 text-foreground/80"
      />
    );
  }
  return account.email ? <AccountChip email={account.email} /> : null;
}

/**
 * Who an account is, without printing the email: the instance name when there
 * is one, else a two-letter chip. The address itself is revealed on demand in
 * the segment's popover.
 */
function AccountName({
  account,
  className,
}: {
  readonly account: LimitAccount;
  readonly className?: string;
}) {
  if (account.displayName)
    return <span className={className}>{account.displayName}</span>;
  if (account.email) {
    return (
      <span className={cn("inline-flex min-w-0 items-center", className)}>
        <AccountChip email={account.email} />
      </span>
    );
  }
  return (
    <span className={className}>
      {getDriverOption(account.driver)?.label ?? String(account.driver)}
    </span>
  );
}

function Row({
  label,
  children,
}: {
  readonly label: string;
  readonly children: ReactNode;
}) {
  return (
    <div className="grid grid-cols-[4.5rem_minmax(0,1fr)] gap-x-3">
      <span className="text-muted-foreground">{label}</span>
      <span className="min-w-0 text-foreground tabular-nums">{children}</span>
    </div>
  );
}

/**
 * The cycle's API-equivalent value in an account popover: estimate, spend,
 * remaining value, and how far the prediction can be trusted.
 */
function CycleValueSection({
  value,
  now,
}: {
  readonly value: CycleValue;
  readonly now: number;
}) {
  const timestampFormat = usePrimarySettings(
    (settings) => settings.timestampFormat,
  );
  const confidence = predictionConfidence(value);
  const basis = basisText(value);
  const earlier = value.history.slice(0, 3);
  const range = rangeText(value.valueLowUsd, value.valueHighUsd);
  const remaining = rangeText(value.remainingLowUsd, value.remainingHighUsd);
  const notice = cycleValueNotice(value);
  const isActive = value.status !== "noCycle";
  return (
    <div className="flex flex-col gap-1 border-t border-border/60 pt-2.5">
      {value.valueUsd !== undefined || range ? (
        <Row label={t("Cycle value")}>
          {value.valueUsd !== undefined
            ? `≈ ${formatUsd(value.valueUsd)}`
            : range}
          {value.valueUsd !== undefined && range ? (
            <span className="text-muted-foreground"> · {range}</span>
          ) : null}
        </Row>
      ) : null}
      {isActive ? (
        <Row label={t("Spent")}>
          {t("{spent} for {low}–{high}% of the quota", {
            spent: formatUsd(value.spentUsd),
            low: value.usedLowPercent,
            high: value.usedHighPercent,
          })}
        </Row>
      ) : null}
      {remaining ? <Row label={t("Remaining")}>{remaining}</Row> : null}
      {confidence ? (
        <Row label={t("Confidence")}>
          <span className="inline-flex items-center gap-1.5">
            <ConfidenceDot confidence={confidence} />
            {confidenceLabel(confidence)}
          </span>
          <span className="block text-muted-foreground">
            {precisionText(value)}
          </span>
        </Row>
      ) : null}
      {basis ? <Row label={t("Basis")}>{basis}</Row> : null}
      {earlier.length > 0 ? (
        <Row label={t("Earlier")}>
          {earlier.map((entry) => (
            <span key={entry.resetsAt} className="flex gap-1.5">
              <span className="text-muted-foreground">
                {formatUpcomingTimestamp(entry.resetsAt, timestampFormat, now)}
              </span>
              <span>{historyValueText(entry)}</span>
            </span>
          ))}
        </Row>
      ) : null}
      {notice ? <p className="text-muted-foreground">{notice}</p> : null}
      {isActive ? (
        <p className="text-muted-foreground">
          {t("Only usage recorded on this device is counted.")}
          {value.unpricedRecords > 0
            ? ` ${t("{count} unpriced records are excluded, so the value may be higher.", { count: value.unpricedRecords })}`
            : ""}
        </p>
      ) : null}
    </div>
  );
}

/**
 * Everything about one account in one window: plan, where it is signed in,
 * the email on request, reset time and share of the pool it restores, and the
 * reset-credit action. Opens on hover for a glance, on click to act.
 */
function SegmentPopover({
  account,
  window,
  reset,
  now,
  cycleValue,
  redeem,
  onRedeem,
}: {
  readonly account: LimitAccount;
  readonly window: LimitPoolMember["window"];
  readonly reset: LimitPoolWindow["resets"][number] | undefined;
  readonly now: number;
  readonly cycleValue: CycleValue | undefined;
  /** Redeem state owned by the segment, since the confirm lives outside this popover. */
  readonly redeem: ReturnType<typeof useResetCredit> | null;
  readonly onRedeem: () => void;
}) {
  const timestampFormat = usePrimarySettings(
    (settings) => settings.timestampFormat,
  );
  const remaining = remainingPercent(window);
  const resetDuration = formatResetDuration(window, now);
  const where =
    account.environments.length > 0
      ? account.environments.map((environment) => environment.label).join(", ")
      : account.sourceLabel;
  const credits =
    redeem && account.limits.resetCredits?.availableCount
      ? account.limits.resetCredits
      : null;
  return (
    <div className="flex w-72 max-w-[calc(100vw-3rem)] flex-col gap-2.5 text-xs">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="flex items-center gap-2 text-sm font-medium text-foreground">
          <AccountAvatar account={account} />
          <span className="truncate">
            {account.displayName ??
              getDriverOption(account.driver)?.label ??
              account.driver}
          </span>
        </span>
        {account.email ? (
          <RedactedSensitiveText
            value={account.email}
            ariaLabel={t("Toggle account email visibility")}
            revealTooltip={t("Click to reveal email")}
            hideTooltip={t("Click to hide email")}
            className="w-fit"
          />
        ) : null}
      </div>
      <div className="flex flex-col gap-1 border-t border-border/60 pt-2.5">
        {account.plan ? <Row label={t("Plan")}>{account.plan}</Row> : null}
        {where ? (
          <Row
            label={account.environments.length > 0 ? t("Signed in") : t("Via")}
          >
            {where}
          </Row>
        ) : null}
      </div>
      <div className="flex flex-col gap-1 border-t border-border/60 pt-2.5">
        <Row label={t("Left")}>{remaining}%</Row>
        {window.resetsAt ? (
          <Row label={t("Resets")}>
            {formatUpcomingTimestamp(window.resetsAt, timestampFormat, now)}
            {resetDuration
              ? ` · ${t("in {duration}", { duration: resetDuration })}`
              : ""}
          </Row>
        ) : null}
        {reset && reset.restoresPercent > 0 ? (
          <Row label={t("Restores")}>
            {t("+{percent}% of pool", { percent: reset.restoresPercent })}
          </Row>
        ) : null}
      </div>
      {cycleValue ? <CycleValueSection value={cycleValue} now={now} /> : null}
      {credits && redeem ? (
        <div className="border-t border-border/60 pt-2.5 text-muted-foreground">
          <span className="flex items-center gap-3">
            <span className="tabular-nums">
              {resetCreditsSummary(credits, now, true)}
            </span>
            <Button
              size="xs"
              variant="outline"
              disabled={redeem.busy}
              className="ms-auto"
              onClick={onRedeem}
            >
              {redeem.busy ? t("Using…") : t("Use reset")}
            </Button>
          </span>
        </div>
      ) : null}
    </div>
  );
}

/**
 * One account's share of one pooled window: the segment, its popover, and the
 * reset confirm. The confirm is a sibling of the popover, not a child: dialogs
 * stack under popovers, and the popover closes as the confirm opens.
 */
function PoolSegment({
  account,
  window,
  reset,
  color,
  now,
  index,
  showAccountName,
  cycleValue,
}: {
  readonly account: LimitAccount;
  readonly window: LimitPoolMember["window"];
  readonly reset: LimitPoolWindow["resets"][number] | undefined;
  readonly color: string;
  readonly now: number;
  readonly cycleValue: CycleValue | undefined;
  /** 1-based position in the bar, shown on the strip and its legend row to tie them together. */
  readonly index: number;
  readonly showAccountName: boolean;
}) {
  const [open, setOpen] = useState(false);
  const isReadOnly = useIsReadOnly();
  const remaining = remainingPercent(window);
  const resetsIn = formatResetsIn(window, now);
  const resetDuration = formatResetDuration(window, now);
  const credits = account.limits.resetCredits?.availableCount ?? 0;
  const prediction = cycleValue ? predictionPhrase(cycleValue) : null;
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        openOnHover
        render={
          <button
            type="button"
            style={{ gridColumn: index, gridRow: 1 }}
            aria-label={`${account.displayName ?? (account.email ? accountInitials(account.email) : account.driver)}: ${t("{percent}% left", { percent: remaining })}${resetsIn ? `, ${resetsIn}` : ""}${credits ? `, ${creditsBanked(credits)}` : ""}${prediction ? `, ${prediction}` : ""}`}
            className="relative h-5 min-w-0 cursor-pointer overflow-hidden rounded-md bg-muted text-start outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background data-[popup-open]:ring-1 data-[popup-open]:ring-inset data-[popup-open]:ring-border @2xl/pool:h-8"
          />
        }
      >
        {/* Translucent so the label reads over the fill for any provider colour and theme. */}
        <div
          aria-hidden
          className="absolute inset-y-0 left-0 rounded-md opacity-35"
          style={{ width: `${remaining}%`, backgroundColor: color }}
        />
        {/* The spent share is hatched, not blank: it is what the countdown restores. */}
        {remaining < 100 && reset ? (
          <div
            aria-hidden
            className="absolute inset-y-0 right-0 opacity-20"
            style={{
              width: `${100 - remaining}%`,
              backgroundImage: `repeating-linear-gradient(135deg, ${color} 0 1px, transparent 1px 5px)`,
            }}
          />
        ) : null}
        {showAccountName ? (
          <span
            aria-hidden
            className="absolute inset-0 flex items-center justify-center text-3xs leading-none font-semibold text-foreground/80 tabular-nums @2xl/pool:hidden"
          >
            {index}
          </span>
        ) : null}
        <div className="relative hidden h-full min-w-0 items-center gap-1.5 px-2 text-xs @2xl/pool:flex">
          {showAccountName ? (
            <AccountName
              account={account}
              className="min-w-0 truncate font-medium text-foreground"
            />
          ) : null}
          <span className="shrink-0 font-semibold text-foreground tabular-nums">
            {remaining}%
          </span>
          {cycleValue ? <CycleValueBadge value={cycleValue} /> : null}
          {/* Countdown and badge get their own plate: fill and hatching run under them otherwise. */}
          <span className="ms-auto flex shrink-0 items-center gap-1.5 rounded-sm bg-background/85 px-1.5 py-0.5 text-2xs text-foreground tabular-nums">
            {resetDuration ? `↻ ${resetDuration}` : ""}
            {credits ? (
              <>
                {resetsIn ? (
                  <span aria-hidden className="text-muted-foreground">
                    ·
                  </span>
                ) : null}
                <span
                  aria-hidden
                  className="inline-flex items-center gap-0.5 font-semibold"
                >
                  <TicketIcon className="size-3" aria-hidden />
                  {credits}
                </span>
              </>
            ) : null}
          </span>
        </div>
      </PopoverTrigger>
      <LegendRow
        account={account}
        window={window}
        color={color}
        now={now}
        index={index}
        showAccountName={showAccountName}
        cycleValue={cycleValue}
      />
      {account.redeem && !isReadOnly ? (
        <RedeemableSegmentPopup
          account={account}
          window={window}
          reset={reset}
          now={now}
          redeemAt={account.redeem}
          closePopover={() => setOpen(false)}
          cycleValue={cycleValue}
        />
      ) : (
        <PopoverPopup side="top" sideOffset={6}>
          <SegmentPopover
            account={account}
            window={window}
            reset={reset}
            now={now}
            cycleValue={cycleValue}
            redeem={null}
            onRedeem={() => {}}
          />
        </PopoverPopup>
      )}
    </Popover>
  );
}

/**
 * Below the strip at narrow widths: one row per account in bar order, carrying
 * the text the segment has no room for. Tapping a row opens the same popover
 * as its segment, so the two are one control with two handles. Like the wide
 * segment label, a single-account pool shows neither the index nor the name.
 */
function LegendRow({
  account,
  window,
  color,
  now,
  index,
  showAccountName,
  cycleValue,
}: {
  readonly account: LimitAccount;
  readonly window: LimitPoolMember["window"];
  readonly color: string;
  readonly now: number;
  readonly index: number;
  readonly showAccountName: boolean;
  readonly cycleValue: CycleValue | undefined;
}) {
  const remaining = remainingPercent(window);
  const resetsIn = formatResetDuration(window, now);
  const resetDuration = resetsIn;
  const credits = account.limits.resetCredits?.availableCount ?? 0;
  return (
    <PopoverTrigger
      style={{ gridColumn: "1 / -1", gridRow: index + 1 }}
      render={<Button variant="ghost" size="compact" />}
      className="min-w-0 sm:text-xs @2xl/pool:hidden"
    >
      {showAccountName ? (
        <>
          <span className="relative inline-flex size-4 shrink-0 items-center justify-center rounded-sm text-3xs leading-none font-semibold text-foreground/80 tabular-nums">
            <span
              aria-hidden
              className="absolute inset-0 rounded-sm opacity-35"
              style={{ backgroundColor: color }}
            />
            <span className="sr-only">{t("Segment")} </span>
            <span className="relative">{index}</span>
          </span>
          <AccountName
            account={account}
            className="min-w-0 truncate font-medium text-foreground"
          />
        </>
      ) : null}
      <span className="shrink-0 font-semibold text-foreground tabular-nums">
        {remaining}%
      </span>
      {cycleValue ? <CycleValueBadge value={cycleValue} /> : null}
      <span className="ms-auto flex shrink-0 items-center gap-1.5 text-2xs text-muted-foreground tabular-nums">
        {resetDuration ? `↻ ${resetDuration}` : ""}
        {credits ? (
          <>
            {resetsIn ? <span aria-hidden>·</span> : null}
            <span
              aria-hidden
              className="inline-flex items-center gap-0.5 font-semibold text-foreground"
            >
              <TicketIcon className="size-3" aria-hidden />
              {credits}
            </span>
            <span className="sr-only">{creditsBanked(credits)}</span>
          </>
        ) : null}
      </span>
    </PopoverTrigger>
  );
}

/** Split out so the redeem hook only runs for accounts that can redeem. */
function RedeemableSegmentPopup({
  account,
  window,
  reset,
  now,
  redeemAt,
  closePopover,
  cycleValue,
}: {
  readonly account: LimitAccount;
  readonly window: LimitPoolMember["window"];
  readonly reset: LimitPoolWindow["resets"][number] | undefined;
  readonly now: number;
  readonly redeemAt: NonNullable<LimitAccount["redeem"]>;
  readonly closePopover: () => void;
  readonly cycleValue: CycleValue | undefined;
}) {
  const redeem = useResetCredit(redeemAt.environmentId, redeemAt.input);
  return (
    <>
      <PopoverPopup side="top" sideOffset={6}>
        <SegmentPopover
          account={account}
          window={window}
          reset={reset}
          now={now}
          cycleValue={cycleValue}
          redeem={redeem}
          onRedeem={() => {
            closePopover();
            redeem.setConfirming(true);
          }}
        />
      </PopoverPopup>
      <ResetCreditDialog
        open={redeem.confirming}
        onOpenChange={redeem.setConfirming}
        onConfirm={() => void redeem.redeem()}
      />
      {/* The popover closed before the confirm, so the outcome needs a home outside it. */}
      {redeem.status ? (
        <span
          role="status"
          className="col-span-full text-xs text-muted-foreground"
        >
          <AccountName
            account={account}
            className="font-medium text-foreground"
          />{" "}
          {redeem.status}
        </span>
      ) : null}
    </>
  );
}

/**
 * One pooled window as equal-width segments, one per account, each filled by
 * the share of that account's quota still open. Equal widths are honest: every
 * account contributes the same share of the pool, whatever its plan.
 *
 * Wide, each segment carries its own label. Narrow, the bar is a bare strip
 * and a legend below lists the accounts in the same order; both open the
 * same popover.
 */
function PoolBar({
  pool,
  color,
  now,
  cycleValues,
}: {
  readonly pool: LimitPoolWindow;
  readonly color: string;
  readonly now: number;
  readonly cycleValues: CycleValueIndex;
}) {
  const restores = new Map(
    pool.resets.map((reset) => [reset.member.account.key, reset]),
  );
  return (
    <div className="@container/pool min-w-0">
      <div
        className="grid gap-x-1 gap-y-1"
        style={{
          gridTemplateColumns: `repeat(${pool.columns.length}, minmax(0, 1fr))`,
        }}
      >
        {pool.columns.map((member, position) =>
          member.window ? (
            <PoolSegment
              key={member.account.key}
              account={member.account}
              window={member.window}
              reset={restores.get(member.account.key)}
              color={color}
              now={now}
              index={position + 1}
              showAccountName={pool.columns.length > 1}
              cycleValue={memberCycleValue(cycleValues, {
                account: member.account,
                window: member.window,
              })}
            />
          ) : null,
        )}
      </div>
    </div>
  );
}

/**
 * Big pooled number and the segment bar. Accounts keep the same column across
 * windows; each segment's popover shows its own reset time and share restored.
 */
function PoolWindowCard({
  pool,
  color,
  now,
  cycleValues,
}: {
  readonly pool: LimitPoolWindow;
  readonly color: string;
  readonly now: number;
  readonly cycleValues: CycleValueIndex;
}) {
  // The soonest reset that hands anything back; an untouched account resets to no effect.
  const nextRefill = pool.resets.find((reset) => reset.restoresPercent > 0);
  return (
    <div className="grid items-center gap-x-6 gap-y-3 rounded-lg border border-border/60 p-4 md:grid-cols-[11rem_minmax(0,1fr)]">
      <div className="flex flex-col gap-1">
        <span className="text-sm font-medium text-foreground">
          {t(pool.label)}
        </span>
        <span className="flex items-baseline gap-2">
          <span className="text-3xl font-semibold text-foreground tabular-nums">
            {pool.remainingPercent}%
          </span>
          <span className="text-sm text-muted-foreground">{t("left")}</span>
          {pool.pace ? <PaceIcon pace={pool.pace} /> : null}
        </span>
        {nextRefill && pool.columns.length > 1 ? (
          <span className="text-xs font-medium text-foreground tabular-nums">
            ↻ +{nextRefill.restoresPercent}%
          </span>
        ) : null}
      </div>
      <PoolBar pool={pool} color={color} now={now} cycleValues={cycleValues} />
    </div>
  );
}

function PoolSection({
  pool,
  now,
  cycleValues,
}: {
  readonly pool: LimitPool;
  readonly now: number;
  readonly cycleValues: CycleValueIndex;
}) {
  const color = barColor(pool.driver);
  const label = getDriverOption(pool.driver)?.label ?? String(pool.driver);
  const windows = displayLimitWindows(pool);
  return (
    <section className="flex flex-col gap-3">
      <h2 className="flex items-center gap-2 text-sm font-medium text-foreground">
        <ProviderInstanceIcon
          driverKind={pool.driver}
          displayName={label}
          indicatorBackground="var(--background)"
          className="size-5"
          iconClassName="size-4 text-foreground/80"
        />
        {label}
      </h2>
      {windows.map((window) => (
        <PoolWindowCard
          key={`${window.kind}:${window.id}`}
          pool={window}
          color={color}
          now={now}
          cycleValues={cycleValues}
        />
      ))}
    </section>
  );
}

/**
 * Accounts pooled per provider: what is open across all of them, who resets
 * next, and how much of the pool that hands back. Answers "can I keep going"
 * before "on which account".
 */
export function UsageLimitsPooled({
  presentations,
  now,
  cycleValues = new Map(),
}: {
  readonly presentations: Parameters<typeof collectLimitAccounts>[0];
  readonly now: number;
  readonly cycleValues?: CycleValueIndex;
}) {
  const pools = collectLimitPools(collectLimitAccounts(presentations), now);
  const notices = collectLimitNotices(presentations);
  const externalLinks = collectExternalUsageLinks(presentations);
  return (
    <div className="flex flex-col gap-8">
      {pools.length === 0 &&
      notices.length === 0 &&
      externalLinks.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {t(
            "No provider on the selected environments reports subscription limits.",
          )}
        </p>
      ) : null}
      {pools.map((pool) => (
        <PoolSection
          key={pool.driver}
          pool={pool}
          now={now}
          cycleValues={cycleValues}
        />
      ))}
      {externalLinks.map((link) => (
        <section
          key={link.url}
          className="flex flex-wrap items-center justify-between gap-3 rounded-xl border p-4"
        >
          <div className="flex min-w-0 flex-1 items-center gap-3">
            {link.url === CHATGPT_USAGE_URL ? (
              <OpenAI className="size-5 shrink-0" aria-hidden="true" />
            ) : null}
            <div className="min-w-0 space-y-1">
              <h2 className="text-sm font-medium">{link.label}</h2>
              {link.url === CHATGPT_USAGE_URL ? (
                <p className="text-xs text-muted-foreground">
                  {t("View usage in ChatGPT with your connected account.")}
                </p>
              ) : link.message ? (
                <p className="max-w-xl text-xs text-muted-foreground">
                  {link.message}
                </p>
              ) : null}
            </div>
          </div>
          <Button
            variant="ghost-muted"
            size="xs"
            onClick={() => void ensureLocalApi().shell.openExternal(link.url)}
          >
            {t("Manage usage")}
            <ExternalLinkIcon className="size-3.5" aria-hidden="true" />
          </Button>
        </section>
      ))}
      <LimitNotices notices={notices} />
    </div>
  );
}

/** Sources and providers that could not be read, so a missing bar is not mistaken for a full one. */
function LimitNotices({ notices }: { readonly notices: readonly string[] }) {
  if (notices.length === 0) return null;
  return (
    <Alert variant="warning" controlAlignment="first-line">
      <AlertTriangleIcon />
      {notices.map((notice) => (
        <AlertTitle key={notice} className="break-words">
          {t(notice)}
        </AlertTitle>
      ))}
    </Alert>
  );
}
