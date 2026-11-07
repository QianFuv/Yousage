/** Phones swap wide header controls for compact menus beside the title instead of a second row. */
import type { ReactElement, ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { PageHeader } from "./ui";
import { ChoiceMenu } from "./usage/ChoiceMenu";

vi.mock(
  "../../tools/parity/upstream/apps/web/src/components/ui/button",
  () => ({
    Button: (props: { "aria-label"?: string; children?: ReactNode }) => (
      <button aria-label={props["aria-label"]}>{props.children}</button>
    ),
  }),
);
vi.mock(
  "../../tools/parity/upstream/apps/web/src/components/ui/menu",
  async () => {
    const { cloneElement } = await import("react");
    const Pass = ({ children }: { children?: ReactNode }) => <>{children}</>;
    return {
      Menu: Pass,
      MenuPopup: () => null,
      MenuRadioGroup: Pass,
      MenuRadioItem: Pass,
      MenuRadioItemIndicator: () => null,
      MenuTrigger: ({
        render,
        children,
      }: {
        render: ReactElement;
        children?: ReactNode;
      }) => cloneElement(render, {}, children),
    };
  },
);

/**
 * Find the class list of the element that directly wraps a marker.
 *
 * @param markup - Rendered header markup.
 * @param marker - Markup of the wrapped child.
 * @returns The wrapper's class attribute.
 */
function wrapperClass(markup: string, marker: string): string {
  const before = markup.slice(0, markup.indexOf(marker));
  return before.match(/<div class="([^"]*)">$/)?.[1] ?? "";
}

describe("page header on phones", () => {
  it("hides the wide controls on phones and shows the compact menu beside the actions", () => {
    const markup = renderToStaticMarkup(
      <PageHeader
        title="Usage"
        actions={<button>Refresh</button>}
        phoneControls={
          <ChoiceMenu
            label="Usage period"
            value="30"
            choices={[
              { value: "7", label: "7d" },
              { value: "30", label: "30d" },
            ]}
            onChange={() => {}}
          />
        }
      >
        <i>Wide toggle</i>
      </PageHeader>,
    );
    expect(wrapperClass(markup, "<i>Wide toggle</i>")).toContain(
      "max-md:hidden",
    );
    const menu = markup.indexOf('aria-label="Usage period: 30d"');
    expect(menu).toBeGreaterThan(-1);
    expect(markup.lastIndexOf("md:hidden", menu)).toBeGreaterThan(
      markup.indexOf("<i>Wide toggle</i>"),
    );
    expect(markup.indexOf("<button>Refresh</button>")).toBeGreaterThan(menu);
  });

  it("keeps the controls on their own phone row when a page offers no compact form", () => {
    const markup = renderToStaticMarkup(
      <PageHeader title="Quota">
        <i>Wide toggle</i>
      </PageHeader>,
    );
    const wide = wrapperClass(markup, "<i>Wide toggle</i>");
    expect(wide).toContain("max-md:basis-full");
    expect(wide).not.toContain("max-md:hidden");
  });
});
