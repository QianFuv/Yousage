# 开发指南

## 环境要求

| 工具 | 版本 |
| --- | --- |
| Go | 1.26.2 |
| Node | 24.15.0 |
| pnpm | 10.32.0 |
| Wails CLI | 3.0.0-beta.28 |

前端依赖使用冻结的 lockfile 安装，Go 依赖由 `go.sum` 锁定。前端的 Vite 构建工具安装在
`tools/parity` 中，因此两个目录都需要安装依赖（构建任务会自动执行）。

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.28
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

便携版 ZIP 包含 `yousage.exe`、`README.md`、`THIRD_PARTY_NOTICES.md`、
`dependencies.json` 和 `SHA256SUMS.txt`，ZIP 自身的摘要写入 `bin/SHA256SUMS.txt`。

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

- `go test` 使用合成的 Codex / Claude / Grok / OpenCode / Cursor / Antigravity 数据
  覆盖解析、去重、定价、时间窗口、额度合并和损耗、速率、小票等报表，不会读取真实账户，
  也不会消耗重置券。
- `tools/spikes/claude` 是 Claude 控制协议的测试替身，`tools/spikes/theme`
  在隔离目录中运行生产可执行文件的命令行主题流程。
- 浏览器中的尺寸测试只能验证 CSS 布局，不能代替原生 DPI、文件对话框、标题栏颜色和进程退出等检查。

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
