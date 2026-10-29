# 参考对照测试

`tools/parity` 存放固定版本的上游参考源码以及基于它的对照测试。这些源码同时被前端直接打包
（共享界面组件、图表、调色板与主题编辑器），因此它既是测试基线，也是构建输入。

## 源码固定

- 参考版本记录在 `tools/parity/sources.json` 的 `commit` 字段中，每个文件都记录了
  Git blob ID、SHA-256 和字节数。
- `pnpm --dir tools/parity sync` 只从固定版本的原始 URL 下载声明的文件，先校验 Git blob，
  通过后才写入 `tools/parity/upstream/`。
- `pnpm --dir tools/parity test` 会先运行 `verify:sources` 校验所有哈希，再运行对照测试。
- 上游文件保持原样不修改；Git 换行转换对它们禁用。上游的 Effect 补丁中有一行必需的单空格
  上下文行，因此只有补丁文件关闭了行尾空白检查。

## 对照测试

```powershell
pnpm --dir tools/parity install --frozen-lockfile
pnpm --dir tools/parity test         # 校验源码哈希并运行对照测试
pnpm --dir tools/parity reference    # 在 127.0.0.1:4174 打开参考工作台
```

- 定价、聚合、转录解析、额度合并与汇总、格式化以及调色板生成都与原始实现对照，
  另有手算用例覆盖推理 Token 包含关系、上报价格与自定义价格、缓存回退、三个时区与夏令时、
  别名、来源归属、会话去重和稀疏的等权账户汇总。
- Pi 原生读取器保持固定版本原文，`src/pi.test.tsx` 与 Go 的 Pi 测试共享
  `fixtures/pi-usage.json` 中手算的预期值，覆盖实际响应模型、工具和摘要费用、互斥缓存计数、
  推理子集、缺失价格与分叉复制的稳定身份。
- Codex 原生读取器同样保持原文，`src/codex.test.tsx` 与 Go 的 Codex 测试共享
  `fixtures/codex-usage.json` 中的 27 组手算用例，覆盖累计值核对、陈旧快照、压缩归零、
  分叉复制、各类计数不变量与格式错误计数。读取器引用的 Codex 主目录发现和服务档位历史
  只用于真实服务端，由 `src/bridges/codexServer.tsx` 占位。
- `fixtures/theme-golden.json` 记录六套完整的 61 角色调色板（两种外观、三组种子色，
  包括灰度、透明度和 OKLCH）。正常测试只做比较；确需更新时运行
  `UPDATE_REFERENCE=1 pnpm --dir tools/parity test:theme`。Go 侧的
  `internal/theme` 测试使用同一组金标准。
- 契约桥接（`fixtures/bridges`）只为已经有效的测试数据提供常量和类型标记，不验证 Effect
  模式校验或远程互通。

## 参考工作台与截图

参考工作台用固定的合成数据渲染原始图表、占比条、主题预览、菜单、对话框和主题编辑器，
用于在相同输入、主题、视口、缩放和参考时间下比较几何、颜色和焦点行为。它不是完整应用的截图，
也不连接任何凭据、历史文件、远程环境或重置券。

截图保存在 `docs/parity/screenshots/`（PNG 不纳入版本控制），其文件摘要记录在
`screenshots/manifest.json`。截图只能证明视觉外观，不能证明焦点行为或原生对话框。

## 工具链说明

- 脚本使用 `.mjs`，因为它们需要在安装 TypeScript 依赖之前运行；Vite 配置保留工具约定的 `.ts`。
- Vite 使用上游别名 `@voidzero-dev/vite-plus-core@1.0.0`（内含 Vite 8），该包不提供
  `vite` 可执行文件，脚本直接调用其 CLI 路径，避免误用全局安装的其他版本。
- 别名会产生 peer 版本警告，以实际测试和构建结果为准。
- 上游许可证保留在 `tools/parity/upstream/LICENSE` 和根目录的 `THIRD_PARTY_NOTICES.md` 中。
