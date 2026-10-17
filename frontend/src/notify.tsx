/** Floating notifications for the outcome of user actions, shown in the bottom-right corner. */
import { Toast } from "@base-ui/react/toast";
import {
  CircleAlertIcon,
  CircleCheckIcon,
  InfoIcon,
  XIcon,
} from "lucide-react";
import { t, useI18n } from "./i18n";

/** How a notification reads: a completed action, a failure, or neutral information. */
export type NotifyTone = "success" | "error" | "info";

const TONE_TIMEOUT: Record<NotifyTone, number> = {
  success: 4000,
  info: 5000,
  error: 8000,
};

const TONE_ICON = {
  success: CircleCheckIcon,
  error: CircleAlertIcon,
  info: InfoIcon,
} as const;

/** The single queue every page posts to, so notifications never shift page layout. */
export const toastManager = Toast.createToastManager();

/**
 * Show the outcome of an action as a floating notification.
 *
 * Repeating the same message refreshes the visible notification instead of stacking a copy.
 *
 * @param message - Text to show.
 * @param tone - Success, error or neutral information.
 * @returns The notification ID.
 */
export function notify(message: string, tone: NotifyTone = "success"): string {
  return toastManager.add({
    id: `${tone}:${message}`,
    title: message,
    type: tone,
    timeout: TONE_TIMEOUT[tone],
    priority: tone === "error" ? "high" : "low",
  });
}

/**
 * Show a failed action, using the message of an `Error` when there is one.
 *
 * @param cause - What the action threw.
 * @returns The notification ID.
 */
export function notifyError(cause: unknown): string {
  return notify(
    cause instanceof Error ? cause.message : String(cause),
    "error",
  );
}

/**
 * Remove every notification, for example when the page they describe goes away.
 */
export function clearNotifications() {
  toastManager.close();
}

/** Visible notifications, newest closest to the corner. */
function ToastList() {
  useI18n();
  const { toasts } = Toast.useToastManager();
  return toasts.map((toast) => {
    const tone = (toast.type ?? "info") as NotifyTone;
    const Icon = TONE_ICON[tone] ?? InfoIcon;
    return (
      <Toast.Root
        key={toast.id}
        toast={toast}
        swipeDirection={["right", "down"]}
        className="app-toast"
        data-tone={tone}
      >
        <Toast.Content className="app-toast-content">
          <Icon className="app-toast-icon" aria-hidden />
          <Toast.Title className="app-toast-title" />
          <Toast.Close className="app-toast-close" aria-label={t("Dismiss")}>
            <XIcon aria-hidden />
          </Toast.Close>
        </Toast.Content>
      </Toast.Root>
    );
  });
}

/**
 * Host for floating notifications; mount once next to the application root.
 *
 * @returns The notification viewport.
 */
export function Toaster() {
  return (
    <Toast.Provider toastManager={toastManager} limit={3}>
      <Toast.Portal>
        <Toast.Viewport className="app-toast-viewport">
          <ToastList />
        </Toast.Viewport>
      </Toast.Portal>
    </Toast.Provider>
  );
}
