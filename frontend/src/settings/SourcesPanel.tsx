/** Root-mounted source drafts separate historical scanning from quota accounts and authenticated environments. */
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import * as nativeApi from "../../bindings/yousage/app";
import {
  SavedConfiguration,
  type Configuration,
} from "../../bindings/yousage/internal/service/models";
import { Provider } from "../../bindings/yousage/internal/domain/models";
import { SavedSources } from "../../bindings/yousage/models";
import { useData } from "../usage/controller";

const PROVIDERS = [
  "codex",
  "claude",
  "grok",
  "cursor",
  "opencode",
  "antigravity",
] as const;
const DRIVERS = [
  "codex",
  "claudeAgent",
  "cursor",
  "grok",
  "opencode",
  "antigravity",
] as const;

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
  const [status, setStatus] = useState("");
  const [hasCredentialCleanup, setHasCredentialCleanup] = useState(false);
  const [localIdentity, setLocalIdentity] = useState("");
  const [isPending, setIsPending] = useState(false);
  const isWorking = useRef(false);
  const draftCredentials = useRef(new Set<string>());
  const [hubKeys, setHubKeys] = useState<Record<string, string>>({});
  const [pair, setPair] = useState({
    origin: "",
    label: "",
    bootstrap: "",
    isReadOnly: true,
  });
  const isSourcesDirty =
    JSON.stringify(sources) !== JSON.stringify(savedSources);
  const isConfigDirty =
    JSON.stringify(config) !== JSON.stringify(savedConfig) ||
    Object.values(hubKeys).some(Boolean);
  const isPairDirty = Boolean(pair.origin || pair.label || pair.bootstrap);
  useLayoutEffect(
    () =>
      onDraftChange(
        isSourcesDirty || isConfigDirty || isPairDirty || hasCredentialCleanup,
        isPending,
      ),
    [
      isSourcesDirty,
      isConfigDirty,
      isPairDirty,
      hasCredentialCleanup,
      isPending,
      onDraftChange,
    ],
  );
  useEffect(() => {
    void nativeApi
      .GetLocalEnvironmentId()
      .then(setLocalIdentity)
      .catch((cause) => setStatus(String(cause)));
  }, []);
  useEffect(() => {
    let isDisposed = false;
    void Promise.all([nativeApi.GetSources(), nativeApi.GetConfiguration()])
      .then(([roots, settings]) => {
        if (!isDisposed) {
          setSources(roots);
          setSavedSources(roots);
          setConfig(settings);
          setSavedConfig(settings);
        }
      })
      .catch((cause) => {
        if (!isDisposed) setStatus(String(cause));
      });
    return () => {
      isDisposed = true;
    };
  }, []);
  /** Preserve failed drafts and block overlapping Apply/Discard/pair actions. */
  async function act(operation: () => Promise<void>) {
    if (isWorking.current) return;
    isWorking.current = true;
    setIsPending(true);
    setStatus("");
    try {
      await operation();
    } catch (cause) {
      setStatus(String(cause));
    } finally {
      isWorking.current = false;
      setIsPending(false);
    }
  }
  /** Replace metadata only; credentials remain opaque native references. */
  function changeConfig(value: Partial<Configuration>) {
    setConfig((current) =>
      current ? { ...current, value: { ...current.value, ...value } } : current,
    );
  }
  /** Keep canceled pickers from clearing the existing draft field. */
  async function pick(kind: string, assign: (path: string) => void) {
    try {
      const path = await nativeApi.PickSourcePath(kind);
      if (path) assign(path);
    } catch (cause) {
      setStatus(String(cause));
    }
  }
  if (!sources || !config)
    return (
      <section className="settings-panel">
        <h1>Sources &amp; connections</h1>
        <output role="status">{status || "Loading configuration…"}</output>
      </section>
    );
  return (
    <section className="settings-panel" aria-label="Sources and connections">
      <h1>Sources &amp; connections</h1>
      <p>
        Changes stay in this draft while you navigate. Apply each group
        explicitly. Account credentials are held by the operating system.
      </p>
      <fieldset disabled={isPending} className="settings-card">
        <legend>History sources{isSourcesDirty ? " · Unsaved" : ""}</legend>
        <p>
          Disabling a quota account does not remove history. A root may be
          configured before it exists; applying a path does not query an
          account.
        </p>
        {sources.value.map((source, index) => (
          <div className="source-row" key={source.id}>
            <label className="check-row">
              <input
                type="checkbox"
                checked={source.isEnabled}
                onChange={(event) =>
                  setSources({
                    ...sources,
                    value: sources.value.map((row, position) =>
                      position === index
                        ? { ...row, isEnabled: event.target.checked }
                        : row,
                    ),
                  })
                }
              />
              Enabled
            </label>
            <label>
              Provider
              <select
                value={source.provider}
                onChange={(event) =>
                  setSources({
                    ...sources,
                    value: sources.value.map((row, position) =>
                      position === index
                        ? {
                            ...row,
                            provider: event.target
                              .value as typeof source.provider,
                          }
                        : row,
                    ),
                  })
                }
              >
                {PROVIDERS.map((provider) => (
                  <option key={provider}>{provider}</option>
                ))}
              </select>
            </label>
            <label>
              History path
              <input
                value={source.root}
                maxLength={4096}
                onChange={(event) =>
                  setSources({
                    ...sources,
                    value: sources.value.map((row, position) =>
                      position === index
                        ? { ...row, root: event.target.value }
                        : row,
                    ),
                  })
                }
              />
            </label>
            <div className="settings-actions">
              <button
                onClick={() =>
                  void pick(
                    source.provider === "cursor" ? "file" : "directory",
                    (path) =>
                      setSources({
                        ...sources,
                        value: sources.value.map((row, position) =>
                          position === index ? { ...row, root: path } : row,
                        ),
                      }),
                  )
                }
              >
                Browse
              </button>
              <button
                onClick={() =>
                  setSources({
                    ...sources,
                    value: sources.value.filter((row) => row.id !== source.id),
                  })
                }
              >
                Remove
              </button>
            </div>
          </div>
        ))}
        <div className="settings-actions">
          <button
            disabled={sources.value.length >= 64}
            onClick={() =>
              setSources({
                ...sources,
                value: [
                  ...sources.value,
                  {
                    id: crypto.randomUUID(),
                    provider: Provider.Codex,
                    root: "",
                    isEnabled: true,
                  },
                ],
              })
            }
          >
            Add history source
          </button>
          <button
            disabled={!isSourcesDirty}
            onClick={() =>
              void act(async () => {
                const next = await nativeApi.SaveSources(
                  SavedSources.createFrom(JSON.stringify(sources)),
                );
                setSources(next);
                setSavedSources(next);
                await data.reload();
                setStatus("History sources applied.");
              })
            }
          >
            Apply history sources
          </button>
          <button
            disabled={!isSourcesDirty}
            onClick={() => {
              setSources(savedSources);
              setStatus("History draft discarded.");
            }}
          >
            Discard history changes
          </button>
        </div>
      </fieldset>
      <fieldset disabled={isPending} className="settings-card">
        <legend>
          Quota accounts &amp; environment connections
          {isConfigDirty ? " · Unsaved" : ""}
        </legend>
        <h2>Native quota accounts</h2>
        <p>
          Enable only the accounts you want queried. Codex and Claude require a
          native executable. Optional directories are specific to each provider;
          default environment paths apply when blank. Antigravity native quota
          is currently unsupported.
        </p>
        {config.value.providers.map((provider, index) => (
          <div key={provider.id} className="source-row">
            <label className="check-row">
              <input
                type="checkbox"
                checked={provider.enabled}
                onChange={(event) =>
                  changeConfig({
                    providers: config.value.providers.map((row, position) =>
                      position === index
                        ? { ...row, enabled: event.target.checked }
                        : row,
                    ),
                  })
                }
              />
              Enabled
            </label>
            <label>
              Driver
              <select
                value={provider.driver}
                onChange={(event) =>
                  changeConfig({
                    providers: config.value.providers.map((row, position) =>
                      position === index
                        ? { ...row, driver: event.target.value }
                        : row,
                    ),
                  })
                }
              >
                {DRIVERS.map((driver) => (
                  <option key={driver}>{driver}</option>
                ))}
              </select>
            </label>
            <label>
              Label
              <input
                value={provider.label}
                maxLength={128}
                onChange={(event) =>
                  changeConfig({
                    providers: config.value.providers.map((row, position) =>
                      position === index
                        ? { ...row, label: event.target.value }
                        : row,
                    ),
                  })
                }
              />
            </label>
            {(provider.driver === "codex" ||
              provider.driver === "claudeAgent") && (
              <>
                <label>
                  Native executable
                  <input
                    value={provider.executable}
                    maxLength={4096}
                    onChange={(event) =>
                      changeConfig({
                        providers: config.value.providers.map(
                          (row, position) =>
                            position === index
                              ? { ...row, executable: event.target.value }
                              : row,
                        ),
                      })
                    }
                  />
                </label>
                <button
                  onClick={() =>
                    void pick("executable", (path) =>
                      changeConfig({
                        providers: config.value.providers.map(
                          (row, position) =>
                            position === index
                              ? { ...row, executable: path }
                              : row,
                        ),
                      }),
                    )
                  }
                >
                  Choose executable
                </button>
              </>
            )}
            <label>
              {provider.driver === "codex"
                ? "Codex state directory"
                : provider.driver === "claudeAgent"
                  ? "Claude config directory"
                  : "Alternate user home"}
              <input
                value={provider.home}
                maxLength={4096}
                onChange={(event) =>
                  changeConfig({
                    providers: config.value.providers.map((row, position) =>
                      position === index
                        ? { ...row, home: event.target.value }
                        : row,
                    ),
                  })
                }
              />
            </label>
            <div className="settings-actions">
              <button
                onClick={() =>
                  void pick("directory", (path) =>
                    changeConfig({
                      providers: config.value.providers.map((row, position) =>
                        position === index ? { ...row, home: path } : row,
                      ),
                    }),
                  )
                }
              >
                Choose home
              </button>
              <button
                onClick={() =>
                  changeConfig({
                    providers: config.value.providers.filter(
                      (row) => row.id !== provider.id,
                    ),
                  })
                }
              >
                Remove account
              </button>
            </div>
          </div>
        ))}
        <button
          disabled={config.value.providers.length >= 32}
          onClick={() =>
            changeConfig({
              providers: [
                ...config.value.providers,
                {
                  id: crypto.randomUUID(),
                  driver: "codex",
                  label: "Codex",
                  executable: "",
                  home: "",
                  enabled: false,
                },
              ],
            })
          }
        >
          Add quota account
        </button>
        <label className="check-row">
          <input
            type="checkbox"
            checked={config.value.cursorKeychainEnabled}
            onChange={(event) =>
              changeConfig({ cursorKeychainEnabled: event.target.checked })
            }
          />
          Allow macOS Cursor Keychain access when querying Cursor
        </label>
        <h2>Remote environments</h2>
        <p>
          Direct HTTPS origins (HTTP only on loopback). Pairing exchanges a
          transient bootstrap token; saving the draft starts the connection.
          Relay, DPoP and SSH transports are not supported.
        </p>
        {config.value.connections.map((connection, index) => {
          const state = data.snapshot?.environments.find(
            (environment) =>
              environment.environmentId === connection.connection.environmentId,
          );
          const applied = savedConfig?.value.connections.find(
            (row) =>
              row.connection.environmentId ===
              connection.connection.environmentId,
          );
          const isApplied = Boolean(
            applied &&
            JSON.stringify(applied.connection) ===
              JSON.stringify(connection.connection) &&
            applied.enabled === connection.enabled,
          );
          return (
            <div
              key={connection.connection.environmentId}
              className="source-row"
            >
              <label className="check-row">
                <input
                  type="checkbox"
                  checked={connection.enabled}
                  onChange={(event) =>
                    changeConfig({
                      connections: config.value.connections.map(
                        (row, position) =>
                          position === index
                            ? { ...row, enabled: event.target.checked }
                            : row,
                      ),
                    })
                  }
                />
                Enabled
              </label>
              <label>
                Label
                <input
                  value={connection.label}
                  maxLength={128}
                  onChange={(event) =>
                    changeConfig({
                      connections: config.value.connections.map(
                        (row, position) =>
                          position === index
                            ? { ...row, label: event.target.value }
                            : row,
                      ),
                    })
                  }
                />
              </label>
              <p className="private-path">{connection.connection.url}</p>
              <small>
                {isApplied && state?.connected
                  ? "Connected"
                  : isApplied
                    ? "Offline"
                    : "Draft · not yet applied"}{" "}
                · Protocol{" "}
                {state?.serverConfig?.environment
                  .orchestrationProtocolVersion ?? "unknown"}{" "}
                ·{" "}
                {isApplied && state?.canOperate
                  ? "Can operate"
                  : "Read only / unavailable"}
              </small>
              <button
                onClick={() =>
                  changeConfig({
                    connections: config.value.connections.filter(
                      (row) =>
                        row.connection.environmentId !==
                        connection.connection.environmentId,
                    ),
                    primaryEnvironmentId:
                      config.value.primaryEnvironmentId ===
                      connection.connection.environmentId
                        ? localIdentity
                        : config.value.primaryEnvironmentId,
                  })
                }
              >
                Remove environment
              </button>
            </div>
          );
        })}
        <div className="settings-grid">
          <label>
            Environment origin
            <input
              value={pair.origin}
              maxLength={2048}
              placeholder="https://…"
              onChange={(event) =>
                setPair({ ...pair, origin: event.target.value })
              }
            />
          </label>
          <label>
            Environment label
            <input
              value={pair.label}
              maxLength={128}
              onChange={(event) =>
                setPair({ ...pair, label: event.target.value })
              }
            />
          </label>
          <label>
            Bootstrap token
            <input
              type="password"
              autoComplete="off"
              value={pair.bootstrap}
              maxLength={2048}
              onChange={(event) =>
                setPair({ ...pair, bootstrap: event.target.value })
              }
            />
          </label>
          <label className="check-row">
            <input
              type="checkbox"
              checked={pair.isReadOnly}
              onChange={(event) =>
                setPair({ ...pair, isReadOnly: event.target.checked })
              }
            />
            Request read-only access
          </label>
        </div>
        <button
          disabled={!pair.origin || !pair.bootstrap}
          onClick={() =>
            void act(async () => {
              const input = pair;
              setPair({ ...pair, bootstrap: "" });
              const paired = await nativeApi.PairEnvironment(
                input.origin,
                input.label,
                input.bootstrap,
                input.isReadOnly,
              );
              draftCredentials.current.add(paired.connection.credentialId);
              changeConfig({
                connections: [
                  ...config.value.connections.filter(
                    (row) =>
                      row.connection.environmentId !==
                      paired.connection.environmentId,
                  ),
                  paired,
                ],
              });
              setPair({
                origin: "",
                label: "",
                bootstrap: "",
                isReadOnly: true,
              });
              setStatus(
                "Paired credential staged. Apply connections to activate it.",
              );
            })
          }
        >
          Pair and stage environment
        </button>
        <label>
          Primary theme environment
          <select
            disabled={!localIdentity}
            value={config.value.primaryEnvironmentId}
            onChange={(event) =>
              changeConfig({ primaryEnvironmentId: event.target.value })
            }
          >
            <option value={localIdentity}>This device</option>
            {config.value.connections.map((connection) => (
              <option
                key={connection.connection.environmentId}
                value={connection.connection.environmentId}
              >
                {connection.label || connection.connection.environmentId}
              </option>
            ))}
          </select>
        </label>
        <h2>Quota hubs</h2>
        <p>
          Management API origin and key. A changed origin requires a fresh key.
          Keys are never stored in the application database.
        </p>
        {config.value.hubs.map((hub, index) => (
          <div className="source-row" key={hub.id}>
            <label className="check-row">
              <input
                type="checkbox"
                checked={hub.enabled}
                onChange={(event) =>
                  changeConfig({
                    hubs: config.value.hubs.map((row, position) =>
                      position === index
                        ? { ...row, enabled: event.target.checked }
                        : row,
                    ),
                  })
                }
              />
              Enabled
            </label>
            <label>
              Hub label
              <input
                value={hub.label}
                maxLength={128}
                onChange={(event) =>
                  changeConfig({
                    hubs: config.value.hubs.map((row, position) =>
                      position === index
                        ? { ...row, label: event.target.value }
                        : row,
                    ),
                  })
                }
              />
            </label>
            <label>
              Hub origin
              <input
                value={hub.url}
                maxLength={2048}
                onChange={(event) =>
                  changeConfig({
                    hubs: config.value.hubs.map((row, position) =>
                      position === index
                        ? { ...row, url: event.target.value }
                        : row,
                    ),
                  })
                }
              />
            </label>
            <label>
              {hub.credentialId
                ? "Replace management key (leave blank to retain)"
                : "Management key"}
              <input
                type="password"
                autoComplete="off"
                maxLength={2048}
                value={hubKeys[hub.id] ?? ""}
                onChange={(event) =>
                  setHubKeys({ ...hubKeys, [hub.id]: event.target.value })
                }
              />
            </label>
            <button
              onClick={() =>
                changeConfig({
                  hubs: config.value.hubs.filter((row) => row.id !== hub.id),
                })
              }
            >
              Remove hub
            </button>
          </div>
        ))}
        <button
          disabled={config.value.hubs.length >= 32}
          onClick={() =>
            changeConfig({
              hubs: [
                ...config.value.hubs,
                {
                  id: crypto.randomUUID(),
                  label: "Quota hub",
                  url: "",
                  credentialId: "",
                  enabled: false,
                },
              ],
            })
          }
        >
          Add quota hub
        </button>
        {hasCredentialCleanup && (
          <p role="status">
            Settings were applied; unused draft credential cleanup needs retry.
            Use Discard connection changes to retry cleanup.
          </p>
        )}
        <div className="settings-actions">
          <button
            disabled={!isConfigDirty}
            onClick={() =>
              void act(async () => {
                const next = SavedConfiguration.createFrom(
                  JSON.stringify(config),
                );
                for (const hub of next.value.hubs) {
                  const key = hubKeys[hub.id];
                  if (key) {
                    hub.credentialId = await nativeApi.StageHubCredential(
                      hub.url,
                      key,
                    );
                    draftCredentials.current.add(hub.credentialId);
                    setConfig(
                      SavedConfiguration.createFrom(JSON.stringify(next)),
                    );
                    setHubKeys((current) => ({ ...current, [hub.id]: "" }));
                  }
                }
                setConfig(next);
                const saved = await nativeApi.SaveConfiguration(next);
                setConfig(saved);
                setSavedConfig(saved);
                const used = new Set([
                  ...saved.value.connections.map(
                    (row) => row.connection.credentialId,
                  ),
                  ...saved.value.hubs.map((row) => row.credentialId),
                ]);
                setHasCredentialCleanup(true);
                setStatus(
                  "Accounts and connections applied. Cleaning unused draft credentials…",
                );
                await nativeApi.DiscardDraftCredentials(
                  [...draftCredentials.current].filter(
                    (identity) => !used.has(identity),
                  ),
                );
                draftCredentials.current.clear();
                setHasCredentialCleanup(false);
                setHubKeys({});
                await data.reload();
                setStatus("Quota accounts and connections applied.");
              })
            }
          >
            Apply accounts &amp; connections
          </button>
          <button
            disabled={!isConfigDirty && !isPairDirty && !hasCredentialCleanup}
            onClick={() =>
              void act(async () => {
                await nativeApi.DiscardDraftCredentials([
                  ...draftCredentials.current,
                ]);
                draftCredentials.current.clear();
                setHasCredentialCleanup(false);
                setConfig(savedConfig);
                setHubKeys({});
                setPair({
                  origin: "",
                  label: "",
                  bootstrap: "",
                  isReadOnly: true,
                });
                setStatus("Connection draft discarded.");
              })
            }
          >
            Discard connection changes
          </button>
        </div>
      </fieldset>
      <output role="status">{status}</output>
    </section>
  );
}
