# Android viewer

Run from the repository root:

```sh
wails3 task android:debug
wails3 task android:package
```

The debug task builds for an x86_64 emulator. The package task builds an arm64
release APK. Both generate mobile bindings before running
`pnpm --dir frontend build:android`, then embed `frontend/dist-android` into Go.
Neither task starts an emulator or installs the app.

Outputs are `artifacts/releases/yousage.apk` and an additional
`artifacts/releases/yousage-<debug|release>-<amd64|arm64>.apk`. Set `ANDROID_ARCH=arm64` or
`ANDROID_ARCH=amd64` to override a task's default architecture.

The build helper supports Windows, Linux, and macOS. It resolves the SDK from
`ANDROID_HOME`, then `ANDROID_SDK_ROOT`, then the host's standard Android SDK
location. The NDK comes from `ANDROID_NDK_HOME`, `ANDROID_NDK_ROOT`, or `NDK_HOME`,
otherwise the newest installed side-by-side NDK. Windows uses the NDK's `.cmd`
compiler wrappers. Java uses `JAVA_HOME` when set; otherwise the helper searches
for JDK 21 or 17 using known environment variables, PATH, sibling JDKs, and
standard install directories. Set `JAVA_HOME` if automatic discovery cannot find
the intended JDK. The pinned Gradle 8.9 distribution supports this project's
Android Gradle Plugin 8.7.3 and JDK 21.

Release APKs use the debug keystore unless `ANDROID_KEYSTORE_FILE`,
`ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, and `ANDROID_KEY_PASSWORD` are
provided; the GitHub release workflow refuses to publish without them. The app
version comes from `build/config.yml`, and the version code is derived from it as
`major * 10000 + minor * 100 + patch`. The application ID is
`app.yousage.mobile`; the JNI namespace remains `com.wails.app`.

For an existing frontend build, the individual phases are:

```sh
go run ./build/android/scripts -mode release -arch arm64 -compile-only
go run ./build/android/scripts -mode release -arch arm64 -assemble-only
go run ./build/android/scripts -mode debug -arch amd64 -compile-only
go run ./build/android/scripts -mode debug -arch amd64 -assemble-only
```

Pairing credentials are stored in `pairing.json` under
`application.Android.StoragePath()`, which calls Java `Activity.getFilesDir()`.
The file is atomically replaced with mode 0600; Android backup is disabled.
Only pairing state and the relay origin cross the Go binding boundary.

The Activity loads `https://wails.localhost/`, served from `frontend/dist-android/index.html`. External links should
call the bound `Service.OpenURL`, which accepts HTTPS and dispatches an Android
`ACTION_VIEW` intent. External HTTPS navigations are also intercepted by the
Activity and sent to the browser; other external schemes are blocked. Prefer the
bound method over `window.open`, whose popup behavior depends on the WebView.

The Activity applies native system-bar and display-cutout insets to the WebView's
container and consumes them before they reach the WebView. Frontend layouts do
not need to query native inset sizes or add system-bar padding. Insets and
keyboard behavior still need device/emulator validation. The installed 3.0.0
JavaScript Android namespace exposes haptics, device info, and toast methods;
it does not expose the newer Go `SafeAreaJSON` helper.
