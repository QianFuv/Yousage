# 原生主题

## 调色板生成

生成器根据画布色和强调色推导全部 61 个颜色角色，六组金标准用例（包括灰度、透明度和 OKLCH 种子）
在所有角色上逐字比较。生成器按画布的实际亮度而不是外观槽位推导；种子色的透明度被有意忽略，
而可移植格式中直接覆盖的角色保留透明度。CSS 颜色字面量通过内嵌的 culori 解析（运行在 goja 中）。
相关代码与数据为 MIT 许可，见根目录的许可证说明和 `CULORI-LICENSE.txt`。

`builtins.json` 是从参考源码导出的内置主题数据（来源与摘要记录在 `source.json`），
包含五组成对主题和有序的 61 角色列表。“标准”主题即不加主题的默认界面。

## 导入与发布格式

严格的可移植 v1 导入和宽松的环境发布是两种不同的格式：

- 可移植导入拒绝未知角色、重复的基础外观变体和无效颜色。
- 发布文件接受未来版本的角色、以文件名作为身份、简写种子和稀疏调色板；解析时丢弃未知或无效值并补齐默认值。

## 主题库

主题库保存自定义主题、合集和亮色 / 暗色选择，按修订号原子保存。发布目录中默认主题的每一代
只被采用一次，之后用户的手动选择优先；选择了主远程环境时，由该环境发布的主题生效。
目录变化通过文件监听感知，并在 100 毫秒稳定后发出完整快照；发布的主题消失时回退到默认主题。

## 发布与命令行

发布写入 Yousage 自己的目录，使用跨进程的系统文件锁、先暂存并同步再替换（Windows 上使用替换操作）。
发布前先读取设置，设置格式错误或无法读取时不产生任何主题副作用。如果提交设置失败，原文件会被恢复，
除非已被更新的写入者替换。未知的设置键会保留。`clear` 只移除默认值 / 代数字段，保留文件和客户端选择。

```powershell
.\bin\yousage.exe theme set ocean --state-dir "$env:TEMP\yousage-theme-example"
.\bin\yousage.exe theme set .\night.json --id night --state-dir "$env:TEMP\yousage-theme-example"
.\bin\yousage.exe theme show --state-dir "$env:TEMP\yousage-theme-example"
.\bin\yousage.exe theme clear --state-dir "$env:TEMP\yousage-theme-example"
```

默认目录是系统用户配置目录下的 `Yousage`。命令行分派在创建 Wails 应用之前执行，
不会启动 GUI 或 Node 进程，也不会写入其他应用的状态。

```powershell
go test ./internal/theme/... -count=1
pnpm --dir tools/parity test:theme
go run ./tools/spikes/theme --app bin/yousage.exe
```

最后一条命令在一次性的隔离目录中运行生产可执行文件的命令行主题流程。
