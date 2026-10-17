/** Sync & sharing settings: relay connection, phone pairing and active share links. */
import { useEffect, useState } from "react";
import { Events } from "@wailsio/runtime";
import { CopyIcon, RefreshCwIcon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { Input } from "../../../tools/parity/upstream/apps/web/src/components/ui/input";
import { Switch } from "../../../tools/parity/upstream/apps/web/src/components/ui/switch";
import {
  AlertDialog,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogPopup,
  AlertDialogTitle,
} from "../../vendor/ui/alert-dialog";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import {
  SaveInput,
  type ShareView,
  State as SyncState,
} from "../../bindings/yousage/internal/sync/models";
import { t } from "../i18n";
import {
  Field,
  Notice,
  PageBody,
  PageHeader,
  SettingsRow,
  SettingsSection,
} from "../ui";
import { SummaryCode } from "../receipt/ReceiptPaper";

/**
 * Whether a relay address sends the token without encryption to another machine.
 *
 * @param address - Relay URL as typed.
 * @returns `true` for plain HTTP to a non-loopback host.
 */
export function isInsecureRelay(address: string): boolean {
  try {
    const url = new URL(address);
    return (
      url.protocol === "http:" &&
      !["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)
    );
  } catch {
    return false;
  }
}

/**
 * Format an RFC 3339 instant as a short local date and time.
 *
 * @param instant - Time to format.
 * @returns The local time, or an empty string for an empty value.
 */
function localTime(instant: string): string {
  if (!instant) return "";
  const date = new Date(instant);
  return Number.isNaN(date.getTime()) ? instant : date.toLocaleString();
}

/**
 * Title of a shared period.
 *
 * @param days - Window length in days.
 * @returns “Last 24 hours” or “Last N days”.
 */
function periodTitle(days: number): string {
  return days === 1
    ? t("Last 24 hours")
    : t("Last {days} days", { days: String(days) });
}

/**
 * The sync state after an action, keeping the current one when the action returns none.
 *
 * Bindings without a result still resolve to an empty object, which must not replace the state.
 *
 * @param current - State shown before the action.
 * @param result - What the action resolved to.
 * @returns The state to show next.
 */
export function nextSyncState(
  current: SyncState | null,
  result: unknown,
): SyncState | null {
  return result instanceof SyncState ? result : current;
}

/** A destructive action waiting for confirmation. */
type PendingConfirm =
  | { kind: "rotate" }
  | { kind: "revoke"; share: ShareView }
  | null;

/**
 * Settings page for the relay, the phone link and share links.
 *
 * @returns The sync settings page.
 */
export function SyncPanel() {
  const [state, setState] = useState<SyncState | null>(null);
  const [relayUrl, setRelayUrl] = useState("");
  const [token, setToken] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [isBusy, setIsBusy] = useState(false);
  const [confirm, setConfirm] = useState<PendingConfirm>(null);

  useEffect(() => {
    void nativeApi
      .GetSync()
      .then((value) => {
        setState(value);
        setRelayUrl(value.relayUrl);
      })
      .catch((cause) => setError(String(cause)));
    return Events.On("sync-state", (event) =>
      setState(event.data as SyncState),
    );
  }, []);

  /**
   * Run one sync action, reporting its outcome.
   *
   * @param action - The binding call.
   * @param success - Message shown when it succeeds.
   */
  async function run(action: () => Promise<SyncState | void>, success: string) {
    setIsBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await action();
      setState((current) => nextSyncState(current, result));
      setMessage(success);
    } catch (cause) {
      setError(String(cause));
    } finally {
      setIsBusy(false);
    }
  }

  /**
   * Save the relay address and, when typed, a new token.
   *
   * @param isEnabled - Whether sync should be on after saving.
   */
  function save(isEnabled: boolean) {
    void run(
      async () => {
        const next = await nativeApi.SaveSync(
          SaveInput.createFrom({ isEnabled, relayUrl, token }),
        );
        setToken("");
        return next;
      },
      isEnabled ? t("Sync is on.") : t("Sync settings saved."),
    );
  }

  const status = state?.status;
  const isDirty =
    state !== null && (relayUrl !== state.relayUrl || token !== "");
  return (
    <section aria-label={t("Sync & sharing")}>
      <PageHeader parent={t("Settings")} title={t("Sync & sharing")} />
      <PageBody width="narrow">
        {error && <Notice tone="error">{error}</Notice>}
        {message && !error && <Notice>{message}</Notice>}
        <SettingsSection title={t("Relay")}>
          <SettingsRow
            title={t("Sync to phone")}
            description={t(
              "After each refresh, an encrypted snapshot is uploaded to your relay. The relay cannot read it.",
            )}
            control={
              <Switch
                checked={state?.isEnabled ?? false}
                disabled={!state || isBusy}
                aria-label={t("Sync to phone")}
                onCheckedChange={(isChecked) => save(isChecked)}
              />
            }
          />
          <SettingsRow>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field
                label={t("Relay address")}
                hint={t("For example https://relay.example.com")}
              >
                <Input
                  value={relayUrl}
                  placeholder="https://"
                  spellCheck={false}
                  autoComplete="off"
                  disabled={!state || isBusy}
                  onChange={(event) => setRelayUrl(event.target.value)}
                />
              </Field>
              <Field
                label={t("Relay token")}
                hint={
                  state?.hasToken
                    ? t("A token is saved. Type a new one to replace it.")
                    : t("The YOUSAGE_RELAY_TOKEN set on the relay.")
                }
              >
                <Input
                  type="password"
                  value={token}
                  placeholder={state?.hasToken ? "••••••••" : ""}
                  autoComplete="off"
                  disabled={!state || isBusy}
                  onChange={(event) => setToken(event.target.value)}
                />
              </Field>
            </div>
            {isInsecureRelay(relayUrl) && (
              <Notice tone="error">
                {t(
                  "This address uses plain HTTP, so the relay token travels unencrypted. Use HTTPS, for example through a cloudflared tunnel.",
                )}
              </Notice>
            )}
            <div className="flex flex-wrap justify-end gap-2">
              <Button
                variant="outline"
                size="sm"
                disabled={!state || isBusy || relayUrl.trim() === ""}
                onClick={() =>
                  void run(
                    () => nativeApi.TestRelay(relayUrl, token),
                    t("The relay is reachable and accepts the token."),
                  )
                }
              >
                {t("Test connection")}
              </Button>
              <Button
                size="sm"
                disabled={!state || isBusy || !isDirty}
                onClick={() => save(state?.isEnabled ?? false)}
              >
                {t("Save")}
              </Button>
            </div>
          </SettingsRow>
          {state?.isEnabled && (
            <SettingsRow
              title={t("Last upload")}
              description={
                status?.isPushing
                  ? t("Uploading…")
                  : status?.lastError
                    ? status.lastError
                    : status?.lastPushAt
                      ? localTime(status.lastPushAt)
                      : t("Not uploaded yet")
              }
              control={
                <Button
                  variant="outline"
                  size="sm"
                  disabled={isBusy || status?.isPushing}
                  onClick={() =>
                    void run(() => nativeApi.SyncNow(), t("Snapshot uploaded."))
                  }
                >
                  <RefreshCwIcon aria-hidden />
                  {t("Sync now")}
                </Button>
              }
            />
          )}
        </SettingsSection>
        {state?.isEnabled && state.pairingUrl && (
          <SettingsSection
            title={t("Phone")}
            action={
              <Button
                variant="ghost"
                size="xs"
                disabled={isBusy}
                onClick={() => setConfirm({ kind: "rotate" })}
              >
                {t("Reset link")}
              </Button>
            }
          >
            <SettingsRow>
              <div className="sync-pairing">
                <div className="sync-pairing-code">
                  <SummaryCode modules={state.pairingQrModules} />
                </div>
                <div className="flex min-w-0 flex-col gap-2 text-sm">
                  <span>
                    {t("Scan with your phone to open the dashboard.")}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {t(
                      "The part after # is the key. Anyone with the full link can read your snapshot, so keep it private.",
                    )}
                  </span>
                  <div>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() =>
                        void run(
                          () => nativeApi.CopyText(state.pairingUrl),
                          t("Link copied."),
                        )
                      }
                    >
                      <CopyIcon aria-hidden />
                      {t("Copy link")}
                    </Button>
                  </div>
                </div>
              </div>
            </SettingsRow>
          </SettingsSection>
        )}
        {state?.isEnabled && (
          <SettingsSection title={t("Share links")}>
            {state.shares.length === 0 ? (
              <SettingsRow>
                <p className="text-xs text-muted-foreground">
                  {t(
                    "Create a link from a printed receipt. Active links are listed here.",
                  )}
                </p>
              </SettingsRow>
            ) : (
              state.shares.map((share) => (
                <SettingsRow
                  key={share.id}
                  title={periodTitle(share.days)}
                  description={
                    t("Available until {date}", {
                      date: localTime(share.expiresAt),
                    }) + (share.hideAmounts ? ` · ${t("Amounts hidden")}` : "")
                  }
                  control={
                    <>
                      <Button
                        variant="outline"
                        size="xs"
                        onClick={() =>
                          void run(
                            () => nativeApi.CopyText(share.url),
                            t("Link copied."),
                          )
                        }
                      >
                        {t("Copy link")}
                      </Button>
                      <Button
                        variant="ghost"
                        size="xs"
                        disabled={isBusy}
                        onClick={() => setConfirm({ kind: "revoke", share })}
                      >
                        {t("Revoke")}
                      </Button>
                    </>
                  }
                />
              ))
            )}
          </SettingsSection>
        )}
      </PageBody>
      <AlertDialog
        open={confirm !== null}
        onOpenChange={(isOpen) => {
          if (!isOpen) setConfirm(null);
        }}
      >
        <AlertDialogPopup>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirm?.kind === "rotate"
                ? t("Reset the phone link?")
                : t("Revoke this share link?")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirm?.kind === "rotate"
                ? t(
                    "The current link stops working and its snapshot is deleted from the relay. Scan the new code on your phone.",
                  )
                : t(
                    "The page is deleted from the relay and the link stops working immediately.",
                  )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <Button variant="outline" onClick={() => setConfirm(null)}>
              {t("Cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={isBusy}
              onClick={() => {
                const pending = confirm;
                setConfirm(null);
                if (pending?.kind === "rotate")
                  void run(
                    () => nativeApi.RotatePairing(),
                    t("A new phone link is ready."),
                  );
                else if (pending?.kind === "revoke")
                  void run(
                    () => nativeApi.RevokeShare(pending.share.id),
                    t("Share link revoked."),
                  );
              }}
            >
              {confirm?.kind === "rotate" ? t("Reset link") : t("Revoke")}
            </Button>
          </AlertDialogFooter>
        </AlertDialogPopup>
      </AlertDialog>
    </section>
  );
}
