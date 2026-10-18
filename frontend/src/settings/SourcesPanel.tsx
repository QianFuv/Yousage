/** Local history sources and quota accounts for the monitored providers. */
import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { FolderPlusIcon, ScanSearchIcon, Trash2Icon } from "lucide-react";
import * as nativeApi from "../../bindings/yousage/internal/desktop/app";
import {
  SavedConfiguration,
  type ProviderDetection,
} from "../../bindings/yousage/internal/service/models";
import { SavedSources } from "../../bindings/yousage/internal/desktop/models";
import { useData } from "../usage/controller";
import { t } from "../i18n";
import { notify, notifyError } from "../notify";
import { Notice, PageBody, PageHeader, SettingsSection } from "../ui";
import {
  addSessionFolder,
  isMissingFolder,
  isRemovableSessionFolder,
  providerLabel,
  providersWithoutQuota,
  removeSessionFolder,
  sessionFolderNumber,
  sessionFolderProviders,
  sourceStatus,
} from "./sources";
import { Button } from "../../../tools/parity/upstream/apps/web/src/components/ui/button";
import { Input } from "../../../tools/parity/upstream/apps/web/src/components/ui/input";
import { Switch } from "../../../tools/parity/upstream/apps/web/src/components/ui/switch";

/**
 * Localized installation status for a quota account.
 *
 * @param detection - Detection result for the account's driver, if any.
 * @returns The status line.
 */
function detectionMessage(detection: ProviderDetection | undefined): string {
  if (!detection) return t("Not detected yet.");
  return detection.isInstalled
    ? t(
        "Detected automatically. Account access is checked only after enabling quotas.",
      )
    : t(
        "Not detected. Install the provider or configure its standard location, then detect again.",
      );
}

/** Edit two independently revisioned resources with distinct Apply and Discard actions. */
export function SourcesPanel({
  onDraftChange,
}: {
  onDraftChange: (isDirty: boolean, isPending: boolean) => void;
}) {
  const data = useData();
  const [sources, setSources] = useState<SavedSources | null>(null);
  const [savedSources, setSavedSources] = useState<SavedSources | null>(null);
  const [config, setConfig] = useState<SavedConfiguration | null>(null);
  const [savedConfig, setSavedConfig] = useState<SavedConfiguration | null>(
    null,
  );
  const [detections, setDetections] = useState<ProviderDetection[]>([]);
  const [addedSourceId, setAddedSourceId] = useState("");
  const [loadError, setLoadError] = useState("");
  const [isPending, setIsPending] = useState(false);
  const isWorking = useRef(false);
  const isSourcesDirty =
    JSON.stringify(sources) !== JSON.stringify(savedSources);
  const isConfigDirty = JSON.stringify(config) !== JSON.stringify(savedConfig);
  useLayoutEffect(
    () => onDraftChange(isSourcesDirty || isConfigDirty, isPending),
    [isSourcesDirty, isConfigDirty, isPending, onDraftChange],
  );
  useEffect(() => {
    let isDisposed = false;
    void Promise.all([
      nativeApi.GetSources(),
      nativeApi.GetConfiguration(),
      nativeApi.DetectProviders(),
    ])
      .then(([roots, settings, detected]) => {
        if (!isDisposed) {
          setDetections(detected);
          setSources(roots);
          setSavedSources(roots);
          setConfig(settings);
          setSavedConfig(settings);
        }
      })
      .catch((cause) => {
        if (!isDisposed) setLoadError(String(cause));
      });
    return () => {
      isDisposed = true;
    };
  }, []);
  /** Preserve failed drafts and block overlapping Apply/Discard actions. */
  async function act(operation: () => Promise<void>) {
    if (isWorking.current) return;
    isWorking.current = true;
    setIsPending(true);
    try {
      await operation();
    } catch (cause) {
      notifyError(cause);
    } finally {
      isWorking.current = false;
      setIsPending(false);
    }
  }
  if (!sources || !config)
    return (
      <section aria-label={t("Sources & connections")}>
        <PageHeader parent={t("Settings")} title={t("Sources & connections")} />
        <PageBody width="narrow">
          <Notice tone={loadError ? "error" : "default"}>
            {loadError || t("Loading configuration…")}
          </Notice>
        </PageBody>
      </section>
    );
  /** Replace one history source row in the draft. */
  function changeSource(
    index: number,
    value: Partial<SavedSources["value"][number]>,
  ) {
    setSources({
      ...sources!,
      value: sources!.value.map((row, position) =>
        position === index ? { ...row, ...value } : row,
      ),
    });
  }
  /** Append an empty custom session folder for a provider and focus its path. */
  function addSource(provider: SavedSources["value"][number]["provider"]) {
    const next = addSessionFolder(sources!.value, provider);
    setSources({ ...sources!, value: next.sources });
    setAddedSourceId(next.id);
  }
  /** Drop one custom session folder from the draft. */
  function removeSource(id: string) {
    setSources({ ...sources!, value: removeSessionFolder(sources!.value, id) });
  }
  /** Replace one quota account row in the draft. */
  function changeAccount(
    index: number,
    value: Partial<SavedConfiguration["value"]["providers"][number]>,
  ) {
    setConfig({
      ...config!,
      value: {
        ...config!.value,
        providers: config!.value.providers.map((row, position) =>
          position === index ? { ...row, ...value } : row,
        ),
      },
    });
  }
  const folderProviders = sessionFolderProviders(sources.value, detections);
  const hasMissingFolder = sources.value.some(isMissingFolder);
  const quotaFreeProviders = providersWithoutQuota(detections);
  return (
    <section aria-label={t("Sources & connections")}>
      <PageHeader parent={t("Settings")} title={t("Sources & connections")}>
        <Button
          variant="outline"
          size="sm"
          disabled={isPending}
          onClick={() =>
            void act(async () => {
              setDetections(await nativeApi.DetectProviders());
              notify(t("Provider detection updated."));
            })
          }
        >
          <ScanSearchIcon aria-hidden />
          {t("Detect providers again")}
        </Button>
      </PageHeader>
      <PageBody width="narrow">
        <fieldset disabled={isPending} className="m-0 min-w-0 border-0 p-0">
          <SettingsSection
            title={
              <DraftTitle
                title={t("History sources")}
                isDirty={isSourcesDirty}
              />
            }
            action={folderProviders.map((provider) => (
              <Button
                key={provider}
                variant="outline"
                size="xs"
                onClick={() => addSource(provider)}
              >
                <FolderPlusIcon aria-hidden />
                {t("Add {label} session folder", {
                  label: providerLabel(provider),
                })}
              </Button>
            ))}
          >
            {sources.value.map((source, index) =>
              isRemovableSessionFolder(source) ? (
                <SessionFolderRow
                  key={source.id}
                  label={t("{label} session folder {number}", {
                    label: providerLabel(source.provider),
                    number: sessionFolderNumber(sources.value, index),
                  })}
                  root={source.root}
                  isEnabled={source.isEnabled}
                  shouldFocus={source.id === addedSourceId}
                  onRootChange={(root) => changeSource(index, { root })}
                  onEnabledChange={(isEnabled) =>
                    changeSource(index, { isEnabled })
                  }
                  onRemove={() => removeSource(source.id)}
                />
              ) : (
                <EntryRow
                  key={source.id}
                  label={providerLabel(source.provider)}
                  isEnabled={source.isEnabled}
                  onEnabledChange={(isEnabled) =>
                    changeSource(index, { isEnabled })
                  }
                  status={t(sourceStatus(source, detections))}
                  action={
                    !source.isAutomatic && (
                      <Button
                        variant="ghost"
                        size="xs"
                        onClick={() =>
                          changeSource(index, { root: "", isAutomatic: true })
                        }
                      >
                        {t("Use automatic detection")}
                      </Button>
                    )
                  }
                />
              ),
            )}
          </SettingsSection>
        </fieldset>
        <fieldset disabled={isPending} className="m-0 min-w-0 border-0 p-0">
          <SettingsSection
            title={
              <DraftTitle title={t("Quota accounts")} isDirty={isConfigDirty} />
            }
          >
            {config.value.providers.map((provider, index) => (
              <EntryRow
                key={provider.id}
                label={providerLabel(provider.driver)}
                isEnabled={provider.enabled}
                onEnabledChange={(enabled) => changeAccount(index, { enabled })}
                status={
                  provider.home || provider.executable
                    ? t("Custom configuration")
                    : detectionMessage(
                        detections.find(
                          (item) => item.driver === provider.driver,
                        ),
                      )
                }
                action={
                  (provider.home || provider.executable) && (
                    <Button
                      variant="ghost"
                      size="xs"
                      onClick={() =>
                        changeAccount(index, { executable: "", home: "" })
                      }
                    >
                      {t("Use automatic detection")}
                    </Button>
                  )
                }
              />
            ))}
            {quotaFreeProviders.map((detection) => (
              <div
                key={detection.provider}
                className="settings-row flex min-w-0 flex-col gap-0.5"
              >
                <span className="text-sm text-foreground">
                  {providerLabel(detection.provider)}
                </span>
                <output className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
                  {t(
                    "Quota limits are not available: {label} does not report subscription quotas. Its usage still appears from saved history.",
                    { label: providerLabel(detection.provider) },
                  )}
                </output>
              </div>
            ))}
          </SettingsSection>
        </fieldset>
        {(isSourcesDirty || isConfigDirty) && (
          <div className="sticky bottom-4 z-10 flex flex-col gap-2">
            {isSourcesDirty && (
              <DraftBar
                label={t("Unsaved history source changes")}
                applyLabel={t("Apply history sources")}
                isPending={isPending}
                problem={
                  hasMissingFolder
                    ? t("Enter a path for every custom session folder.")
                    : undefined
                }
                onDiscard={() => {
                  setSources(savedSources);
                  notify(t("History draft discarded."), "info");
                }}
                onApply={() =>
                  void act(async () => {
                    const next = await nativeApi.SaveSources(
                      SavedSources.createFrom(JSON.stringify(sources)),
                    );
                    setSources(next);
                    setSavedSources(next);
                    await data.reload();
                    notify(t("History sources applied."));
                  })
                }
              />
            )}
            {isConfigDirty && (
              <DraftBar
                label={t("Unsaved quota account changes")}
                applyLabel={t("Apply quota accounts")}
                isPending={isPending}
                onDiscard={() => {
                  setConfig(savedConfig);
                  notify(t("Quota account draft discarded."), "info");
                }}
                onApply={() =>
                  void act(async () => {
                    const saved = await nativeApi.SaveConfiguration(
                      SavedConfiguration.createFrom(JSON.stringify(config)),
                    );
                    setConfig(saved);
                    setSavedConfig(saved);
                    await data.reload();
                    notify(t("Quota accounts applied."));
                  })
                }
              />
            )}
          </div>
        )}
      </PageBody>
    </section>
  );
}

/**
 * Section title with an unsaved-draft badge.
 *
 * @param props - Title text and draft state.
 * @returns The title content.
 */
function DraftTitle({ title, isDirty }: { title: string; isDirty: boolean }) {
  return (
    <span className="flex items-center gap-2">
      {title}
      {isDirty && (
        <span className="rounded-full bg-primary/10 px-1.5 py-px text-2xs font-medium text-primary">
          {t("Unsaved")}
        </span>
      )}
    </span>
  );
}

/**
 * Fixed provider row with its name, detection status, optional action and enable switch.
 *
 * @param props - Provider label, enabled state, status text and optional action.
 * @returns The entry row element.
 */
function EntryRow({
  label,
  isEnabled,
  onEnabledChange,
  status,
  action,
}: {
  label: string;
  isEnabled: boolean;
  onEnabledChange: (isEnabled: boolean) => void;
  status: string;
  action?: ReactNode;
}) {
  return (
    <div className="settings-row flex min-w-0 items-center gap-4">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-sm text-foreground">{label}</span>
        <output className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
          {status}
        </output>
      </div>
      {action}
      <Switch
        aria-label={t("Enable {label}", { label })}
        checked={isEnabled}
        onCheckedChange={onEnabledChange}
      />
    </div>
  );
}

/**
 * Custom session folder row with an editable path, removal and an enable switch.
 *
 * @param props - Row label, folder path, enabled state, initial focus and actions.
 * @returns The folder row element.
 */
function SessionFolderRow({
  label,
  root,
  isEnabled,
  shouldFocus,
  onRootChange,
  onEnabledChange,
  onRemove,
}: {
  label: string;
  root: string;
  isEnabled: boolean;
  shouldFocus: boolean;
  onRootChange: (root: string) => void;
  onEnabledChange: (isEnabled: boolean) => void;
  onRemove: () => void;
}) {
  const isMissing = root.trim() === "";
  return (
    <div className="settings-row flex min-w-0 flex-col gap-2">
      <div className="flex min-w-0 items-center gap-4">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="text-sm text-foreground">{label}</span>
          <output className="text-xs text-muted-foreground [overflow-wrap:anywhere]">
            {isMissing
              ? t("Enter the folder where the session files are saved.")
              : t("Custom session folder")}
          </output>
        </div>
        <Button
          variant="ghost"
          size="xs"
          aria-label={t("Remove {label}", { label })}
          onClick={onRemove}
        >
          <Trash2Icon aria-hidden />
          {t("Remove")}
        </Button>
        <Switch
          aria-label={t("Enable {label}", { label })}
          checked={isEnabled}
          onCheckedChange={onEnabledChange}
        />
      </div>
      <Input
        value={root}
        aria-label={t("{label} path", { label })}
        aria-invalid={isMissing || undefined}
        placeholder={t("Session folder path")}
        spellCheck={false}
        autoComplete="off"
        autoFocus={shouldFocus}
        onChange={(event) => onRootChange(event.target.value)}
      />
    </div>
  );
}

/**
 * Floating apply/discard bar for one independently revisioned draft.
 *
 * @param props - Draft label, pending state, an optional problem that blocks Apply, and actions.
 * @returns The draft bar element.
 */
function DraftBar({
  label,
  applyLabel,
  isPending,
  problem,
  onDiscard,
  onApply,
}: {
  label: string;
  applyLabel: string;
  isPending: boolean;
  problem?: string;
  onDiscard: () => void;
  onApply: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border bg-popover/95 px-4 py-2.5 shadow-lg backdrop-blur-md">
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-sm text-foreground">{label}</span>
        {problem && (
          <span role="status" className="text-xs text-muted-foreground">
            {problem}
          </span>
        )}
      </span>
      <div className="flex items-center gap-2">
        <Button
          variant="ghost"
          size="sm"
          disabled={isPending}
          onClick={onDiscard}
        >
          {t("Discard")}
        </Button>
        <Button
          size="sm"
          aria-label={applyLabel}
          disabled={isPending || problem !== undefined}
          onClick={onApply}
        >
          {t("Apply")}
        </Button>
      </div>
    </div>
  );
}
