/** First-run screen: paste the phone link copied from the desktop app. */
import { useState, type FormEvent } from "react";
import { Clipboard } from "@wailsio/runtime";
import { ClipboardPasteIcon, LinkIcon } from "lucide-react";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { Input } from "../../../tools/parity/upstream/apps/web/src/components/ui/input";
import * as mobileApi from "../../bindings/yousage/internal/mobile/service";
import { t, useI18n } from "../i18n";

/**
 * Verify the desktop's phone link before saving it on the device.
 *
 * @param props - Called once the link is saved, and, when a link is saved already, to keep it instead.
 * @returns The pairing screen.
 */
export function PairScreen({
  onPaired,
  onCancel,
}: {
  onPaired: () => void;
  onCancel?: () => void;
}) {
  useI18n();
  const [link, setLink] = useState("");
  const [isBusy, setIsBusy] = useState(false);
  const [error, setError] = useState("");
  /** Connect using the typed link, keeping existing pairing data if verification fails. */
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setIsBusy(true);
    setError("");
    try {
      await mobileApi.Pair(link.trim());
      onPaired();
    } catch {
      setError(
        t(
          "Could not connect. Check the phone link and network, then try again.",
        ),
      );
      setIsBusy(false);
    }
  };
  /** Fill the field from the clipboard. */
  const paste = async () => {
    const text = await Clipboard.Text().catch(() => "");
    if (text) setLink(text.trim());
  };
  return (
    <form className="android-pair" onSubmit={(event) => void submit(event)}>
      <strong className="sidebar-wordmark android-pair-wordmark">
        <span aria-hidden>Y</span>
        <span className="sidebar-wordmark-tail" aria-hidden>
          ou
        </span>
        <span className="sidebar-wordmark-tail" aria-hidden>
          sage
        </span>
      </strong>
      <h1>{t("Connect to your desktop")}</h1>
      <p>
        {t(
          "On your computer, open Settings → Sync & sharing, turn on Sync to phone and copy the phone link. Paste it here.",
        )}
      </p>
      <div className="android-pair-field">
        <Input
          type="url"
          inputMode="url"
          autoComplete="off"
          spellCheck={false}
          placeholder="https://…/v/…#…"
          aria-label={t("Phone link")}
          value={link}
          disabled={isBusy}
          onChange={(event) => setLink(event.target.value)}
        />
        <Button
          type="button"
          variant="outline"
          aria-label={t("Paste")}
          disabled={isBusy}
          onClick={() => void paste()}
        >
          <ClipboardPasteIcon aria-hidden />
        </Button>
      </div>
      {error && (
        <p className="android-pair-error" role="alert">
          {error}
        </p>
      )}
      <Button type="submit" disabled={isBusy || link.trim() === ""}>
        <LinkIcon aria-hidden />
        {t("Connect")}
      </Button>
      {onCancel && (
        <Button
          type="button"
          variant="ghost"
          disabled={isBusy}
          onClick={onCancel}
        >
          {t("Keep the current link")}
        </Button>
      )}
      <p className="android-pair-note">
        {t(
          "The part after # is the key. It stays on this phone and is never sent to the relay.",
        )}
      </p>
    </form>
  );
}
