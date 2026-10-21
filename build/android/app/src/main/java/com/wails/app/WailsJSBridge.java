package com.wails.app;

import android.util.Log;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import android.webkit.JavascriptInterface;
import android.webkit.WebView;
import com.wails.app.BuildConfig;

/**
 * WailsJSBridge provides the JavaScript interface that allows the web frontend
 * to communicate with the Go backend. This is exposed to JavaScript as the
 * `window.wails` object.
 *
 * Similar to iOS's WKScriptMessageHandler but using Android's addJavascriptInterface.
 */
public class WailsJSBridge {
    private static final String TAG = "WailsJSBridge";
    // Pooled threads avoid unbounded thread creation under high call volume.
    private static final ExecutorService executor = Executors.newCachedThreadPool();

    private final WailsBridge bridge;
    private final WebView webView;
    private final WailsBridge.WebViewBinding binding;
    private volatile boolean isAttached = true;

    public WailsJSBridge(WailsBridge bridge, WebView webView) {
        this.bridge = bridge;
        this.webView = webView;
        this.binding = bridge.captureWebViewBinding(webView);
    }

    /** Reject calls and replies belonging to a document that has been disposed. */
    void detach() {
        isAttached = false;
    }

    /** Check both interface lifetime and current native attachment before invoking Go. */
    private boolean canInvoke() {
        return isAttached && bridge.isCurrentBinding(binding);
    }

    /** Retain interface lifetime as well as attachment lifetime across queued native UI work. */
    private void postToCurrentViewer(Runnable action) {
        if (!canInvoke()) return;
        bridge.postForBinding(binding, () -> {
            if (canInvoke()) action.run();
        });
    }

    /**
     * Send a message to Go and return the response synchronously.
     * Called from JavaScript: wails.invoke(message)
     *
     * @param message The message to send (JSON string)
     * @return The response from Go (JSON string)
     */
    @JavascriptInterface
    public String invoke(String message) {
        if (!canInvoke()) return "{\"error\":\"Viewer detached\"}";
        return bridge.handleMessage(message);
    }

    /**
     * Send a message to Go asynchronously.
     * The response will be sent back via a callback.
     * Called from JavaScript: wails.invokeAsync(callbackId, message)
     *
     * @param callbackId The callback ID to use for the response
     * @param message The message to send (JSON string)
     */
    @JavascriptInterface
    public void invokeAsync(final String callbackId, final String payload) {
        if (!canInvoke()) return;

        // Handle off the JS thread so we don't block the WebView.
        executor.execute(() -> {
            if (!canInvoke()) return;
            try {
                String response = bridge.handleRuntimeCall(payload);
                sendCallback(callbackId, response, null);
            } catch (Exception e) {
                Log.e(TAG, "Error in async invoke");
                sendCallback(callbackId, null, e.getMessage());
            }
        });
    }

    /**
     * Retain the legacy console interface while discarding arbitrary caller content.
     * Fixed diagnostic categories use diagnostic() instead.
     */
    @JavascriptInterface
    public void log(String level, String message) {
    }

    /** Accept a fixed render/internal category only from the currently attached document. */
    @JavascriptInterface
    public void diagnostic(String category) {
        if (canInvoke() && ("render".equals(category) || "internal".equals(category))) {
            postToCurrentViewer(() -> bridge.setViewerShown(false));
            bridge.recordFrontendDiagnostic(category);
        }
    }

    /** Report fresh accepted rendering, or leaving/failing the current viewer, without payload data. */
    @JavascriptInterface
    public void setViewerShown(boolean isShown) {
        postToCurrentViewer(() -> bridge.setViewerShown(isShown));
    }

    /** Open the native diagnostics dialog from frontend settings without a Go round trip. */
    @JavascriptInterface
    public void showDiagnostics() {
        postToCurrentViewer(() -> bridge.showDiagnostics());
    }

    /**
     * Get the platform name
     * Called from JavaScript: wails.platform()
     *
     * @return "android"
     */
    @JavascriptInterface
    public String platform() {
        return "android";
    }

    /**
     * Check if we're running in debug mode
     * Called from JavaScript: wails.isDebug()
     *
     * @return true if debug build, false otherwise
     */
    @JavascriptInterface
    public boolean isDebug() {
        return BuildConfig.DEBUG;
    }

    /**
     * Send a callback response to JavaScript
     */
    private void sendCallback(String callbackId, String result, String error) {
        if (!canInvoke()) return;
        final String js;
        if (error != null) {
            js = String.format(
                    "window._wailsAndroidCallback && window._wailsAndroidCallback('%s', null, '%s');",
                    escapeJsString(callbackId),
                    escapeJsString(error)
            );
        } else {
            js = String.format(
                    "window._wailsAndroidCallback && window._wailsAndroidCallback('%s', '%s', null);",
                    escapeJsString(callbackId),
                    escapeJsString(result != null ? result : "")
            );
        }

        bridge.executeJavaScript(webView, js);
    }

    private String escapeJsString(String str) {
        if (str == null) return "";
        return str.replace("\\", "\\\\")
                .replace("'", "\\'")
                .replace("\n", "\\n")
                .replace("\r", "\\r")
                // JS line terminators (U+2028/U+2029) must be escaped too; built via
                // (char) casts so the Java lexer does not reinterpret them as newlines.
                .replace(String.valueOf((char) 0x2028), "\\u2028")
                .replace(String.valueOf((char) 0x2029), "\\u2029");
    }
}
