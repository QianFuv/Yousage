/** Create a public share link for the receipt period after previewing exactly what it shows. */
import { useEffect, useRef, useState } from "react";
import { CopyIcon, Link2Icon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { Switch } from "../../../tools/parity/upstream/apps/web/src/components/ui/switch";
import {
  Dialog,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogPanel,
  DialogPopup,
  DialogTitle,
} from "../../../tools/parity/upstream/apps/web/src/components/ui/dialog";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import {
  ShareRequest,
  type ShareView,
} from "../../bindings/yousage/internal/sync/models";
import { msg, t } from "../i18n";
import { Notice, OptionSelect } from "../ui";
import { SharePage } from "../web/SharePage";
import type { SharePayload } from "../web/payloads";
import { SummaryCode } from "./ReceiptPaper";
import { captureSharePalette } from "./sharePalette";

/** Lifetimes the relay accepts, in days. */
const LIFETIMES = [
  { value: 1, label: msg("1 day") },
  { value: 7, label: msg("7 days") },
  { value: 14, label: msg("14 days") },
  { value: 30, label: msg("30 days") },
] as const;

/**
 * Build a share request with the colors currently on screen.
 *
 * @param days - Receipt period in days.
 * @param ttlDays - Link lifetime in days.
 * @param hideAmounts - Whether amounts are withheld from the page.
 * @param timeZone - IANA zone of the receipt window.
 * @returns The request for the desktop bindings.
 */
function buildShareRequest(
  days: number,
  ttlDays: number,
  hideAmounts: boolean,
  timeZone: string,
): ShareRequest {
  return ShareRequest.createFrom({
    days,
    ttlDays,
    hideAmounts,
    timeZone,
    palette: captureSharePalette(),
  });
}

/** Ask the application shell to open a settings section. */
export function openSyncSettings() {
  window.dispatchEvent(
    new CustomEvent("yousage-navigate", {
      detail: { route: "settings", section: "sync" },
    }),
  );
}

/**
 * Dialog that previews the share page, creates the link on confirmation and shows its code.
 *
 * @param props - Receipt period, open state and a callback for the created link.
 * @returns The dialog.
 */
export function ShareDialog({
  days,
  isOpen,
  onOpenChange,
  onCreated,
}: {
  days: number;
  isOpen: boolean;
  onOpenChange: (isOpen: boolean) => void;
  onCreated: (share: ShareView) => void;
}) {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  const [isEnabled, setIsEnabled] = useState<boolean | null>(null);
  const [ttlDays, setTtlDays] = useState(14);
  const [hideAmounts, setHideAmounts] = useState(false);
  const [preview, setPreview] = useState<SharePayload | null>(null);
  const [created, setCreated] = useState<ShareView | null>(null);
  const [error, setError] = useState("");
  const [isCreating, setIsCreating] = useState(false);
  const [copyStatus, setCopyStatus] = useState("");
  const request = useRef(0);

  useEffect(() => {
    if (!isOpen) return;
    setCreated(null);
    setError("");
    setCopyStatus("");
    void nativeApi
      .GetSync()
      .then((state) => setIsEnabled(state.isEnabled))
      .catch((cause) => setError(String(cause)));
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen || !isEnabled || created) return;
    const identity = ++request.current;
    void nativeApi
      .PreviewShare(buildShareRequest(days, ttlDays, hideAmounts, zone))
      .then((payload) => {
        if (identity === request.current)
          setPreview(payload as unknown as SharePayload);
      })
      .catch((cause) => {
        if (identity === request.current) setError(String(cause));
      });
  }, [isOpen, isEnabled, created, days, ttlDays, hideAmounts, zone]);

  /** Upload the previewed page and keep its link. */
  async function create() {
    setIsCreating(true);
    setError("");
    try {
      const share = await nativeApi.CreateShare(
        buildShareRequest(days, ttlDays, hideAmounts, zone),
      );
      setCreated(share);
      onCreated(share);
    } catch (cause) {
      setError(String(cause));
    } finally {
      setIsCreating(false);
    }
  }

  return (
    <Dialog open={isOpen} onOpenChange={onOpenChange}>
      <DialogPopup className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("Share this receipt")}</DialogTitle>
          <DialogDescription>
            {created
              ? t("Anyone with this link can open the page until it expires.")
              : t(
                  "The page below is encrypted on this device and uploaded to your relay. The key stays in the link.",
                )}
          </DialogDescription>
        </DialogHeader>
        <DialogPanel>
          <div className="flex flex-col gap-4">
            {error && <Notice tone="error">{error}</Notice>}
            {isEnabled === false && (
              <Notice
                action={
                  <Button
                    size="xs"
                    variant="outline"
                    onClick={() => {
                      onOpenChange(false);
                      openSyncSettings();
                    }}
                  >
                    {t("Open sync settings")}
                  </Button>
                }
              >
                {t("Set up a relay in Sync & sharing to create share links.")}
              </Notice>
            )}
            {isEnabled && created && (
              <div className="share-created">
                <div className="share-created-code">
                  <SummaryCode modules={created.qrModules} />
                </div>
                <div className="flex min-w-0 flex-col gap-2">
                  <code className="share-created-url">{created.url}</code>
                  <span className="text-xs text-muted-foreground">
                    {t("Available until {date}", {
                      date: new Date(created.expiresAt).toLocaleString(),
                    })}
                  </span>
                  <div>
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() =>
                        void nativeApi
                          .CopyText(created.url)
                          .then(() => setCopyStatus(t("Copied")))
                          .catch((cause) => setCopyStatus(String(cause)))
                      }
                    >
                      <CopyIcon aria-hidden />
                      {copyStatus || t("Copy link")}
                    </Button>
                  </div>
                </div>
              </div>
            )}
            {isEnabled && !created && (
              <>
                <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
                  <label className="flex items-center gap-2 text-sm">
                    {t("Available for")}
                    <OptionSelect
                      value={ttlDays}
                      options={LIFETIMES.map((option) => ({
                        value: option.value,
                        label: t(option.label),
                      }))}
                      onChange={setTtlDays}
                      label={t("Available for")}
                      className="min-w-28"
                    />
                  </label>
                  <label className="flex items-center gap-2 text-sm">
                    <Switch
                      checked={hideAmounts}
                      onCheckedChange={setHideAmounts}
                    />
                    {t("Hide amounts")}
                  </label>
                </div>
                <div
                  className="share-preview"
                  aria-label={t("Share preview")}
                  role="region"
                >
                  {preview ? (
                    <SharePage payload={preview} />
                  ) : (
                    <p className="p-6 text-sm text-muted-foreground">
                      {t("Preparing preview…")}
                    </p>
                  )}
                </div>
              </>
            )}
          </div>
        </DialogPanel>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {created ? t("Done") : t("Cancel")}
          </Button>
          {isEnabled && !created && (
            <Button
              disabled={!preview || isCreating}
              onClick={() => void create()}
            >
              <Link2Icon aria-hidden />
              {isCreating ? t("Creating link…") : t("Create link")}
            </Button>
          )}
        </DialogFooter>
      </DialogPopup>
    </Dialog>
  );
}
