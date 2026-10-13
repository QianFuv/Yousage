# 远程环境协议

Yousage 可以直连兼容的 T3 Code 服务端，读取其用量汇总、额度和主题，并修改其价格与别名设置。
协议是 Effect 框架的分帧 JSON RPC（不是 JSON-RPC 2.0），实现在 `internal/remote`。

## 配对与连接

1. `GET /.well-known/t3/environment`：在消耗一次性配对凭据之前，先校验环境身份和协议版本。
   当前协议版本为 2，缺省视为 1。
2. `POST /oauth/token`（form-urlencoded）：使用 token-exchange 授权，提交 `subject_token`，
   `subject_token_type=urn:t3:params:oauth:token-type:environment-bootstrap`、
   `requested_token_type=urn:ietf:params:oauth:token-type:access_token`，可选缩小 scope。
   返回的 Bearer 访问令牌保存在系统凭据库中。没有刷新令牌接口，过期或被吊销后需要重新配对。
3. 携带认证的 `POST /api/auth/websocket-ticket` 返回 `ticket` 和 `expiresAt`，然后连接
   `/ws?wsTicket=...&orchestrationProtocol=2`。访问令牌永远不会出现在 URL 中。
   协议不兼容时服务端返回 426 `orchestration_protocol_incompatible`。

远程地址必须使用 HTTPS，回环地址允许 HTTP。不跟随重定向，原始服务端错误内容不会写入诊断。

## 帧格式

- 请求：`_tag:Request, id, tag, payload, headers:[]`。
- 结束：`_tag:Exit, requestId, exit:{_tag:Success,value}` 或带标签的 Failure 原因。
- 数据块使用 `values[]`，每消费一个块都需要回复 Ack；取消使用 Interrupt。
- 每帧可以是对象或数组；处理请求关联、Defect 和 Ping/Pong。服务端每 5 秒发送一次 ping，
  上一次 ping 未得到响应时在下一次触发超时。

## 方法

| 方法 | 权限 / 行为 |
| --- | --- |
| `subscribeServerConfig {environmentThemes:true,usageLimitSources:true}` | 读取；先返回快照，之后推送 Provider / 设置 / 主题 / 额度中心变化，可选的主题和额度中心数据单独到达 |
| `server.getUsageSummary` | 读取；`sinceDay` / `untilDay` 闭区间加时区，可选小时级 `sinceTime`（含）/ `untilTime`（不含） |
| `server.refreshUsageRates`、`server.getSettings` | 读取 |
| `server.updateSettings` | 操作；按条目修补价格与别名，`null` 删除对应条目，不整体替换 |
| `server.refreshProviders` | 操作；`{}` 同时刷新所有额度中心，指定实例时不刷新额度中心 |
| `provider.consumeResetCredit` | 操作；按原生实例或来源 / 账户 / 重置券路由，只能由用户显式触发 |
| `server.probe` | 读取；仅在具备 `connectionProbe` 能力时使用，否则使用 getConfig |

“读取 / 操作”分别对应 `orchestration:read` / `orchestration:operate` 权限。
能力标记包括 `usagePriceOverrides`、`usageModelAliases`、`usageLimitSources`、
`environmentThemes`、`connectionProbe`，缺失即视为不支持。Yousage 不声明
`usageLimitsCommand`，因为它不处理聊天命令。

## 写入与结果

- 切换或重连到另一台服务端时，清除上一台留下的可选数据。
- 设置和重置写入不会自动重放：各目标分别结算，只重试失败的目标。连接断开或取消时，
  已发送的写入结果视为未知。
- 重置结果为 `reset` / `nothingToReset` / `noCredit` / `alreadyRedeemed`，成功时也可能附带警告。

## 汇总合并

- 支持用量汇总 v4–v6，与传输协议版本 2 相互独立。无效的数组条目逐个丢弃，无效的外层结构
  使本次观测失败；没有价格时保留 Token。
- 物理来源键为 `(hostId, provider, resolvedHomePath, volumeId)`。完整优先于部分，部分优先于失败；
  其次比较最新的 `readAt`，最后比较环境 ID。较新的部分结果只补充缺失的日 / 小时 / Provider / 模型单元格。
  会话数使用各来源自己的 `distinctSessions`，不对各时间桶求和；补充时取最大值。
- 额度账户键依次为 Provider + 规范化邮箱、凭据指纹、原生环境 / 实例或额度中心来源 / 账户。
  最新的 `checkedAt` 提供窗口，最新的实际重置券快照提供重置券。额度中心的重置路由优先，
  因为兑换同时会清除路由冷却。账户等权汇总，保留稀疏列。

不支持托管中继、DPoP 和 SSH 隧道。令牌过期时间等生命周期以服务端返回的字段为准。
