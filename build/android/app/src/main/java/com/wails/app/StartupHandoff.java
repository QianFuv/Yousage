package com.wails.app;

/** Keep native-frame and current web-paint evidence independent until one startup handoff. */
final class StartupHandoff {
    private long revision;
    private boolean hasNativeFrame;
    private boolean isPending = true;
    private boolean isReady;
    private boolean hasWebFrame;

    /** Invalidate readiness without showing a completed loading presentation again. */
    long content(boolean ready) {
        revision++;
        isReady = ready;
        hasWebFrame = false;
        return revision;
    }

    /** Accept a frame only for the current ready content. */
    void webFrame(long request) {
        if (isPending && isReady && request == revision) hasWebFrame = true;
    }

    /** Record the first opaque native content frame independently of web progress. */
    void nativeFrame() {
        hasNativeFrame = true;
    }

    /** Permit exactly one dismissal after both sides have rendered. */
    boolean finish() {
        if (!isPending || !hasNativeFrame || !isReady || !hasWebFrame) return false;
        isPending = false;
        return true;
    }

    /** Native recovery replaces the loading presentation without requiring frontend success. */
    void cancel() {
        content(false);
        isPending = false;
    }

    boolean isPending() {
        return isPending;
    }
}
