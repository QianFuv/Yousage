package com.wails.app;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.graphics.Typeface;
import android.widget.ScrollView;
import android.widget.TextView;

import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/** Native read-only diagnostics dialog, usable without Go, React or a working WebView. */
public final class DiagnosticsUi {
    private static final ThreadPoolExecutor READER = new ThreadPoolExecutor(1, 1, 30, TimeUnit.SECONDS,
            new ArrayBlockingQueue<>(1), action -> {
                Thread thread = new Thread(action, "diagnostics-reader");
                thread.setDaemon(true);
                return thread;
            });

    /** Utility class; every dialog belongs to its current Activity. */
    private DiagnosticsUi() {
    }

    /** Open from a native menu/button, including startup/recovery UI before Go is available. */
    public static void show(Activity activity) {
        if (activity == null) return;
        try {
            show(activity, DiagnosticsStore.initialize(activity.getFilesDir()));
        } catch (RuntimeException ignored) {
        }
    }

    /** Show an already initialized process store without accessing any bridge or WebView. */
    public static void show(Activity activity, DiagnosticsStore store) {
        if (activity == null || store == null) return;
        try {
            activity.runOnUiThread(() -> {
                try {
                    if (activity.isFinishing() || activity.isDestroyed()) return;
                    TextView text = new TextView(activity);
                    text.setText("Loading diagnostics…");
                    text.setTextIsSelectable(true);
                    text.setTypeface(Typeface.MONOSPACE);
                    int padding = (int) (16 * activity.getResources().getDisplayMetrics().density);
                    text.setPadding(padding, padding, padding, padding);
                    ScrollView scroll = new ScrollView(activity);
                    scroll.addView(text);
                    AlertDialog dialog = new AlertDialog.Builder(activity)
                            .setTitle("Diagnostics")
                            .setView(scroll)
                            .setPositiveButton("Refresh", null)
                            .setNeutralButton("Share", null)
                            .setNegativeButton("Close", (closedDialog, which) -> closedDialog.dismiss())
                            .create();
                    AtomicBoolean isBusy = new AtomicBoolean();
                    dialog.setOnShowListener(shownDialog -> {
                        dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(
                                view -> read(activity, dialog, text, store, isBusy, false));
                        dialog.getButton(AlertDialog.BUTTON_NEUTRAL).setOnClickListener(
                                view -> read(activity, dialog, text, store, isBusy, true));
                        read(activity, dialog, text, store, isBusy, false);
                    });
                    dialog.show();
                } catch (RuntimeException ignored) {
                }
            });
        } catch (RuntimeException ignored) {
        }
    }

    /** Re-read/revalidate for each refresh and explicit share; bound worker threads and queued reads. */
    private static void read(Activity activity, AlertDialog dialog, TextView text, DiagnosticsStore store,
            AtomicBoolean isBusy, boolean shouldShare) {
        if (!isBusy.compareAndSet(false, true)) return;
        try {
            READER.execute(() -> {
                try {
                    DiagnosticsStore.Report report = store.readReport();
                    activity.runOnUiThread(() -> {
                        try {
                            if (activity.isFinishing() || activity.isDestroyed() || !dialog.isShowing()) return;
                            text.setText(report.isAvailable ? report.text : "Diagnostics unavailable\n" + report.text);
                            if (shouldShare) {
                                Intent send = new Intent(Intent.ACTION_SEND)
                                        .setType("text/plain")
                                        .putExtra(Intent.EXTRA_TEXT, report.text);
                                activity.startActivity(Intent.createChooser(send, "Share diagnostics"));
                            }
                        } catch (RuntimeException ignored) {
                        } finally {
                            isBusy.set(false);
                        }
                    });
                } catch (RuntimeException ignored) {
                    isBusy.set(false);
                }
            });
        } catch (RuntimeException ignored) {
            isBusy.set(false);
        }
    }
}
