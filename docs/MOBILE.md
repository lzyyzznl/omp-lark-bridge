# 手机网关（OMP Mobile）

手机是这套部署的**第二把钥匙**：节点网页要不要放行、配对和密钥能不能改，都由手机上的一次确认决定。
Android App 源码在 [`omp-android/`](../omp-android)，服务端实现在 [`core/mobile.go`](../core/mobile.go)，
节点 relay 的拦截在 [`core/hub_tunnel.go`](../core/hub_tunnel.go)。

## 1. 它做什么

| 场景 | 没有手机网关 | 开启后（已登记 ≥1 台手机） |
|---|---|---|
| 浏览器打开节点网页（`https://omp-xxxx.<suffix>/`） | 凭 token / 登录码直接进 | 先显示「等待手机确认」，手机批准后拿到一次性链接才放行 |
| 浏览器打开中心控制台（`https://bridge.<domain>/`） | 凭 management token 直接进 | 同样先手机批准；批准后 cookie 只管控制台，不放行任何节点 |
| 配对节点、改节点登录码、改备注、移除节点 | 只要 management token | 必须带**手机签名**；只有 token 的写请求返回 403 |
| 手机上管理节点 | — | App 内「节点」页直接打开节点 OMP Web（一次性授权链接） |

关键安全性质：**手机一台都没登记时，两道闸都是关闭的**（否则会把自己锁在门外）。
所以「打开开关」本身不会造成锁死；第一个 `enroll` 成功后闸门才生效。

## 2. 服务端配置

`config.toml`：

```toml
[mobile]
enabled = true            # 默认 false
# state_file = ""         # 默认 <data_dir>/mobile-devices.json
require_console = true    # 节点网页需要手机批准
require_for_keys = true   # 配对/密钥写操作需要手机签名
```

改完重启服务：`omp-lark-bridge daemon restart`。`/api/v1/mobile/status` 用 management token
访问即可确认状态：

```bash
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:31990/api/v1/mobile/status
# {"ok":true,"data":{"enabled":true,"devices":0,"pending":0,
#   "require_console":true,"require_for_keys":true}}
```

## 3. 手机登记（一次性）

1. 编译并安装 APK（见第 7 节）。
2. 「设置」页填三样：中心地址（不带结尾斜杠，如 `https://hub.example.com`）、
   management token、设备名。
3. 点「登记手机」→ 调 `POST /api/v1/mobile/enroll`，服务端返回 `device_id` + `secret`。
4. App 用 Android `EncryptedSharedPreferences` 保存 `device_id/secret`，随后清空 token。

此后**每个请求**都带三个头，`secret` 永不出现在请求里：

```
X-OMP-Mobile-Device: mob_xxxxxxxx
X-OMP-Mobile-Ts: 1762500000
X-OMP-Mobile-Sig: base64url(HMAC-SHA256(secret,
                     METHOD + "\n" + requestURI + "\n" + ts + "\n" + sha256hex(body)))
```

时间戳允许 ±5 分钟偏差；签名覆盖方法与完整 URI（含 query）以及 body 的 SHA-256，
所以抓到的请求既不能改路径也不能改 body 重放（重放窗口只有 5 分钟）。

> 已经有手机登记后再加第二台手机：必须从**已登记手机**发起（带签名），
> 或者在本机 loopback 上操作。否则 `/enroll` 返回 403。

## 4. 日常使用

- **节点**：列表显示在线状态、会话数、最后活跃时间。点「打开」→ 生成一次性授权链接
  （`POST /api/v1/mobile/access/grant`，10 分钟有效）→ App 内 WebView 打开，节点网页免确认。
- **控制台**：节点页右上角「Console」→ 同一接口传 `{"device":"console"}`，链接取自 App
  访问中心时用的 origin（`https://bridge.<domain>` 或 LAN `http://<ip>:31990`），
  WebView 里直接进中心管理界面，跳过浏览器等待页。
- **审批**：节点网页在等待时，中心通过 WebSocket（`/api/v1/mobile/stream`，签名鉴权）
  推送 `approval_requested`；前台服务负责保持连接并发通知。在 App 里批准后，
  页面自动跳转（浏览器侧轮询 `/api/v1/mobile/approvals/status?nonce=...`）。
  每条审批 3 分钟过期，过期需在浏览器刷新重来。
- **密钥**：节点卡片 →「密钥」：配对、改登录码、改备注、移除节点、吊销手机。
  这些就是 `require_for_keys` 保护的操作。
- **设置**：服务地址、设备名、吊销本机（`devices/<id>/revoke`）、清空 token。

## 5. 服务端接口一览

| 方法 | 路径 | 鉴权 | 作用 |
|---|---|---|---|
| GET | `/api/v1/mobile/status` | token 或签名 | 开关、已登记数、待审批数 |
| POST | `/api/v1/mobile/enroll` | token（首台）或签名 | 登记手机，返回 ID + secret |
| GET | `/api/v1/mobile/devices` | token 或签名 | 已登记手机列表 |
| POST | `/api/v1/mobile/devices/<id>/revoke` | 签名 | 吊销手机，连带清掉其访问 cookie |
| GET | `/api/v1/mobile/approvals?status=pending` | 签名 | 待处理/历史审批 |
| POST | `/api/v1/mobile/approvals/<id>` | 签名 | `{"approve":true/false}`，批准返回一次性链接 |
| POST | `/api/v1/mobile/access/grant` | 签名 | `{"device":"<token>"}` → 10 分钟一次性 URL |
| GET | `/api/v1/mobile/stream` | 签名 | WebSocket 事件流（hello / approval_requested / …） |
| GET | `/__omp/grant?t=<token>` | 链接本身 | **节点域名下**，设 `omp_node_access` cookie（12h）后 303 到 `/` |
| GET | `/api/v1/mobile/approvals/status?nonce=` | 无 | **节点域名下**，等待页轮询结果 |

闸门只挡浏览器：`/api/v1/mobile/*`（App 签名）以及 `/api/v1/hub/tunnel`、
`/api/v1/hub/pair/claim`（节点自带 `X-Hub-Device` + secret）直接放行；其余控制台
路径都要手机批准。

`/__omp/grant` 与审批轮询都由 relay 在节点域名下本地处理，不转发给节点进程；
中心控制台域名下同样由控制台进程本地处理这两条路径（scope 为 `console`），
所以控制台的等待页也能自动跳转。

### 重新对外开放控制台域名

手机闸生效后，中心域名可以重新挂回公网——浏览器拿不到手机的批准，就只看到等待页。
cloudflared ingress 示例：

```yaml
ingress:
  - hostname: bridge.lizeyu.win        # 中心控制台
    service: http://127.0.0.1:31990
  - hostname: omp-center.lizeyu.win    # 同上，旧域名保留
    service: http://127.0.0.1:31990
  - hostname: "*.lizeyu.win"           # 各节点 relay（同样有手机闸）
    service: http://127.0.0.1:31991
  - service: http_status:404
```

没登记手机时闸门是关的，所以**先登记手机、再下发域名**；顺序反了等于把控制台裸奔在公网。

## 6. 安全模型与边界

- 信任根仍是 management token：能拿到中心 token 的人可以登记第一台手机。所以 token 要保管好；
  第一个手机登记之后，新增手机需要签名。
- 节点网页的放行是 **cookie**（`omp_node_access`，HttpOnly，HTTPS 下 Secure，12 小时），
  不是 token；cookie 只对单个节点有效（绑定 node token）。控制台的放行是同名 cookie，
  绑定伪节点 `console`：批准控制台不会放行任何节点，批准某个节点也不会放行控制台。
- 控制台闸只拦浏览器流量；App 的 `/api/v1/mobile/*` 走签名，不受闸门影响
  （否则手机就无法列出/批准请求）。
- 授权链接一次性、10 分钟过期；审批本身 3 分钟过期。轮询页只在内存里，重启服务即清空。
- 手机丢失：用另一台手机（或 loopback）吊销它；全部吊销后闸门自动关闭，恢复成「只有 token」模式。
- App 的 secret 存在 `EncryptedSharedPreferences`（Android Keystore 包裹），
  但设备已 root 时不再可信。

## 7. 编译 APK

```bash
cd omp-android
# 需要 JDK 17
./gradlew assembleDebug      # 产物：app/build/outputs/apk/debug/app-debug.apk
```

首次构建会下载 AGP/Compose 依赖。装到手机后无需 Google 服务；通知通过前台服务本地发出，
不依赖 FCM。

## 8. 故障排查

| 现象 | 原因 / 处理 |
|---|---|
| 写操作 403 `phone approval required` | 已登记手机，但请求没有签名。App 内操作；loopback 也只认签名。实在拿不到手机时才吊销全部手机，退回只认 token 的模式 |
| 节点网页一直「等待手机确认」 | 手机没在线/没批准。打开 App 审批页；确认中心 `/api/v1/mobile/status` 的 `pending` 在增长 |
| 链接「已使用」/「已过期」 | 一次性链接只能开一次，10 分钟过期；回 App 重新「打开」 |
| 签名总是失败 | 手机与服务端时钟相差超过 5 分钟；或地址填成了带结尾斜杠的 URL 导致 URI 不一致 |
| 节点网页 502 | 节点隧道不在线（节点服务停了/没配对），与手机网关无关 |
| 502 且已批准 | 批准成功但节点离线；`/hub/nodes` 看该节点在线状态 |

## 9. 测试

```bash
go test ./core/ -run Mobile -v
```

覆盖：登记与签名（含时钟偏差与 body 篡改）、密钥闸 403、网页闸→审批→放行、
控制台闸（token-only 401、批准后 200、scope 互不影响）、一次性链接只能打开一次、
吊销后 cookie 失效。

真机联调可用端到端脚本（在中心服务器上运行：登记临时设备 → 走完整审批 → 吊销）。
它假设还没有登记过手机、且已配对至少一个节点；路径/端口可用 `OMP_CONFIG`、
`OMP_HUB_STATE`、`OMP_API_PORT`、`OMP_RELAY_PORT` 覆盖：

```bash
python3 scripts/e2e_mobile.py
```
