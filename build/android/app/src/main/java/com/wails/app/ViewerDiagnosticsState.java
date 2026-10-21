package com.wails.app;

/** Keep render readiness independent of a live renderer's responsiveness on the UI thread. */
final class ViewerDiagnosticsState {
    private boolean isAwaitingRender = true;
    private boolean isUnresponsive;

    /** Require fresh accepted-render evidence for each newly constructed WebView. */
    void startViewer() {
        isAwaitingRender = true;
        isUnresponsive = false;
    }

    /** Accept current frontend readiness without clearing an outstanding renderer stall. */
    void setViewerShown(boolean isShown) {
        isAwaitingRender = !isShown;
    }

    /** Clear only the stall reason when the platform reports responsiveness. */
    void setRendererUnresponsive(boolean isUnresponsive) {
        this.isUnresponsive = isUnresponsive;
    }

    /** Keep independent native access until rendering and responsiveness are both healthy. */
    boolean shouldShowEntry() {
        return isAwaitingRender || isUnresponsive;
    }
}
