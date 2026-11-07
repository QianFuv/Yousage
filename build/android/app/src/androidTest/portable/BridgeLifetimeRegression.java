package com.wails.app;

import android.os.Handler;
import android.webkit.WebView;
import android.util.Log;
import java.lang.reflect.Method;
import java.lang.reflect.Field;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Comparator;
import java.util.stream.Stream;

/** Exercise actual bridge dispatch code with synthetic calls and a deterministic host UI queue. */
public final class BridgeLifetimeRegression {
    private static int failureCount;
    private static final String CANARY = "PRIVATE-CANARY-token-path-payload";

    /** Record backend calls without initializing JNI or reading application data. */
    private static final class RecordingBridge extends WailsBridge {
        int messageCount;
        int diagnosticCount;
        int diagnosticsUiCount;

        RecordingBridge() {
            super(null);
        }

        @Override
        public String handleMessage(String message) {
            messageCount++;
            return "synthetic-response";
        }

        /** Count diagnostic dispatch without JNI; also compiles against the pre-diagnostics baseline. */
        public void recordFrontendDiagnostic(String category) {
            diagnosticCount++;
        }

        /** Count native UI requests without opening a device dialog. */
        public void showDiagnostics() {
            diagnosticsUiCount++;
        }
    }

    /** Run all lifetime cases, allowing the baseline to expose independent failures. */
    public static void main(String[] arguments) throws Exception {
        run("active native delivery", BridgeLifetimeRegression::activeNativeDelivery);
        run("queued native script after detach", BridgeLifetimeRegression::queuedNativeScriptAfterDetach);
        run("queued native script after replacement", BridgeLifetimeRegression::queuedNativeScriptAfterReplacement);
        run("same-view attachment invalidation", BridgeLifetimeRegression::sameViewAttachmentInvalidation);
        run("stale interface backend call", BridgeLifetimeRegression::staleInterfaceBackendCall);
        run("late JavaScript callback after replacement", BridgeLifetimeRegression::lateCallbackAfterReplacement);
        run("queued JavaScript callback after detach", BridgeLifetimeRegression::queuedCallbackAfterDetach);
        run("active JavaScript callback", BridgeLifetimeRegression::activeCallback);
        run("current native diagnostics interface", BridgeLifetimeRegression::activeDiagnostics);
        run("detached native diagnostics interface", BridgeLifetimeRegression::detachedDiagnostics);
        run("replaced native diagnostics interface", BridgeLifetimeRegression::replacedDiagnostics);
        run("old interface after same-view reattachment", BridgeLifetimeRegression::sameViewInterfaceInvalidation);
        run("queued diagnostics UI after same-view reattachment", BridgeLifetimeRegression::queuedDiagnosticsAfterRebind);
        run("frontend failures keep the logging allowance", BridgeLifetimeRegression::rateLimitedFailure);
        run("legacy console and native logs redact caller data", BridgeLifetimeRegression::logPrivacy);
        if (failureCount != 0) throw new AssertionError(failureCount + " lifetime regressions failed");
    }

    /** Run and report one independent assertion without hiding later baseline failures. */
    private static void run(String name, Runnable test) {
        Handler.drain();
        try {
            test.run();
            System.out.println("PASS " + name);
        } catch (AssertionError error) {
            failureCount++;
            System.out.println("FAIL " + name + ": " + error.getMessage());
        }
        Handler.drain();
    }

    /** Confirm attached documents still receive current native work. */
    private static void activeNativeDelivery() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        bridge.executeJavaScript("synthetic-current");
        Handler.drain();
        require(view.scripts.size() == 1, "current document lost its script");
    }

    /** Queued events must never execute on a document destroyed before the UI queue drains. */
    private static void queuedNativeScriptAfterDetach() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        bridge.executeJavaScript("synthetic-stale");
        bridge.setWebView(null);
        view.isDestroyed = true;
        Handler.drain();
        require(view.scripts.isEmpty(), "detached document received work");
    }

    /** A replacement document must receive its own work without reviving old work. */
    private static void queuedNativeScriptAfterReplacement() {
        RecordingBridge bridge = new RecordingBridge();
        WebView previous = new WebView();
        WebView current = new WebView();
        bridge.setWebView(previous);
        bridge.executeJavaScript("synthetic-stale");
        bridge.setWebView(current);
        bridge.executeJavaScript("synthetic-current");
        Handler.drain();
        require(previous.scripts.isEmpty(), "old attachment received a queued event");
        require(current.scripts.size() == 1, "replacement received missing or stale work");
    }

    /** Rebinding the same object must still invalidate the prior attachment's queued work. */
    private static void sameViewAttachmentInvalidation() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        bridge.executeJavaScript("synthetic-stale");
        bridge.setWebView(view);
        Handler.drain();
        require(view.scripts.isEmpty(), "attachment identity was reduced to view identity");
    }

    /** An interface from the old view must not dispatch new backend calls after replacement. */
    private static void staleInterfaceBackendCall() {
        RecordingBridge bridge = new RecordingBridge();
        WebView previous = new WebView();
        bridge.setWebView(previous);
        WailsJSBridge javascriptBridge = new WailsJSBridge(bridge, previous);
        bridge.setWebView(new WebView());
        javascriptBridge.invoke("synthetic-request");
        require(bridge.messageCount == 0, "stale interface invoked the backend");
    }

    /** A completed async reply from a replaced document must reach neither view. */
    private static void lateCallbackAfterReplacement() {
        RecordingBridge bridge = new RecordingBridge();
        WebView previous = new WebView();
        WebView current = new WebView();
        bridge.setWebView(previous);
        WailsJSBridge javascriptBridge = new WailsJSBridge(bridge, previous);
        bridge.setWebView(current);
        reply(javascriptBridge);
        Handler.drain();
        require(previous.scripts.isEmpty() && current.scripts.isEmpty(), "late callback escaped its document");
    }

    /** Detaching between callback enqueue and UI execution must cancel that reply. */
    private static void queuedCallbackAfterDetach() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        WailsJSBridge javascriptBridge = new WailsJSBridge(bridge, view);
        reply(javascriptBridge);
        bridge.setWebView(null);
        view.isDestroyed = true;
        Handler.drain();
        require(view.scripts.isEmpty(), "queued reply reached a detached document");
    }

    /** Healthy async callback dispatch must remain functional. */
    private static void activeCallback() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        reply(new WailsJSBridge(bridge, view));
        Handler.drain();
        require(view.scripts.size() == 1 && view.scripts.get(0).contains("synthetic-result"),
                "active callback was not delivered");
    }

    /** Current settings/errors dispatch directly without relying on a running Go backend. */
    private static void activeDiagnostics() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        WailsJSBridge javascriptBridge = new WailsJSBridge(bridge, view);
        diagnostic(javascriptBridge, "render");
        openDiagnostics(javascriptBridge);
        Handler.drain();
        require(bridge.diagnosticCount == 1 && bridge.diagnosticsUiCount == 1, "current diagnostic interface did not dispatch");
    }

    /** Detached interfaces must not log failures or reopen native UI after their document dies. */
    private static void detachedDiagnostics() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        WailsJSBridge javascriptBridge = new WailsJSBridge(bridge, view);
        javascriptBridge.detach();
        diagnostic(javascriptBridge, "internal");
        openDiagnostics(javascriptBridge);
        require(bridge.diagnosticCount == 0 && bridge.diagnosticsUiCount == 0, "detached diagnostic interface dispatched");
    }

    /** A replacement document invalidates the old document's diagnostic entry points. */
    private static void replacedDiagnostics() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        WailsJSBridge javascriptBridge = new WailsJSBridge(bridge, view);
        bridge.setWebView(new WebView());
        diagnostic(javascriptBridge, "render");
        openDiagnostics(javascriptBridge);
        require(bridge.diagnosticCount == 0 && bridge.diagnosticsUiCount == 0, "replaced diagnostic interface dispatched");
    }

    /** Use the real bridges and state with only the final Activity UI sink replaced by a recording fake. */
    private static final class ViewerFixture {
        final MainActivity activity = new MainActivity();
        final WailsBridge bridge = new WailsBridge(activity);
        final WebView view = new WebView();
        final WailsJSBridge javascriptBridge;

        /** Construct synthetic native attachment without JNI initialization or real app data. */
        ViewerFixture() {
            bridge.setWebView(view);
            javascriptBridge = new WailsJSBridge(bridge, view);
        }
    }

    /** Old interfaces cannot revive by issuing new calls after the same WebView is rebound. */
    private static void sameViewInterfaceInvalidation() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        WailsJSBridge previous = new WailsJSBridge(bridge, view);
        bridge.setWebView(view);
        previous.invoke("synthetic-stale");
        diagnostic(previous, "render");
        openDiagnostics(previous);
        reply(previous);
        Handler.drain();
        require(bridge.messageCount == 0 && bridge.diagnosticCount == 0 && bridge.diagnosticsUiCount == 0 && view.scripts.isEmpty(),
                "old interface revived after same-view attachment");
        WailsJSBridge current = new WailsJSBridge(bridge, view);
        current.invoke("synthetic-current");
        diagnostic(current, "internal");
        openDiagnostics(current);
        Handler.drain();
        require(bridge.messageCount == 1 && bridge.diagnosticCount == 1 && bridge.diagnosticsUiCount == 1,
                "new interface lost current attachment access");
    }

    /** Native Settings UI work needs the same queued attachment guard as visibility signals. */
    private static void queuedDiagnosticsAfterRebind() {
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        WailsJSBridge javascriptBridge = new WailsJSBridge(bridge, view);
        openDiagnostics(javascriptBridge);
        bridge.setWebView(view);
        Handler.drain();
        require(bridge.diagnosticsUiCount == 0, "queued settings diagnostics escaped its attachment");
    }

    /** Exercise the real store allowance: failures beyond ten per minute are not logged. */
    private static void rateLimitedFailure() {
        Path root = null;
        try {
            root = Files.createTempDirectory("viewer-visibility-rate-");
            DiagnosticsStore store = DiagnosticsStore.initialize(root.toFile());
            ViewerFixture fixture = new ViewerFixture();
            for (int index = 0; index < 10; index++) diagnostic(fixture.javascriptBridge, "internal");
            Handler.drain();
            String before = store.readReport().text;
            require(before.split("frontend_error", -1).length - 1 == 10, "synthetic log allowance was not filled");
            diagnostic(fixture.javascriptBridge, "render");
            Handler.drain();
            require(store.readReport().text.equals(before), "logging rate limit or stored log retention changed");
            Field count = DiagnosticsStore.class.getDeclaredField("frontendCount");
            count.setAccessible(true);
            require(count.getInt(store) == 10, "native intake exceeded the existing ten-per-minute allowance");
        } catch (Exception failure) {
            throw new AssertionError("could not exercise synthetic logging allowance", failure);
        } finally {
            if (root != null) {
                try (Stream<Path> paths = Files.walk(root)) {
                    for (Path path : (Iterable<Path>) paths.sorted(Comparator.reverseOrder())::iterator) Files.delete(path);
                } catch (Exception failure) {
                    throw new AssertionError("could not clean synthetic temporary logs", failure);
                }
            }
        }
    }

    /** Caller payloads, paths and arbitrary console values must not reach logcat, even in debug. */
    private static void logPrivacy() {
        Log.messages.clear();
        RecordingBridge bridge = new RecordingBridge();
        WebView view = new WebView();
        bridge.setWebView(view);
        WailsJSBridge javascriptBridge = new WailsJSBridge(bridge, view);
        javascriptBridge.invoke(CANARY);
        javascriptBridge.log("error", CANARY);
        bridge.serveAsset(CANARY, "GET", "{}");
        new WailsPathHandler(bridge).handle(CANARY);
        require(Log.messages.stream().noneMatch(value -> value.contains(CANARY)), "raw caller content reached native logs");
        javascriptBridge.log(null, CANARY);
    }

    /** Invoke new API reflectively so regression cases can also compile against the old baseline. */
    private static void diagnostic(WailsJSBridge javascriptBridge, String category) {
        try {
            WailsJSBridge.class.getMethod("diagnostic", String.class).invoke(javascriptBridge, category);
        } catch (ReflectiveOperationException failure) {
            throw new AssertionError("Native diagnostic API unavailable", failure);
        }
    }

    /** Invoke the new settings entry point while keeping the before-state fixture source compatible. */
    private static void openDiagnostics(WailsJSBridge javascriptBridge) {
        try {
            WailsJSBridge.class.getMethod("showDiagnostics").invoke(javascriptBridge);
        } catch (ReflectiveOperationException failure) {
            throw new AssertionError("Native diagnostics UI entry unavailable", failure);
        }
    }

    /** Simulate async completion deterministically through the production reply method. */
    private static void reply(WailsJSBridge javascriptBridge) {
        try {
            Method callback = WailsJSBridge.class.getDeclaredMethod(
                    "sendCallback", String.class, String.class, String.class);
            callback.setAccessible(true);
            callback.invoke(javascriptBridge, "synthetic-id", "synthetic-result", null);
        } catch (ReflectiveOperationException error) {
            throw new AssertionError("Could not exercise production callback", error);
        }
    }

    /** Assert an observable dispatch outcome. */
    private static void require(boolean isSatisfied, String message) {
        if (!isSatisfied) throw new AssertionError(message);
    }
}
