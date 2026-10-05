/** Fixture host for actual upstream chart, share bar, theme wireframe, and preview circles. */
import React, { useState, useCallback } from "react";
import "../tools/parity/upstream/apps/web/src/index.css";
import "../tools/parity/src/reference.css";
import { UsageProviderChart } from "../tools/parity/upstream/apps/web/src/components/usage/UsageProviderChart";
import { UsageShareBar } from "../tools/parity/upstream/apps/web/src/components/usage/UsageShareBar";
import { ThemeWireframe } from "../tools/parity/upstream/apps/web/src/components/settings/ThemeWireframe";
import {
  ThemePreviewCircles,
  STANDARD_THEME_CARDS,
  getThemeCardDefinition,
} from "../tools/parity/upstream/apps/web/src/components/settings/ThemePreviewCircles";
import { BUILT_IN_THEMES } from "../tools/parity/upstream/packages/shared/src/themePalettes";
import { applyThemePalette } from "../tools/parity/upstream/apps/web/src/themePalette";
import { DAYS, DAILY, PROVIDERS } from "../tools/parity/src/fixtures";
import { formatUsd } from "../tools/parity/upstream/packages/shared/src/usageFormat";
import {
  Dialog,
  DialogTrigger,
  DialogPopup,
  DialogTitle,
  DialogDescription,
  DialogHeader,
} from "../tools/parity/upstream/apps/web/src/components/ui/dialog";
import {
  Menu,
  MenuTrigger,
  MenuPopup,
  MenuItem,
} from "../tools/parity/upstream/apps/web/src/components/ui/menu";
import { ThemeEditorPanel } from "../tools/parity/upstream/apps/web/src/components/settings/ThemeEditorPanel";
import { Button } from "../tools/parity/upstream/apps/web/src/components/ui/button";

/** Apply a deterministic URL-selected palette before React's first rendered frame. */
const PARAMETERS = new URLSearchParams(location.search);
const APPEARANCE = PARAMETERS.get("appearance") === "dark" ? "dark" : "light";
document.documentElement.classList.toggle("dark", APPEARANCE === "dark");
applyThemePalette(PARAMETERS.get("theme") ?? "default", APPEARANCE);

/** Present original component bodies with explicitly synthetic data and host-only controls. */
export function Reference({
  appearance = APPEARANCE,
  onAppearanceChange,
  view = "usage",
  shouldManageTheme = true,
}: {
  appearance?: "light" | "dark";
  onAppearanceChange?: (appearance: "light" | "dark") => void;
  view?: "usage" | "appearance";
  shouldManageTheme?: boolean;
}) {
  const [metric, setMetric] = useState<"cost" | "tokens">("cost");
  const [isEditorOpen, setIsEditorOpen] = useState(false);
  const [page, setPage] = useState("Usage");
  const [selectedTheme, setSelectedTheme] = useState(
    PARAMETERS.get("theme") ?? "default",
  );
  const restoreTheme = useCallback(() => {
    document.documentElement.classList.toggle("dark", appearance === "dark");
    applyThemePalette(selectedTheme, appearance);
  }, [appearance, selectedTheme]);
  React.useEffect(() => {
    if (shouldManageTheme) applyThemePalette(selectedTheme, appearance);
  }, [appearance, selectedTheme, shouldManageTheme]);
  const cards = [
    ...STANDARD_THEME_CARDS,
    ...BUILT_IN_THEMES.map(getThemeCardDefinition),
  ];
  return (
    <main className="reference-shell bg-background text-foreground">
      <header>
        <h1>{view === "usage" ? "Usage" : "Themes"}</h1>
        <p>
          Synthetic fixtures · 51f7f77 · {appearance} · {page}
        </p>
        <div className="flex flex-wrap gap-2 mt-3" hidden={view === "usage"}>
          <Menu>
            <MenuTrigger render={<Button variant="outline" />}>
              Fixture menu
            </MenuTrigger>
            <MenuPopup>
              <MenuItem
                onClick={() => setPage(page === "Usage" ? "Themes" : "Usage")}
              >
                Switch fixture page
              </MenuItem>
              <MenuItem onClick={() => setIsEditorOpen(true)}>
                Open theme editor
              </MenuItem>
            </MenuPopup>
          </Menu>
          <Dialog>
            <DialogTrigger render={<Button variant="outline" />}>
              Fixture dialog
            </DialogTrigger>
            <DialogPopup>
              <DialogHeader>
                <DialogTitle>Reference dialog</DialogTitle>
                <DialogDescription>
                  Original portal, focus and responsive dialog primitives with
                  synthetic content.
                </DialogDescription>
              </DialogHeader>
            </DialogPopup>
          </Dialog>
          <Button variant="outline" onClick={() => setIsEditorOpen(true)}>
            Floating editor
          </Button>
          <Button
            variant="ghost"
            onClick={() => setPage(page === "Usage" ? "Themes" : "Usage")}
          >
            Navigate fixture
          </Button>
        </div>
      </header>
      <section
        className="rounded-xl border border-border bg-card p-5"
        hidden={view !== "usage"}
      >
        <div className="flex justify-between">
          <h2>Provider chart</h2>
          <button
            onClick={() => setMetric(metric === "cost" ? "tokens" : "cost")}
          >
            Metric: {metric}
          </button>
        </div>
        <UsageProviderChart
          providers={[...PROVIDERS]}
          days={DAYS}
          daily={DAILY}
          hours={[]}
          hourly={[]}
          metric={metric}
          referenceTime="2026-10-06T12:00:00Z"
          resolution="day"
          timeZone="UTC"
        />
        <UsageShareBar
          label="Provider share"
          segments={PROVIDERS.map((provider, index) => ({
            label: provider,
            value: index + 1,
            color: [
              "#5b9bbd",
              "#d97757",
              "#999999",
              "#8b8b8b",
              "#5b9bbd",
              "#8c7bd1",
            ][index],
          }))}
          format={formatUsd}
        />
      </section>
      <section className="theme-grid" hidden={view !== "appearance"}>
        {cards.map((card) => (
          <article
            className="rounded-xl border border-border bg-card p-3"
            key={card.id}
          >
            <h2>{card.label}</h2>
            <ThemeWireframe
              className="h-32"
              panes={card.previews.map((preview) => ({
                colors: preview.colors,
                clip: preview.mode === "light" ? "left" : "right",
              }))}
            />
            <ThemePreviewCircles
              label={card.label}
              previews={card.previews}
              activeModes={[]}
              onSelectMode={(mode) => {
                if (mode === "system") return;
                setSelectedTheme(card.id);
                onAppearanceChange?.(mode);
                document.documentElement.classList.toggle(
                  "dark",
                  mode === "dark",
                );
                applyThemePalette(card.id, mode);
              }}
            />
          </article>
        ))}
      </section>
      {isEditorOpen && (
        <ThemeEditorPanel
          open={isEditorOpen}
          onOpenChange={setIsEditorOpen}
          onSaved={() => true}
          editingTheme={null}
          initialAppearance={appearance}
          seedName="Fixture Theme"
          restoreTheme={restoreTheme}
        />
      )}
    </main>
  );
}
