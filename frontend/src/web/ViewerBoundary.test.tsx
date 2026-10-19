/** A boundary shows its fallback after a failure and renders the viewer again on retry or a newer snapshot. */
import { describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ViewerBoundary } from "./ViewerBoundary";

describe("ViewerBoundary", () => {
  it("renders its children until something fails", () => {
    expect(
      renderToStaticMarkup(
        <ViewerBoundary resetKey={0} fallback={() => "fallback"}>
          viewer
        </ViewerBoundary>,
      ),
    ).toBe("viewer");
  });

  it("switches to the fallback after a failure and clears it for a new reset key only", () => {
    const failed = {
      hasFailed: true,
      resetKey: 1,
    };
    expect(ViewerBoundary.getDerivedStateFromError()).toEqual({
      hasFailed: true,
    });
    const props = { resetKey: 1, fallback: () => null, children: null };
    expect(ViewerBoundary.getDerivedStateFromProps(props, failed)).toBeNull();
    expect(
      ViewerBoundary.getDerivedStateFromProps(
        { ...props, resetKey: 2 },
        failed,
      ),
    ).toEqual({ hasFailed: false, resetKey: 2 });
  });

  it("reports caught failures and runs the retry action before rendering again", () => {
    const onError = vi.fn();
    const onRetry = vi.fn();
    let retry: () => void = () => {};
    const boundary = new ViewerBoundary({
      resetKey: 0,
      onError,
      onRetry,
      fallback: (action) => {
        retry = action;
        return "fallback";
      },
      children: "viewer",
    });
    boundary.setState = vi.fn();
    boundary.componentDidCatch(new Error("render failed"));
    expect(onError).toHaveBeenCalledWith(new Error("render failed"));
    boundary.state = { hasFailed: true, resetKey: 0 };
    expect(boundary.render()).toBe("fallback");
    retry();
    expect(onRetry).toHaveBeenCalledOnce();
    expect(boundary.setState).toHaveBeenCalledWith({ hasFailed: false });
  });
});
