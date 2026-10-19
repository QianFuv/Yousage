/** Contain a rendering failure in the viewer so it shows a way forward instead of a blank page. */
import { Component, type ReactNode } from "react";

/** What a boundary shows and whom it tells about a failure. */
type ViewerBoundaryProps = {
  /** A new value clears a failure and renders the children again, as after a newer snapshot. */
  resetKey: number;
  /** Told about each failure the boundary catches. */
  onError?: (error: unknown) => void;
  /** Runs before a retry renders the children again. */
  onRetry?: () => void;
  /** Shown instead of the children after a failure, with a way to render them again. */
  fallback: (retry: () => void) => ReactNode;
  children: ReactNode;
};

/** Whether the children failed since the boundary last saw its reset key change. */
type ViewerBoundaryState = { hasFailed: boolean; resetKey: number };

/** Show a fallback when the children throw while rendering, until a retry or a new reset key. */
export class ViewerBoundary extends Component<
  ViewerBoundaryProps,
  ViewerBoundaryState
> {
  state: ViewerBoundaryState = {
    hasFailed: false,
    resetKey: this.props.resetKey,
  };

  /**
   * Switch to the fallback after a child threw.
   *
   * @returns The failed state.
   */
  static getDerivedStateFromError(): Partial<ViewerBoundaryState> {
    return { hasFailed: true };
  }

  /**
   * Render the children again once the reset key changes.
   *
   * @param props - Current props.
   * @param state - Current state.
   * @returns A cleared state for a new reset key, otherwise no change.
   */
  static getDerivedStateFromProps(
    props: ViewerBoundaryProps,
    state: ViewerBoundaryState,
  ): Partial<ViewerBoundaryState> | null {
    return props.resetKey === state.resetKey
      ? null
      : { hasFailed: false, resetKey: props.resetKey };
  }

  /**
   * Report a caught failure.
   *
   * @param error - What a child threw.
   */
  componentDidCatch(error: unknown) {
    this.props.onError?.(error);
  }

  /** Render the children again after the person asks to try again. */
  private retry = () => {
    this.props.onRetry?.();
    this.setState({ hasFailed: false });
  };

  /**
   * Render the children, or the fallback after a failure.
   *
   * @returns The rendered content.
   */
  render() {
    return this.state.hasFailed
      ? this.props.fallback(this.retry)
      : this.props.children;
  }
}
