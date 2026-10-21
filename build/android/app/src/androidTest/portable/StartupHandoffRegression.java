package com.wails.app;

import android.os.Handler;
import android.webkit.WebView;

/** Exercise handoff ordering and actual bridge document tickets without Android rendering. */
public final class StartupHandoffRegression {
    public static void main(String[] arguments) {
        StartupHandoff state = new StartupHandoff();
        long first = state.content(true);
        state.webFrame(first);
        require(!state.finish(), "Web content dismissed before native first frame");
        state.nativeFrame();
        require(state.finish(), "Web-first ordering could not hand off");
        require(!state.finish(), "Repeated handoff");
        state.content(false);
        state.webFrame(state.content(true));
        require(!state.finish(), "Ordinary refresh resurrected loading");

        state = new StartupHandoff();
        state.nativeFrame();
        first = state.content(true);
        state.content(false);
        state.webFrame(first);
        require(!state.finish(), "Failure did not invalidate old visual completion");
        long second = state.content(true);
        state.webFrame(first);
        require(!state.finish(), "Old frame acknowledged replacement content");
        state.webFrame(second);
        require(state.finish(), "Native-first ordering could not hand off");
        state = new StartupHandoff();
        state.nativeFrame();
        first = state.content(true);
        state.cancel();
        state.webFrame(first);
        require(!state.finish(), "Recovery accepted an obsolete frame");

        MainActivity activity = new MainActivity();
        WailsBridge bridge = new WailsBridge(activity);
        WebView view = new WebView();
        bridge.setWebView(view);
        WailsJSBridge javascript = new WailsJSBridge(bridge, view);
        String ticket = javascript.startupDocument();
        javascript.setStartupContentReady(ticket, true);
        require(activity.startupCalls == 0, "Readiness bypassed UI queue");
        Handler.drain();
        require(activity.startupCalls == 1, "Current readiness was lost");
        javascript.setStartupContentReady(ticket, true);
        bridge.onDocumentStarted(view);
        Handler.drain();
        javascript.setStartupContentReady(ticket, true);
        Handler.drain();
        require(activity.startupCalls == 1, "Old document ticket escaped navigation");
        ticket = javascript.startupDocument();
        javascript.setStartupContentReady(ticket, true);
        javascript.detach();
        Handler.drain();
        require(activity.startupCalls == 1, "Detached interface completed startup");
        System.out.println("PASS startup frame ordering, one-shot handoff, cancellation and document tickets");
    }

    private static void require(boolean condition, String message) {
        if (!condition) throw new AssertionError(message);
    }
}
