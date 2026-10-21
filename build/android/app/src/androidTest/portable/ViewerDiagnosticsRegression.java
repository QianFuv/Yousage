package com.wails.app;

/** Exercise production visibility reasons without claiming to render an Android Activity. */
public final class ViewerDiagnosticsRegression {
    /** Cover pending startup, accepted offline rendering and independent failure/stall ordering. */
    public static void main(String[] arguments) {
        ViewerDiagnosticsState state = new ViewerDiagnosticsState();
        require(state.shouldShowEntry(), "startup must expose diagnostics before Go or WebView exists");
        state.setRendererUnresponsive(false);
        require(state.shouldShowEntry(), "responsive alone cannot acknowledge startup rendering");
        state.setViewerShown(true);
        require(!state.shouldShowEntry(), "fresh accepted cached rendering can hide without networking");
        System.out.println("PASS startup/pending versus accepted cached/offline render");

        state.setViewerShown(false);
        require(state.shouldShowEntry(), "leaving or failing the viewer must reveal diagnostics");
        state.setRendererUnresponsive(false);
        require(state.shouldShowEntry(), "responsive cannot clear standalone/render fault");
        state.setViewerShown(true);
        require(!state.shouldShowEntry(), "fresh accepted rendering must clear the render reason");
        System.out.println("PASS standalone/render fault survives responsive until accepted rendering");

        state.setRendererUnresponsive(true);
        state.setRendererUnresponsive(true);
        require(state.shouldShowEntry(), "repeated stalls must expose diagnostics");
        state.setViewerShown(true);
        require(state.shouldShowEntry(), "accepted render cannot clear an outstanding stall");
        state.setRendererUnresponsive(false);
        state.setRendererUnresponsive(false);
        require(!state.shouldShowEntry(), "responsive can clear a stall after accepted rendering");
        System.out.println("PASS accepted/stall/responsive ordering and repeated callbacks");

        state.setRendererUnresponsive(true);
        state.setViewerShown(false);
        state.setRendererUnresponsive(false);
        require(state.shouldShowEntry(), "responsive must preserve a later render failure");
        state.setRendererUnresponsive(true);
        state.setViewerShown(true);
        require(state.shouldShowEntry(), "render acceptance while stalled must remain visible");
        state.startViewer();
        require(state.shouldShowEntry(), "fresh WebView needs its own accepted rendering");
        state.setViewerShown(true);
        require(!state.shouldShowEntry(), "a disposed renderer's stall must not survive into a new WebView");
        System.out.println("PASS independent fault/stall reasons and fresh WebView reset");
    }

    /** Assert the recovery entry's observable visibility decision. */
    private static void require(boolean isSatisfied, String message) {
        if (!isSatisfied) throw new AssertionError(message);
    }
}
