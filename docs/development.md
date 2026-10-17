# 开发指南

## 环境要求

| 工具 | 版本 |
| --- | --- |
| Go | 1.26.2 |
| Node | 24.15.0 |
| pnpm | 10.32.0 |
| Wails CLI | 3.0.0-beta.30 |

前端依赖使用冻结的 lockfile 安装，Go 依赖由 `go.sum` 锁定。前端的 Vite 构建工具安装在
`tools/parity` 中，因此两个目录都需要安装依赖（构建任务会自动执行）。

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.30
wails3 doctor
```

## 开发与构建

```powershell
wails3 task dev                                   # Windows 开发模式，前端端口 4175
wails3 task windows:build ARCH=amd64 -f           # 生成 bin/yousage.exe
powershell -NoProfile -File build/package-windows.ps1  # 生成便携版 ZIP 和 SHA256SUMS.txt
```

构建任务依次执行：安装前端依赖、生成第三方许可证（`build/dependencies.json` 与
`THIRD_PARTY_NOTICES.md`，包含嵌套的原生运行时许可证）、生成 TypeScript 绑定、构建前端，
最后以 `production` 标签编译 GUI 子系统的可执行文件。构建期的 Node 工具不会被打包。

便携版 ZIP 的根目录只包含 `yousage.exe` 和 `data/`，文档、许可证、依赖清单和文件校验和放在
`data/package/`，不会打包本机数据库或缓存。ZIP 和 ZIP 自身的 `SHA256SUMS.txt` 输出到
`artifacts/releases/`；依赖分析临时文件放在 `.task/build/`，使 `bin/` 只保留程序和数据。

GitHub Release 从上一个已发布且在当前历史中的稳定版本起列出全部提交，按 Conventional Commit
类型分为 Features、Bug fixes、CI、Documentation、Maintenance 等类别，保留每条提交的链接及
完整版本对比链接。首次稳定发布列出全部历史；草稿、预发布及其他分支的版本不作为基线。

### 中继与网页

中继独立于桌面程序，网页资源由 `build:web` 输出到 `internal/relay/web/dist`，再由 Go 内嵌。
在仓库根目录执行（令牌占位值需替换为自己的强随机令牌，不要提交到仓库）：

```powershell
pnpm --dir tools/parity install --frozen-lockfile
pnpm --dir frontend install --frozen-lockfile
pnpm --dir frontend build:web
$env:YOUSAGE_RELAY_TOKEN = '<替换为至少 24 字符的强随机令牌>'
$env:YOUSAGE_RELAY_ADDR = '127.0.0.1:8080'
$env:YOUSAGE_RELAY_DATA = './data'
go run ./cmd/yousage-relay
```

另开终端可运行 `curl.exe -fsS http://127.0.0.1:8080/healthz`。
没有先构建网页时，Go 仍可编译，但查看地址仅返回占位页；网页变化后需要重新构建中继。

```powershell
go test ./internal/relay/... ./cmd/yousage-relay/...
go vet ./internal/relay/... ./cmd/yousage-relay/...
task relay:docker
```

`relay:docker` 在仓库根目录构建 `yousage-relay:local`，不需要本机 Wails 或原生 GUI 开发库。
若通过 Wails 提供的 Task 运行器执行，使用 `wails3 task relay:docker`。
Docker / Compose 部署、HTTPS 入口与桌面配对见 [中继指南](relay.md)。

### 其他平台

- macOS（需要 Xcode 命令行工具，macOS 12 及以上）：
  `wails3 task darwin:package ARCH=arm64 -f` 或 `ARCH=amd64`。应用包使用 ad-hoc 签名，
  分发签名与公证需另行处理。
- Linux amd64（需要 C 编译器、`pkg-config`、GTK 4、WebKitGTK 6.0 和 X11 开发头文件）：
  `wails3 task linux:build ARCH=amd64 -f`。

目前只在 Windows 上做过原生运行验证，macOS / Linux 的构建任务已提供但尚未在原生环境中验证。

## 测试与检查

```powershell
go test -p 1 ./...
go vet ./...
gofmt -l .
pnpm --dir frontend typecheck
pnpm --dir frontend lint
pnpm --dir frontend format:check
pnpm --dir frontend test
pnpm --dir tools/parity test                  # 参考实现对照测试，见 docs/parity/README.md
go run ./tools/spikes/theme --app bin/yousage.exe  # 用真实可执行文件验证命令行主题
```

- `go test` 使用合成的 Codex / Claude 数据
  覆盖解析、去重、定价、时间窗口、额度合并和损耗、速率、小票等报表，不会读取真实账户，
  也不会消耗重置券。
- `tools/spikes/claude` 是 Claude 控制协议的测试替身，`tools/spikes/theme`
  在隔离目录中运行生产可执行文件的命令行主题流程。
- 浏览器中的尺寸测试只能验证 CSS 布局，不能代替原生 DPI、文件对话框、标题栏颜色和进程退出等检查。

## 版本与发布

版本号以 `build/config.yml` 的 `info.version` 为准。Windows 版本资源、macOS `Info.plist`、
`internal/sync/snapshot.go` 和 `build/dependencies.json` 中的副本由脚本统一修改：

```powershell
node build/version.mjs              # 查看当前版本
node build/version.mjs set 0.2.0    # 修改所有副本
node build/version.mjs check        # 检查各副本是否一致（CI 也会检查）
```

Android 的 versionCode 由版本号换算（主版本 × 10000 + 次版本 × 100 + 修订号），
因此次版本号和修订号都必须小于 100。

平时推送到 main 只运行 CI 检查，不构建安装包和镜像。CI 通过后，Release 工作流检查
`v<版本号>` 标签是否已存在；不存在时（即提升版本号后的第一次推送）同时构建并发布：

- `yousage-<版本>-windows-amd64.zip`：Windows 便携版
- `yousage-<版本>-android-arm64.apk`：Android 安装包
- `ghcr.io/qianfuv/yousage-relay:<版本>` 与 `:latest`：中继镜像

发布页同时附带 `SHA256SUMS.txt`。某个构建失败时不会发布任何内容；修复后再次推送，
或在 Actions 中手动运行 Release 工作流即可重试。

Android 安装包必须用固定的发布密钥签名，换了密钥的新版本无法覆盖安装旧版本。在仓库的
Actions Secrets 中配置 `ANDROID_KEYSTORE_BASE64`（keystore 文件的 Base64）、
`ANDROID_KEYSTORE_PASSWORD`、`ANDROID_KEY_ALIAS` 和 `ANDROID_KEY_PASSWORD`；
缺少任一项时发布会失败，不会退回调试密钥。

## 发布前手动检查

自动化测试无法覆盖原生窗口交互。发布前请用最终的可执行文件检查：

1. 不启动开发服务器直接运行：侧边栏、图标与标题正确，首帧可读。
2. 亮色 / 暗色 / 跟随系统；选择跟随系统时切换 Windows 外观；重启后主题保持。
3. 原生主题文件选择与取消、拖入主题 JSON、诊断报告保存对话框的选择与取消。
4. 复制数值和脱敏报告并粘贴到其他程序，内容正确。
5. 外观快捷键、Tab / Shift+Tab、对话框关闭后的焦点、Alt+Tab。
6. 滚动各页面；不同窗口大小、舒适 / 紧凑、字体、100%–200% 界面缩放，
   以及 Windows 显示缩放 100% / 125% / 150% / 200%。
7. 最大化、最小化、恢复，重启后的窗口位置，窗口居中；可能时测试拔出显示器和混合 DPI。
8. 留下数据来源 / 价格 / 主题草稿后关闭：取消保留草稿，放弃后关闭；正在写入的操作不会丢失。
9. 关闭到托盘、托盘菜单导航与退出；再次启动时激活已有窗口。
10. 正常退出后，应用进程及其 WebView 子进程全部结束。

不要为了测试界面而消耗真实的重置券。
