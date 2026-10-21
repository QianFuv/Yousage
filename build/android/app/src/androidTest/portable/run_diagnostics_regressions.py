"""Compile diagnostics against the installed Android SDK and test on a host JVM.

No JUnit, JNI, WebView, app data or downloads are required. Android's API-21 file
syscalls are backed by temporary host files at runtime. Native AlertDialog layout,
Activity lifecycle and ACTION_SEND chooser behavior still require device testing.
"""

import argparse
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import xml.etree.ElementTree as element_tree


STUBS = {
    "android/system/ErrnoException.java": """
package android.system;
public final class ErrnoException extends Exception {
    public final int errno;
    public ErrnoException(String function, int errno) { super("synthetic IO failure"); this.errno = errno; }
}
""",
    "android/system/StructStat.java": """
package android.system;
public final class StructStat {
    public final int st_mode;
    public final long st_size;
    public StructStat(int mode, long size) { st_mode = mode; st_size = size; }
}
""",
    "android/system/OsConstants.java": """
package android.system;
public final class OsConstants {
    public static final int ENOENT = 2, O_RDONLY = 0, O_WRONLY = 1, O_RDWR = 2;
    public static final int O_CREAT = 64, O_NOFOLLOW = 131072, SEEK_SET = 0;
    public static boolean S_ISREG(int mode) { return mode == 1; }
    public static boolean S_ISDIR(int mode) { return mode == 2; }
}
""",
    "android/system/Os.java": """
package android.system;
import java.io.FileDescriptor;
import java.io.IOException;
import java.io.RandomAccessFile;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.NoSuchFileException;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.attribute.BasicFileAttributes;
import java.util.IdentityHashMap;
import java.util.Map;

public final class Os {
    private static final Map<FileDescriptor, RandomAccessFile> FILES = new IdentityHashMap<>();
    public static int maxWrite = Integer.MAX_VALUE;
    public static int bytesBeforeFailure = Integer.MAX_VALUE;
    public static StructStat lstat(String name) throws ErrnoException {
        try {
            BasicFileAttributes attributes = Files.readAttributes(
                    Path.of(name), BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
            return new StructStat(attributes.isRegularFile() ? 1 : attributes.isDirectory() ? 2 : 3, attributes.size());
        } catch (NoSuchFileException failure) { throw new ErrnoException("lstat", 2); }
        catch (IOException failure) { throw new ErrnoException("lstat", 5); }
    }
    public static FileDescriptor open(String name, int flags, int mode) throws ErrnoException {
        try {
            Path path = Path.of(name);
            if ((flags & OsConstants.O_NOFOLLOW) == 0 || Files.isSymbolicLink(path)) throw new IOException();
            if (!Files.exists(path) && (flags & OsConstants.O_CREAT) == 0) throw new NoSuchFileException(name);
            RandomAccessFile file = new RandomAccessFile(name, (flags & 3) == 0 ? "r" : "rw");
            FileDescriptor descriptor = file.getFD();
            FILES.put(descriptor, file);
            return descriptor;
        } catch (NoSuchFileException failure) { throw new ErrnoException("open", 2); }
        catch (IOException failure) { throw new ErrnoException("open", 5); }
    }
    public static StructStat fstat(FileDescriptor descriptor) throws ErrnoException {
        try { return new StructStat(1, FILES.get(descriptor).length()); }
        catch (IOException failure) { throw new ErrnoException("fstat", 5); }
    }
    public static int read(FileDescriptor descriptor, byte[] bytes, int offset, int count) throws ErrnoException {
        try { return FILES.get(descriptor).read(bytes, offset, count); }
        catch (IOException failure) { throw new ErrnoException("read", 5); }
    }
    public static int write(FileDescriptor descriptor, byte[] bytes, int offset, int count) throws ErrnoException {
        if (bytesBeforeFailure <= 0) throw new ErrnoException("write", 28);
        int actual = Math.min(Math.min(count, maxWrite), bytesBeforeFailure);
        try {
            FILES.get(descriptor).write(bytes, offset, actual);
            bytesBeforeFailure -= actual;
            return actual;
        } catch (IOException failure) { throw new ErrnoException("write", 5); }
    }
    public static void ftruncate(FileDescriptor descriptor, long length) throws ErrnoException {
        try { FILES.get(descriptor).setLength(length); }
        catch (IOException failure) { throw new ErrnoException("ftruncate", 5); }
    }
    public static long lseek(FileDescriptor descriptor, long offset, int whence) throws ErrnoException {
        try { FILES.get(descriptor).seek(offset); return offset; }
        catch (IOException failure) { throw new ErrnoException("lseek", 5); }
    }
    public static void fsync(FileDescriptor descriptor) throws ErrnoException {
        try { descriptor.sync(); }
        catch (IOException failure) { throw new ErrnoException("fsync", 5); }
    }
    public static void close(FileDescriptor descriptor) throws ErrnoException {
        try { FILES.remove(descriptor).close(); }
        catch (IOException failure) { throw new ErrnoException("close", 5); }
    }
    public static void rename(String source, String target) throws ErrnoException {
        try { Files.move(Path.of(source), Path.of(target), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING); }
        catch (IOException failure) { throw new ErrnoException("rename", 5); }
    }
}
""",
}


def run_java(executable, arguments, directory):
    """Run an installed Java tool with a temporary argument file and bounded timeout.

    Args:
        executable: Installed java or javac executable.
        arguments: Tool arguments without shell interpolation.
        directory: Disposable directory for the argument file.
    """
    argument_file = directory / (executable.stem + ".args")
    argument_file.write_text(
        "\n".join('"' + str(value).replace("\\", "/") + '"' for value in arguments),
        encoding="utf-8",
    )
    subprocess.run([str(executable), "@" + str(argument_file)], check=True, timeout=120)


def verify_log_privacy(production):
    """Require every logcat call in the approved native files to contain fixed text only.

    Args:
        production: Directory containing the approved Android Java sources.
    """
    for name in ("MainActivity.java", "WailsBridge.java", "WailsJSBridge.java", "WailsPathHandler.java"):
        source = (production / name).read_text(encoding="utf-8")
        for call in re.findall(r"\bLog\.\w+\([^;]*\);", source):
            if not re.fullmatch(r'Log\.[diewv]\(TAG, "(?:[^"\\]|\\.)*"\);', call):
                raise AssertionError(f"{name} contains a nonliteral logcat argument")
    print("PASS approved native logcat calls contain fixed text only")


def verify_native_layout(source_root):
    """Verify independent native controls overlay the viewer without reserving healthy height.

    Args:
        source_root: Android main source set directory containing res/layout.
    """
    root = element_tree.parse(source_root / "res/layout/activity_main.xml").getroot()
    android = "{http://schemas.android.com/apk/res/android}"
    identifier = android + "id"
    container = next((child for child in root if child.get(identifier) == "@+id/main_container"), None)
    if container is None or container.tag != "FrameLayout" or list(root) != [container]:
        raise AssertionError("Native diagnostics must not reserve sibling layout space")
    children = list(container)
    entry = next((child for child in children if child.get(identifier) == "@+id/diagnostics_open"), None)
    recovery = next((child for child in children if child.get(identifier) == "@+id/viewer_recovery"), None)
    if entry is None or recovery is None or children.index(entry) >= children.index(recovery):
        raise AssertionError("Native chip must overlay the WebView below the opaque recovery panel")
    expected = {
        "layout_width": "wrap_content", "layout_height": "wrap_content",
        "layout_gravity": "bottom|center_horizontal", "layout_marginBottom": "96dp",
        "minWidth": "48dp", "minHeight": "48dp", "elevation": "0dp",
        "stateListAnimator": "@null", "textColor": "#A3A3A3",
        "background": "?android:attr/selectableItemBackgroundBorderless",
    }
    if any(entry.get(android + key) != value for key, value in expected.items()):
        raise AssertionError("Native chip must retain approved muted, borderless, 48dp overlay presentation")
    if container.get(android + "layout_height") != "match_parent" or recovery.get(android + "background") != "@color/wails_background":
        raise AssertionError("Viewer must fill normal height and recovery must remain opaque")
    if any("WebView" in child.tag for child in root.iter()):
        raise AssertionError("Native recovery layout must inflate without constructing a WebView")
    if not any(child.get(identifier) == "@+id/viewer_diagnostics" for child in container.iter()):
        raise AssertionError("Recovery view must expose native diagnostics")
    source = (source_root / "java/com/wails/app/MainActivity.java").read_text(encoding="utf-8")
    creation = source[source.index("protected void onCreate("):source.index("private void startViewer(")]
    if creation.index("DiagnosticsStore.initialize") >= creation.index("startViewer();") or creation.count("DiagnosticsUi.show(this, diagnostics)") != 2:
        raise AssertionError("Both native diagnostics controls must exist before Go/WebView startup")
    if "viewerDiagnostics.shouldShowEntry() ? View.VISIBLE : View.GONE" not in source:
        raise AssertionError("Healthy native entry must be GONE rather than reserve invisible height")
    finished = source[source.index("public void onPageFinished("):source.index("public boolean onRenderProcessGone(")]
    if "setViewerShown" in finished:
        raise AssertionError("Page load completion must never claim accepted rendering")
    renderer = source[source.index("private void setupRendererClient("):source.index("void setViewerShown(")]
    if any(action in renderer for action in ("terminate(", "loadUrl(", "reload(", "disposeWebView(", "startViewer(")):
        raise AssertionError("Renderer responsiveness callbacks must not replace a live view")
    if "if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) setupRendererClient(webView);" not in source:
        raise AssertionError("Platform renderer callbacks require the API 29 guard")
    if renderer.count("bridge.isCurrentBinding(binding)") != 2:
        raise AssertionError("Both renderer callbacks must reject obsolete attachments")
    print("PASS native overlay reserves no healthy height and initializes independently of Go/WebView")


def main():
    """Compile unchanged production sources against Android 35 and run temporary-file regressions."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--java-home", type=Path)
    parser.add_argument("--android-jar", type=Path)
    arguments = parser.parse_args()
    executable = shutil.which("javac")
    java_home = arguments.java_home or (Path(executable).resolve().parents[1] if executable else None)
    sdk_value = os.environ.get("ANDROID_HOME") or os.environ.get("ANDROID_SDK_ROOT")
    sdk = Path(sdk_value) if sdk_value else Path(os.environ.get("LOCALAPPDATA", "")) / "Android/Sdk"
    android_jar = arguments.android_jar or sdk / "platforms/android-35/android.jar"
    if java_home is None or not android_jar.is_file():
        parser.error("Requires an installed JDK and Android SDK android.jar; no dependencies are downloaded")
    suffix = ".exe" if os.name == "nt" else ""
    javac = java_home / "bin" / ("javac" + suffix)
    java = java_home / "bin" / ("java" + suffix)
    portable = Path(__file__).resolve().parent
    production = portable.parents[1] / "main/java/com/wails/app"
    verify_log_privacy(production)
    verify_native_layout(portable.parents[1] / "main")
    with tempfile.TemporaryDirectory(prefix="yousage-diagnostics-regression-") as temporary:
        directory = Path(temporary)
        compiled = directory / "production"
        run_java(javac, ["--release", "11", "-proc:none", "-Xlint:all", "-classpath", android_jar,
                        "-d", compiled, production / "DiagnosticsStore.java", production / "DiagnosticsUi.java"], directory)
        fakes = []
        for name, source in STUBS.items():
            destination = directory / "fakes" / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_text(source, encoding="utf-8")
            fakes.append(destination)
        test_classes = directory / "tests"
        classpath = os.pathsep.join(map(str, [compiled, android_jar]))
        run_java(javac, ["--release", "11", "-proc:none", "-classpath", classpath, "-d", test_classes,
                        *fakes, portable / "DiagnosticsRegression.java"], directory)
        run_java(java, ["-classpath", os.pathsep.join(map(str, [test_classes, compiled])),
                       "com.wails.app.DiagnosticsRegression"], directory)
    print("Native AlertDialog, Activity lifecycle, and share chooser: device-only; not exercised here")


if __name__ == "__main__":
    main()
