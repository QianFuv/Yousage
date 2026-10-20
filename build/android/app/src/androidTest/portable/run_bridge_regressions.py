"""Run bridge lifetime regressions on a host JVM without JNI, a device, or app data.

Compile complete production bridge sources against the installed Android SDK and
cached Gradle dependencies. Only the native library loader is disabled in a
temporary source copy. Runtime fakes supply a deterministic UI queue and recording
WebViews; this does not verify Android rendering or native Go behavior.
"""

import argparse
import os
from pathlib import Path
import subprocess
import tempfile

from run_diagnostics_regressions import STUBS as DIAGNOSTICS_STUBS


STUBS = {
    **DIAGNOSTICS_STUBS,
    "android/content/Context.java": "package android.content; public class Context {}",
    "android/app/Activity.java": (
        "package android.app; public class Activity extends android.content.Context {}"
    ),
    "android/view/View.java": "package android.view; public class View {}",
    "android/os/Looper.java": """
package android.os;
public class Looper {
    public static Looper getMainLooper() { return new Looper(); }
}
""",
    "android/os/Handler.java": """
package android.os;
import java.util.ArrayDeque;
public class Handler {
    private static final ArrayDeque<Runnable> QUEUE = new ArrayDeque<>();
    public Handler(Looper looper) {}
    public boolean post(Runnable action) { QUEUE.add(action); return true; }
    public static void drain() {
        while (!QUEUE.isEmpty()) QUEUE.remove().run();
    }
}
""",
    "android/util/Log.java": """
package android.util;
public class Log {
    public static final java.util.List<String> messages = new java.util.ArrayList<>();
    public static int d(String tag, String message) { messages.add(message); return 0; }
    public static int w(String tag, String message) { messages.add(message); return 0; }
    public static int i(String tag, String message) { messages.add(message); return 0; }
    public static int v(String tag, String message) { messages.add(message); return 0; }
    public static int e(String tag, String message) { messages.add(message); return 0; }
    public static int e(String tag, String message, Throwable error) { messages.add(message + error); return 0; }
}
""",
    "android/webkit/WebView.java": """
package android.webkit;
import java.util.ArrayList;
import java.util.List;
public class WebView extends android.view.View {
    public final List<String> scripts = new ArrayList<>();
    public boolean isDestroyed;
    public void evaluateJavascript(String script, ValueCallback<String> callback) {
        if (isDestroyed) throw new AssertionError("JavaScript reached a destroyed view");
        scripts.add(script);
    }
    public boolean post(Runnable action) {
        return new android.os.Handler(android.os.Looper.getMainLooper()).post(action);
    }
}
""",
}


def run_java(arguments, executable, directory):
    """Run a bounded Java tool invocation through an argument file.

    Args:
        arguments: Java tool arguments.
        executable: Installed java or javac executable.
        directory: Temporary directory for the argument file.
    """
    argument_file = directory / (executable.stem + ".args")
    argument_file.write_text(
        "\n".join('"' + str(value).replace("\\", "/") + '"' for value in arguments),
        encoding="utf-8",
    )
    subprocess.run([str(executable), "@" + str(argument_file)], check=True, timeout=60)


def main():
    """Compile and execute current sources or a read-only Git baseline copy."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--java-home", type=Path, required=True)
    parser.add_argument("--baseline-ref")
    parser.add_argument("--privacy-negative-control", action="store_true",
                        help="Temporarily restore raw console passthrough; privacy regression must fail")
    arguments = parser.parse_args()
    repository = Path(__file__).resolve().parents[6]
    android_project = repository / "build/android"
    sdk_value = os.environ.get("ANDROID_HOME") or os.environ.get("ANDROID_SDK_ROOT")
    sdk = Path(sdk_value) if sdk_value else Path(os.environ["LOCALAPPDATA"]) / "Android/Sdk"
    android_jar = sdk / "platforms/android-35/android.jar"
    gradle = Path(os.environ.get("GRADLE_USER_HOME", Path.home() / ".gradle"))
    dependencies = sorted((gradle / "caches/8.9/transforms").rglob("classes.jar"))
    dependencies += sorted((gradle / "caches/modules-2/files-2.1").rglob("*.jar"))
    app_classes = android_project / "app/build/intermediates/javac/debug/compileDebugJavaWithJavac/classes"
    if not android_jar.is_file() or not dependencies or not app_classes.is_dir():
        parser.error("Requires Android 35 SDK, cached dependencies and an existing scoped debug Java compile")
    classpath = os.pathsep.join(map(str, [android_jar, app_classes, *dependencies]))
    java_binary = arguments.java_home / "bin" / ("java.exe" if os.name == "nt" else "java")
    javac_binary = arguments.java_home / "bin" / ("javac.exe" if os.name == "nt" else "javac")
    with tempfile.TemporaryDirectory(prefix="yousage-bridge-regression-") as temporary:
        temporary_path = Path(temporary)
        production_sources = []
        for filename in ("WailsBridge.java", "WailsJSBridge.java", "WailsPathHandler.java", "DiagnosticsStore.java", "DiagnosticsUi.java"):
            relative = Path("build/android/app/src/main/java/com/wails/app") / filename
            source = (
                subprocess.check_output(
                    ["git", "show", f"{arguments.baseline_ref}:{relative.as_posix()}"],
                    cwd=repository,
                ).decode("utf-8")
                if arguments.baseline_ref and filename not in ("DiagnosticsStore.java", "DiagnosticsUi.java")
                else (repository / relative).read_text(encoding="utf-8")
            )
            if filename == "WailsBridge.java":
                loader = 'System.loadLibrary("wails");'
                if source.count(loader) != 1:
                    raise ValueError("Expected exactly one native loader to disable in the disposable host copy")
                source = source.replace(loader, "")
            if arguments.privacy_negative_control and filename == "WailsJSBridge.java":
                console = "public void log(String level, String message) {\n    }"
                if source.count(console) != 1:
                    raise ValueError("Expected one no-op legacy console method in the disposable host copy")
                source = source.replace(console, "public void log(String level, String message) {\n        Log.e(TAG, message);\n    }")
            destination = temporary_path / "production/com/wails/app" / filename
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_text(source, encoding="utf-8")
            production_sources.append(destination)
        production_output = temporary_path / "production-classes"
        run_java(
            ["--release", "11", "-proc:none", "-classpath", classpath, "-d", production_output, *production_sources],
            javac_binary,
            temporary_path,
        )
        fake_sources = []
        for filename, source in STUBS.items():
            destination = temporary_path / "fakes" / filename
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_text(source, encoding="utf-8")
            fake_sources.append(destination)
        test_output = temporary_path / "test-classes"
        compiled_classpath = os.pathsep.join([str(production_output), classpath])
        run_java(
            ["--release", "11", "-proc:none", "-classpath", compiled_classpath, "-d", test_output,
             *fake_sources, Path(__file__).with_name("BridgeLifetimeRegression.java")],
            javac_binary,
            temporary_path,
        )
        run_java(
            ["-classpath", os.pathsep.join([str(test_output), compiled_classpath]),
             "com.wails.app.BridgeLifetimeRegression"],
            java_binary,
            temporary_path,
        )


if __name__ == "__main__":
    main()
