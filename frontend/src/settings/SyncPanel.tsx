/** Sync settings: relay connection and phone pairing. */
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
  State as SyncState,
} from "../../bindings/yousage/internal/sync/models";
import { t } from "../i18n";
import { notify, notifyError } from "../notify";
import {
  Field,
  Notice,
  PageBody,
  PageHeader,
  SettingsRow,
  SettingsSection,
} from "../ui";

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

/**
 * Settings page for the relay and the phone link.
 *
 * @returns The sync settings page.
 */
export function SyncPanel() {
  const [state, setState] = useState<SyncState | null>(null);
  const [relayUrl, setRelayUrl] = useState("");
  const [token, setToken] = useState("");
  const [loadError, setLoadError] = useState("");
  const [isBusy, setIsBusy] = useState(false);
  const [isConfirmingRotate, setIsConfirmingRotate] = useState(false);

  useEffect(() => {
    void nativeApi
      .GetSync()
      .then((value) => {
        setState(value);
        setRelayUrl(value.relayUrl);
      })
      .catch((cause) => setLoadError(String(cause)));
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
    try {
      const result = await action();
      setState((current) => nextSyncState(current, result));
      notify(success);
    } catch (cause) {
      notifyError(cause);
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
    <section aria-label={t("Sync")}>
      <PageHeader parent={t("Settings")} title={t("Sync")} />
      <PageBody width="narrow">
        {loadError && <Notice tone="error">{loadError}</Notice>}
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
                onClick={() => setIsConfirmingRotate(true)}
              >
                {t("Reset link")}
              </Button>
            }
          >
            <SettingsRow
              title={t("Phone link")}
              description={t(
                "Open this link on your phone. The part after # is the key, so keep the link private.",
              )}
              control={
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
              }
            />
          </SettingsSection>
        )}
      </PageBody>
      <AlertDialog
        open={isConfirmingRotate}
        onOpenChange={setIsConfirmingRotate}
      >
        <AlertDialogPopup>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("Reset the phone link?")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                "The current link stops working and its snapshot is deleted from the relay. Open the new link on your phone.",
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <Button
              variant="outline"
              onClick={() => setIsConfirmingRotate(false)}
            >
              {t("Cancel")}
            </Button>
            <Button
              variant="destructive"
              disabled={isBusy}
              onClick={() => {
                setIsConfirmingRotate(false);
                void run(
                  () => nativeApi.RotatePairing(),
                  t("A new phone link is ready."),
                );
              }}
            >
              {t("Reset link")}
            </Button>
          </AlertDialogFooter>
        </AlertDialogPopup>
      </AlertDialog>
    </section>
  );
}
