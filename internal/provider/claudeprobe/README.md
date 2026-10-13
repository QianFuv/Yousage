# Claude 额度控制探测

本包只发送 `initialize` 和 `get_usage` 两个控制请求，不提交提示，也不打包 Claude SDK、CLI 或 Node。
Windows 上需要显式的原生 `.exe`，拒绝 shell 包装脚本。

## 超时与回退

初始化（包括启动进程）限时 25 秒，额度请求另外限时 4 秒。只有收到关联的“未知 `skip_behaviors`
字段”错误时，才在同一额度时限内去掉该字段回退一次；未知的 `get_usage`、认证错误和超时都不重试。
额度请求失败时保留成功的初始化结果，额度不可用时标记为不支持。`utilization` 本身就是 0–100 的百分比。

## 进程参数与隔离

参数和环境变量对应 SDK 的无提示传输方式：严格的空 MCP 配置、禁用 hooks 和 IDE 自动连接、不持久化会话。
受管理的 CLI 策略仍可能覆盖这些设置，因此不能保证所有托管 hooks 都被禁用。账户目录通过
`CLAUDE_CONFIG_DIR` 指定。

Windows 使用 `PROC_THREAD_ATTRIBUTE_JOB_LIST` 在创建进程时原子地放入独立的 kill-on-close Job Object，
并限制可继承的 stdio 句柄，不存在先启动再加入的竞态，也不会回退到不受管理的进程。成功、错误、取消和超时时
都会结束 Job、回收根进程并关闭、排空管道。它覆盖普通 CreateProcess 产生的子孙进程，但不控制已存在的外部代理或服务。
POSIX 上使用独立的进程组。

JSONL 单帧上限 1 MiB，stdout 总量上限 16 MiB；无效 UTF-8 或输出超限都会使探测失败。非 JSON 横幅、
无关的控制 ID 和保活消息不能满足请求。stderr 会被持续排空但不保留。

## 测试

```powershell
go test ./internal/provider/claudeprobe/... -count=1 -timeout=45s
go vet ./internal/provider/claudeprobe/...
```

测试替身位于 `tools/spikes/claude`。测试覆盖分片 / 噪声输出、stderr 背压、关联、能力与错误状态、回退、
独立超时、取消、账户隔离、提前退出、超大帧以及子进程和孙进程，验证的是传输和生命周期行为，不查询任何真实账户。
