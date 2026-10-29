# 手机查看与分享中继

Relay 是可自行部署的单用户 Go 服务，为手机只读看板和临时小票分享页保存加密数据，
并提供对应的网页。它不转发实时请求，不查询 Provider，不提供账户系统，也不能从手机修改桌面设置。
未启用同步时，桌面不会自动连接中继。

## 数据与安全

桌面先把 JSON 用 gzip 压缩，再用随机 32 字节密钥进行 AES-256-GCM 加密。
上传的 envelope 为 `0x01 || 12 字节随机 nonce || 密文及认证标签`，
附加认证数据为 `space:<id>` 或 `share:<id>`，将密文绑定到对应位置。
中继只保存密文，无法解密快照或分享；它仍能看到请求元数据，包括 ID、大小、时间戳和 IP，
反向代理或隧道服务也可能记录这些信息。

手机链接为 `https://relay.example.com/v/<id>#<key>`，
分享链接为 `https://relay.example.com/s/<id>#<key>`。
ID 是 16 个随机字节的无填充 base64url 编码（22 字符）；密钥是 32 个随机字节的同类编码。
浏览器不把 `#` 后的片段发送给服务器，网页在浏览器中解密。
持有完整链接的人可以阅读对应内容，因此手机链接应保密，分享链接只发给预期读者。
网页和解密脚本由中继提供，仍需信任部署主机及其提供的网页代码。

桌面把中继写入令牌和手机快照密钥保存在系统凭据库；分享记录（含完整链接）保存在桌面本地偏好数据库。
写入令牌可替换或删除中继中的数据，不能代替链接密钥解密。服务端仅在内存中保存令牌的 SHA-256 摘要并做常量时间比较。
请使用 HTTPS：普通 HTTP 会让 Bearer 令牌以明文经过网络，密文加密无法保护这个令牌。

## Docker 部署

以下 shell 命令在仓库根目录执行，适用于 Bash / Git Bash。先生成强随机令牌：

```bash
export YOUSAGE_RELAY_TOKEN="$(openssl rand -base64 32)"
docker build -f build/relay/Dockerfile -t yousage-relay:local .
docker run -d --name yousage-relay \
  --restart unless-stopped \
  -e YOUSAGE_RELAY_TOKEN \
  -p 127.0.0.1:8080:8080 \
  --mount type=volume,source=yousage-relay-data,target=/data \
  yousage-relay:local
curl -fsS http://127.0.0.1:8080/healthz
```

健康检查返回 `{"status":"ok"}`。令牌应保存在自己的安全凭据存储中，供桌面和后续重启使用，
不要提交到仓库。上面的环境变量仅在当前 shell 中有效。
镜像按 Node 24.15.0 / pnpm 10.32.0 构建网页，再用 Go 1.26.10 静态编译；
最终使用 distroless nonroot 镜像，以 `65532:65532` 运行，不包含 shell。
`/data` 可由该用户写入；绑定宿主目录代替命名卷时，也必须为此用户提供写权限。

也可使用 Compose（与上面的单容器方式二选一，避免占用同一端口）：

```bash
export YOUSAGE_RELAY_TOKEN="$(openssl rand -base64 32)"
docker compose -f build/relay/compose.yaml config --quiet
docker compose -f build/relay/compose.yaml up -d --build
docker compose -f build/relay/compose.yaml logs relay
```

Compose 要求提供 `YOUSAGE_RELAY_TOKEN`，没有默认密钥。可通过当前 shell 传入，
也可在仓库之外创建受保护的环境文件，内容格式为 `YOUSAGE_RELAY_TOKEN=<你的强随机令牌>`，
然后使用 `docker compose --env-file /path/to/relay.env -f build/relay/compose.yaml up -d --build`。
不要把环境文件或 `docker compose config` 展开的令牌输出公开。
示例默认只绑定宿主机 `127.0.0.1:8080`，手机需通过下述 HTTPS 入口访问。

## HTTPS 入口

例如，在 Docker 宿主机上运行 cloudflared，将 HTTPS 隧道转发到本地端口：

```bash
cloudflared tunnel --url http://127.0.0.1:8080
```

把命令输出的 HTTPS 地址填入桌面设置。此方式适合临时验证，地址可能随重启变化；
长期部署应配置有固定域名的命名隧道，将入口服务设为 `http://127.0.0.1:8080`，
或用 Caddy / nginx 等反向代理终止 TLS，再转发到此端口。
cloudflared 在宿主机运行，因此 Compose 不包含隧道服务。
不要把公开入口降为 HTTP，也不要只给首页加登录拦截而阻断桌面的 Bearer API 请求。

## 桌面设置与手机查看

1. 打开 **设置 → 同步与分享**，填写中继的 HTTPS 地址和相同的令牌。
2. 点击 **测试连接**，确认中继可达且接受令牌，再保存并开启 **同步到手机**。
3. 等待首次上传；必要时点击 **立即同步**。用手机扫描“手机”区域的二维码，打开只读看板。
4. 手机可查看用量、额度、损耗和速率，并切换 24 小时 / 7 天 / 30 天 / 90 天窗口。
   手机的刷新只重新读取中继上的最新快照，不会要求桌面或 Provider 立即刷新。
   看板在屏幕上时每 30 秒检查一次中继（内容未变时只返回 304），切回前台时立即检查；
   新快照到达后，主题、各页面报表和桌面改动过的显示偏好（语言、时间格式等）随之更新；
   桌面没有改动的偏好保留手机上的临时选择。
   安卓 App 在私有存储中保留最近一次读取的快照（仍是密文），启动时先显示它并提示正在刷新；
   刷新失败时继续显示缓存及其时间。更换或断开链接会删除缓存。

桌面成功刷新用量或额度、保存主题或显示偏好，以及发布的主题变化后触发上传，采用 10 秒防抖和至少 60 秒的上传间隔，内容未变则跳过。
当 **同步到手机** 和 **自动刷新** 都开启，且窗口隐藏或最小化到托盘时，
桌面每 15 分钟尝试刷新用量与额度，再触发上传，仍遵守已有的数据过期与冷却规则。
关闭同步会停止自动推送，但不会删除已经上传的快照或分享。

## 小票分享、撤回与换链接

在用量页打开当前时间段的小票，点击 **分享链接**，选择有效期（1 / 7 / 14 / 30 天，默认 14 天），
按需开启 **隐藏金额**，检查预览后创建链接。小票二维码随后使用该分享链接，也可直接复制完整链接。
未创建分享链接时，小票二维码仍使用原有的文本摘要。

分享是创建时的固定内容，包含本地环境的小票、趋势和模型占比，不会随之后的同步更新。
它不包含私有来源路径、账户或会话身份；隐藏金额会在加密前移除金额字段，模型占比改按 Token 计算，
而不是仅用样式遮住数字。
在 **设置 → 同步与分享 → 分享链接** 中可复制或 **撤回**；撤回成功后中继删除对应密文，后续读取返回 `404`。

手机链接泄露时，在“手机”区域点击 **重置链接**，生成新的 ID 和密钥，并尝试删除旧快照。
旧快照删除是尽力执行；如果当时网络或中继不可用，旧密文可能仍在，需要在中继恢复后确认删除或清空数据。
重置手机链接不会撤回独立的小票分享。撤回、过期和删除均不能收回读者已经下载或截图的内容。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `YOUSAGE_RELAY_TOKEN` | 无，必填 | 至少 24 个字符；推荐 `openssl rand -base64 32` 生成。用于 `Authorization: Bearer <token>` |
| `YOUSAGE_RELAY_ADDR` | `:8080` | Go HTTP 监听地址；容器内默认监听 8080 |
| `YOUSAGE_RELAY_DATA` | `./data`；镜像内为 `/data` | SQLite 数据目录，包含 `relay.db` 及运行中的 WAL 文件 |

更换令牌需要更新部署环境并重建容器，再更新桌面保存的令牌；不需要重新构建镜像。
更换令牌不会更换手机密钥，也不会自动使已有查看链接失效。

## 备份与重置

持久化内容都在 `/data` 卷中。更新镜像或普通 `docker compose down` 不删除命名卷。
使用固定的 Compose 项目名和目录，避免误启动一个新的空卷。

备份时先停止写入，再复制完整数据目录，避免只复制 `relay.db` 而漏掉 WAL：

```bash
docker compose -f build/relay/compose.yaml stop relay
docker compose -f build/relay/compose.yaml cp relay:/data ./relay-data-backup
docker compose -f build/relay/compose.yaml start relay
```

恢复时先停止服务，用卷备份工具将完整备份还原到同一个卷，确认目录及文件归属为 `65532:65532`，
再启动。备份只有密文，不包含桌面系统凭据库中的密钥或写入令牌；丢失密钥无法从中继恢复明文。
恢复旧备份可能重新带回已撤回但尚未到期的密文。

需要彻底重置中继时，`docker compose -f build/relay/compose.yaml down --volumes`
会删除此 Compose 项目的命名数据卷及全部快照和分享，操作不可恢复；再次 `up -d` 创建空卷。
桌面随后可点击“立即同步”上传快照；旧分享需要重新创建。单容器示例使用的 `yousage-relay-data` 卷需单独管理。

## API 参考

所有 `{id}` 必须符合 `^[A-Za-z0-9_-]{22}$`。标为“需要”的路由要求
`Authorization: Bearer <token>`；无效或缺少令牌返回 `401`，并带 `WWW-Authenticate: Bearer`。
读取密文无需令牌，解密需要完整链接中的密钥。

| 方法 | 路径 | 认证 | 成功与业务状态 |
| --- | --- | --- | --- |
| GET | `/healthz` | 无 | `200`，JSON `{"status":"ok"}` |
| GET | `/api/auth` | 需要 | `204`，验证写入令牌 |
| PUT | `/api/spaces/{id}` | 需要 | `204`，创建或替换快照，请求体为 envelope 字节 |
| GET | `/api/spaces/{id}` | 无 | `200`，密文与 `ETag`、`Last-Modified`；`If-None-Match` 匹配时 `304`；不存在时 `404` |
| DELETE | `/api/spaces/{id}` | 需要 | `204`，不存在也返回 `204` |
| POST | `/api/shares/reserve?ttlDays=N` | 需要 | `201`，JSON `{id, expiresAt}`；N 必须是 1 / 7 / 14 / 30，否则 `400`；没有 API 默认值 |
| PUT | `/api/shares/{id}` | 需要 | `204`，填充预留的分享；不存在或已过期 `404`；已填充 `409` |
| GET | `/api/shares/{id}` | 无 | `200`，密文；不存在、未填充或已过期均为 `404` |
| DELETE | `/api/shares/{id}` | 需要 | `204`，不存在也返回 `204` |
| GET | `/v/{id}` | 无 | `200`，手机查看 HTML；数据是否存在由后续 API 请求确定 |
| GET | `/s/{id}` | 无 | `200`，分享 HTML；数据是否存在由后续 API 请求确定 |
| GET | `/assets/*` | 无 | `200`，网页静态资源；不存在时 `404`；支持条件请求及范围请求 |

带 ID 的路由在 ID 格式错误时返回 `400`。PUT 请求体使用 `application/octet-stream`，
空内容或首字节不是 `0x01` 返回 `400`，读取请求体失败返回 `400`，超过 **4 MiB** 返回 `413`。
中继只检查 envelope 的首字节和大小，不验证解密结果。
存储操作失败或生成分享 ID 失败返回 `500`；未注册的路径返回 `404`，不支持的方法通常返回 `405`。
Go 的 GET 路由也接受 HEAD；静态资源由标准文件响应处理，可能返回 `304`、`206` 或 `416`。

分享须先预留 ID，再用 `share:<id>` 作为附加认证数据加密，然后 PUT 一次；没有单步 `POST /api/shares`。
有效期从预留时开始，过期后立即拒绝读取，并每小时清理一次过期行（含空预留）。
私有快照没有自动过期时间。
所有 `/api` 响应带 `Cache-Control: no-store`；HTML 设置严格 CSP、`Referrer-Policy: no-referrer`
和 `X-Content-Type-Options: nosniff`，带哈希的静态资源缓存一年。

## 当前限制

- 手机只读取桌面最近上传的快照，没有实时转发，也不能触发桌面刷新或使用重置券。桌面退出后不会继续上传。
- 只支持一个拥有者的写入令牌，没有多用户权限、总存储配额或内置请求限速；部署者需管理公网入口和卷容量。
- 最近上传时间只保存在桌面当前进程中，重启后可能显示“尚未上传”，不表示中继中的快照消失。
- 网页依赖浏览器的 WebCrypto 和 gzip 解压能力，公网访问需要 HTTPS。版本不匹配时应一起更新桌面与中继。
- 直接编译 Go 而没有先构建网页时，查看地址会返回占位页；Docker 构建会先生成并内嵌真实网页。
