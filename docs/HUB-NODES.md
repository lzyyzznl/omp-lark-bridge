# Hub 多节点：中心 + 节点安装指南

本文是 `[hub]` 的完整安装/接入手册，覆盖：

- 中心（Ubuntu/Linux）配置
- 节点（Linux / Windows）安装与配对
- 中兴 `zte-linux` / `zte-book` 等设备的注意事项
- 动态添加、临时下线、彻底移除
- 404 / 502 / 403 / 配对过期的排查

架构、安全模型和当前部署的详细说明见 [README 的 Hub 段](../README.md)。

## 0. 先理解三个地址

| 地址 | 指向 | 用途 |
|---|---|---|
| `bridge.example.com` | 中心 `127.0.0.1:31990` | 管理控制台、配对、节点列表 |
| `omp-center.example.com` | 中心 `127.0.0.1:30177` | 中心自己的 OMP Web |
| `omp-<token>.example.com` | 中心 relay `127.0.0.1:31991` | 每个节点的 OMP Web |

节点只主动出站 `443` 连接中心，不需要公网端口，也不需要为每个节点单独建 Cloudflare Tunnel。

## 1. 中心准备

### 1.1 安装 OLB 与 OMP Web

按 [INSTALL.md](../INSTALL.md) 完成单机安装，确认：

```bash
omp-lark-bridge --version
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:31990/   # 200
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:30177/   # 200
```

### 1.2 中心配置

```toml
[management]
enabled = true
host = "127.0.0.1"        # cloudflared 在本机，控制台不要直接暴露公网
port = 31990
token = "<强管理 token>"

[hub]
enabled = true
mode = "server"
secret = "<openssl rand -hex 32 的输出>"
relay_port = 31991
state_file = "/home/you/.omp-lark-bridge/hub-devices.json"
node_host_suffix = "example.com"   # 留空则从 [ompweb] public_url 推导

[ompweb]
enabled = true
port = 30177
public_url = "https://omp-center.example.com"
```

说明：

- `secret` 是中心默认的 hub 密钥，也是旧配对记录的兼容密钥；新节点配对时会生成独立密钥，
  可在节点页修改。不要提交到仓库。
- `node_host_suffix` 决定节点 host；例如 `pc5` → `omp-pc5.example.com`。
- `state_file` 保存已配对设备、每个节点独立的 hub 密钥和登录码哈希，创建权限 `0600`；
  文件包含敏感凭据，必须备份并限制访问，不要提交到仓库。
- 中心自己也是普通 OLB；它的 OMP Web 由 cloudflared 直接指向 `30177`，不走 relay。

重启中心：

```bash
omp-lark-bridge daemon restart
omp-lark-bridge daemon status
```

确认 relay 在监听：

```bash
ss -ltnp | grep 31991
```

### 1.3 Cloudflare DNS 与 ingress

节点使用泛解析，不需要为每个新节点建 DNS 记录或 ingress。

DNS：

```bash
# 中心 tunnel 已经存在时；tunnel 名称/ID 换成你的
cloudflared tunnel route dns <tunnel> bridge.example.com
cloudflared tunnel route dns <tunnel> omp-center.example.com
cloudflared tunnel route dns <tunnel> '*.example.com'
```

`/etc/cloudflared/config.yml`：

```yaml
tunnel: <tunnel-id>
credentials-file: /etc/cloudflared/<tunnel-id>.json

ingress:
  - hostname: bridge.example.com
    service: http://127.0.0.1:31990
  - hostname: omp-center.example.com
    service: http://127.0.0.1:30177
  # 节点统一走 relay；relay 再按 Host 找已配对设备
  - hostname: "*.example.com"
    service: http://127.0.0.1:31991
  - service: http_status:404
```

校验并重启：

```bash
cloudflared tunnel ingress validate
sudo systemctl restart cloudflared
```

注意：

- 精确 host（`bridge`、`omp-center`）必须排在泛解析前面。
- 泛解析 `*.example.com` 很宽；其他子域名如果没有精确记录，也会进 relay 并返回 404。
- Cloudflare Universal SSL 的通配证书覆盖一级 `*.example.com`；`omp-pc5.example.com` 属于一级，正常可用。
- 如果已有旧的 `omp-pc1..4` 精确记录，它们会优先于泛解析；只要仍指向同一个 tunnel 就没问题，后续可以清理。

## 2. 添加第一个节点

### 2.1 在中心生成配对命令

1. 浏览器打开 `https://bridge.example.com/`，填入管理 token。
2. 进入「节点」→「添加节点」。
3. 填写：
   - `device_token`：例如 `pc5`，只允许 `[A-Za-z0-9_-]`，全中心唯一
   - `显示名称`：例如 `zte-linux`
   - `公网主机`：留空，自动生成 `omp-pc5.example.com`
4. 复制页面给出的命令。

配对码：

- 8 位
- 10 分钟有效
- 一次性；claim 后立即失效
- 不要发到公开群，拿到码的人在有效期内可以完成配对

> 必须从公网控制台生成命令。如果从 `http://127.0.0.1:31990` 生成，命令里的
> `server_url` 会是 `127.0.0.1`，远程节点无法使用。

### 2.2 在节点执行

命令形如：

```bash
omp-lark-bridge hub pair https://bridge.example.com <CODE> \
  --name zte-linux \
  --state-file /home/you/.omp-lark-bridge/hub-client.json
```

成功后：

- 写入 `hub-client.json`（创建权限 `0600`），包含 `server_url`、`secret`、`device_token`、`web_url`
- 终端打印 `[hub]` / `[ompweb]` 配置片段
- 控制台节点开始显示；隧道建立后为在线

如果提示 `invalid or expired pairing code`：回控制台重新生成一个新的码。不要重复使用旧码。

### 2.3 免改配置贴码配对（可选）

新版 client 模式允许**未配对启动**：`server_url` 和 `state_file` 配好，
`secret` / `device_token` 都留空。启动后节点只开放配对入口，不会建立同步或隧道；
配对成功后新节点会立即生效，不需要改配置或重启。

```toml
[hub]
enabled = true
mode = "client"
server_url = "https://bridge.example.com"
state_file = "/home/you/.omp-lark-bridge/hub-client.json"
# secret / device_token 留空，等待贴码配对
```

中心「添加节点」生成码后，在**节点本机**调用节点自己的管理 API：

```bash
curl -sS -X POST \
  -H "Authorization: Bearer <节点管理 token>" \
  -H "Content-Type: application/json" \
  -d '{"code":"<CODE>","name":"zte-linux"}' \
  http://127.0.0.1:31990/api/v1/hub/pair/join
```

- 这是**节点**的管理 API，不是中心；token 用节点的 `[management] token`。
- 未配对节点：返回 `applied: true`，立即启动同步循环和反向隧道；状态文件落盘为 `0600`。
- 已配对节点：返回 `restart_required: true`；新凭据已落盘，但运行中的隧道仍使用旧 secret，
  重启节点后生效。
- 仍然可以使用 `hub pair` CLI；两种方式二选一。
- 确认是否已配对：`GET /api/v1/status` 的 `hub.paired` 字段。

### 2.4 节点配置

把命令输出的片段合并进节点 `config.toml`：

```toml
[hub]
enabled = true
mode = "client"
state_file = "/home/you/.omp-lark-bridge/hub-client.json"

[ompweb]
enabled = true
port = 30177
public_url = "https://omp-pc5.example.com"
```

`server_url`、`secret`、`device_token` 默认从 `hub-client.json` 读取；如果 `config.toml` 里也显式写了，显式值优先。

建议显式写 `[ompweb] public_url`。它决定看板和 OMP Web 链接的浏览器地址；不写时 OLB 会尝试用配对返回的 `web_url` 自动填充，但独立部署的 OMP Web/服务环境不一定继承该环境变量。

然后重启节点：

```bash
omp-lark-bridge daemon restart
omp-lark-bridge daemon status
```

### 2.5 客户端只读，中心可编辑

节点页在**中心**和**客户端**都会显示全部已配对节点，并可直接打开对应 OMP Web；
区别在权限：

| 能力 | 中心（server） | 客户端（client） |
|---|---|---|
| 查看全部节点 | ✅ | ✅ |
| 打开节点 OMP Web | ✅ | ✅ |
| 添加/移除节点 | ✅ | ❌ |
| 修改名称/密钥/登录码 | ✅ | ❌ |

客户端调用 `PATCH /api/v1/hub/nodes/{token}`、`POST /hub/pair/new`、
`DELETE /hub/pair/{token}` 都会收到 `409 hub is not in server mode`。

中心节点行点「编辑」可改：

- **名称**：中心记录为准，节点心跳不会覆盖。
- **密钥**：每个节点独立的 tunnel/heartbeat 密钥。修改后中心关闭旧隧道；节点在下一次
  `/hub/register` 同步时拿到新密钥并落盘，随后重连。旧密钥在过渡期仍可用，避免节点失联。
- **登录码**：浏览器经中心访问该节点时使用的 HTTP Basic 密码。中心只保存哈希，不能读回
  原码；留空保持，勾选「清除登录码」可关闭。**它不是节点 OMP Web 自身的登录密码**；
  OMP Web 的 `OMP_WEB_PASSWORD_HASH` 仍由节点本地管理，两者可以不同。

对应 API：

```text
PATCH /api/v1/hub/nodes/{device_token}
Authorization: Bearer <中心 management token>
Content-Type: application/json

{"name":"zte-linux","secret":"<new-secret>","login_code":"<code>"}
```

`login_code` 非空即设置；`clear_login_code: true` 清除。`secret` 至少 16 字符；
不传的字段保持不变。

## 3. Linux 节点安装（含中兴 `zte-linux`）

中兴设备没有专用安装包；按实际操作系统走 Linux 或 Windows 步骤即可。`zte-linux` 这类 Linux 节点按本节操作。

### 3.1 放置二进制与配置

```bash
sudo install -m 0755 omp-lark-bridge-linux-amd64 /usr/local/bin/omp-lark-bridge

sudo mkdir -p /opt/omp-node
sudo cp config.example.toml /opt/omp-node/config.toml
sudo chown -R "$USER":"$USER" /opt/omp-node

cd /opt/omp-node
omp-lark-bridge --version
```

节点需要能出站访问：

- `https://bridge.example.com:443`（WSS 隧道）
- 自己的 Feishu / LLM / 模型 API

**不需要**开放任何入站端口。

### 3.2 配对

```bash
omp-lark-bridge hub pair https://bridge.example.com <CODE> \
  --name zte-linux \
  --state-file /home/you/.omp-lark-bridge/hub-client.json
```

把输出合并进 `/opt/omp-node/config.toml`，再安装/重启服务：

```bash
cd /opt/omp-node
omp-lark-bridge daemon install --work-dir /opt/omp-node
omp-lark-bridge daemon status
```

如果服务是用户级 systemd（`systemctl --user`），确保开机后仍能运行：

```bash
loginctl enable-linger "$USER"
```

### 3.3 节点 OMP Web

安装流程会确保 `ompweb` 存在（Node.js >= 24），并由 OLB 服务编排启动。确认：

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:30177/   # 200
```

安全要求：

- 节点 OMP Web 会通过 `https://omp-pc5.example.com/` 暴露给公网。
- relay **只校验 host 是否属于已配对节点，不校验访问者**。
- 必须给 OMP Web 设置访问密码（`OMP_WEB_PASSWORD_HASH`，按 ompweb 版本文档配置），或用 Cloudflare Access 保护该 host。
- 不要把 OMP Web 直接监听 `0.0.0.0`；保持 `127.0.0.1:30177`，由隧道/本机代理访问。

### 3.4 验证

在中心或任意能访问 Cloudflare 的机器：

```bash
curl -I https://omp-pc5.example.com/
```

预期：

- 已配对且节点在线：`200`
- 已配对但节点离线：`502 node offline`
- host 未配对：`404 unknown node`

控制台「节点」页应显示该节点为在线；agent/session 数量来自节点每约 60 秒一次的 inventory 上报，可能比“在线”状态晚几秒到一分钟。

节点本机也可以直接看 hub 状态：

```bash
curl -sS -H "Authorization: Bearer <节点管理 token>" \
  http://127.0.0.1:31990/api/v1/status | jq '.data.hub'
```

未配对时应看到 `"paired": false`；配对成功后为 `true`，并带 `server_url` / `device_token` / `web_url`。

## 4. Windows 节点安装（含 `zte-book` 等）

如果 `zte-book` 是 Windows，按本节操作；如果它实际运行 Linux，按第 3 节。

### 4.1 准备

1. 安装 Node.js >= 24（OMP Web 依赖）。
2. 把 `omp-lark-bridge-windows-amd64.exe` 放到固定目录，例如 `C:\omp-node\`。
3. 准备配置目录：`%USERPROFILE%\.omp-lark-bridge\`。

PowerShell：

```powershell
New-Item -ItemType Directory -Force "$env:USERPROFILE\.omp-lark-bridge"
Set-Location C:\omp-node
.\omp-lark-bridge-windows-amd64.exe --version
```

### 4.2 配对

```powershell
.\omp-lark-bridge-windows-amd64.exe hub pair https://bridge.example.com <CODE> `
  --name zte-book `
  --state-file "$env:USERPROFILE\.omp-lark-bridge\hub-client.json"
```

把输出的 `[hub]` / `[ompweb]` 合并进 `C:\omp-node\config.toml`，然后注册自启：

```powershell
.\omp-lark-bridge-windows-amd64.exe daemon install --work-dir C:\omp-node
.\omp-lark-bridge-windows-amd64.exe daemon status
```

Windows 下没有 systemd；`daemon install` 使用计划任务（schtasks，注册到当前登录用户）。日志默认在：

```text
%USERPROFILE%\.omp-lark-bridge\logs\
```

OMP Web 由 OLB 负责拉起/监督；确认：

```powershell
Invoke-WebRequest http://127.0.0.1:30177/ -UseBasicParsing | Select-Object StatusCode
```

节点只需要出站 443；Windows 防火墙不需要为隧道开放入站规则。

## 5. 临时下线与彻底移除

### 临时下线（保留配对）

在节点上停服务：

```bash
omp-lark-bridge daemon stop
```

控制台会显示该节点离线；重新启动后隧道自动重连，不需要重新配对。

### 彻底移除

控制台「节点」页点「移除」，或调用管理 API：

```text
DELETE /api/v1/hub/pair/<device_token>
Authorization: Bearer <管理 token>
```

效果：

- 删除配对记录和 host 映射
- 关闭现有隧道
- `https://omp-<token>.example.com/` 变为 `404 unknown node`
- 节点本机 OMP Web、Agent、cron、Feishu 继续运行
- 节点的下一次隧道连接收到 `403 device is not paired`
- 节点可能继续 `/hub/register`，但不会重新出现在「节点」页

如果不再需要这个节点，建议在节点上同时停服务或关闭 `[hub]`，避免无意义的重连日志。

再次需要时，重新「添加节点」并配对即可；同一个 `device_token` 可以复用。

## 6. 排障

| 现象 | 原因 | 处理 |
|---|---|---|
| `404 unknown node` | host 没配对 / `device_token` 不一致 / DNS 未走泛解析 | 控制台核对 host；确认配对记录；检查 `*.example.com` DNS 和 ingress |
| `502 node offline` | 节点服务/隧道没连上 | 节点看 `hub: tunnel` 日志；确认出站 443、`server_url`、`hub-client.json` |
| `403 device is not paired` | 节点已被移除，或中心 `hub-devices.json` 丢失 | 重新生成配对码并 `hub pair` |
| `401 invalid hub secret` | 节点 secret 与中心不一致，或中心改密钥后节点还没同步 | 等节点下一次 `/hub/register` 自动采用新密钥；仍失败则重新配对；不要手改 secret |
| 打开节点出现 `401 login required` | 中心给该节点设了登录码 | 在浏览器 Basic 认证框把登录码填作密码；或在中心「编辑」里清除登录码 |
| 客户端没有「添加/编辑/移除」 | 客户端设计为只读 | 正常行为；去中心控制台修改 |
| 中心改密钥后节点离线 | 节点版本过旧/未配置 state_file，无法接收新密钥 | 升级节点二进制；保留 `hub-client.json` 并重启，等待同步 |
| 浏览器打开还是 `127.0.0.1` | 节点 `[ompweb] public_url` 缺失/未重启 | 写入 `https://omp-pc5.example.com`，重启 OLB |
| `hub: load client state` 解析失败 | `hub-client.json` 损坏/权限不对 | 重新配对；保持文件 JSON 合法、权限 `0600` |
| 控制台/节点 host 偶发超时 | Cloudflare 边缘链路问题 | 先在中心本机验证 relay：`curl -i -H 'Host: omp-pc5.example.com' http://127.0.0.1:31991/`；本机 200 说明源站正常 |
| 端口冲突 | `31990`/`30177`/`31991` 被占用 | 改 `[management]`/`[ompweb]`/`[hub] relay_port`，同步 cloudflared ingress |
| Windows 上 OMP Web 没起来 | Node.js 版本/ompweb 安装失败 | `node -v` >= 24；`daemon status` 和日志；重跑 `daemon install` |

### 安全清单

- [ ] 管理 token 不是默认值，且未提交到仓库
- [ ] `bridge` 和 `omp-center` 之外的 host 没有绕过 Cloudflare 直接暴露
- [ ] 中心 `31990` 尽量只监听 `127.0.0.1`，由 cloudflared 转发
- [ ] 每个节点 OMP Web 都有密码或 Cloudflare Access
- [ ] 中心 `hub-devices.json` 权限 `0600`；它现在含每节点密钥和登录码哈希，按敏感文件处理
- [ ] `hub-client.json` 权限 `0600`，没有发群/入库
- [ ] 登录码是中心 relay 的 HTTP Basic 密码，不等同于 OMP Web 自身密码；两者都要管理
- [ ] 配对码只在管理控制台和节点命令中出现，用完即弃
- [ ] 定期备份中心 `hub-devices.json`；丢失后所有节点需要重新配对
- [ ] 移除节点后，确认节点侧不再需要该配对；必要时停服务或关闭 `[hub]`

## 7. 和中兴设备的关系

`zte-linux` / `zte-book` 等设备不需要特殊中心配置：

- 中心只认 `device_token`、`name`、`host`。
- 节点按操作系统走 Linux 或 Windows 安装步骤。
- 节点只需要出站 443；如果设备在 NAT/防火墙后，不需要端口映射。
- 当前如果 SSH 22 不可达，需要在中兴设备本地（或通过其远程桌面/带外管理）执行 `hub pair` 和重启服务；中心侧无法代替节点本地安装。

## 8. 相关文件

- 中心配对状态：`~/.omp-lark-bridge/hub-devices.json`
- 节点配对状态：`~/.omp-lark-bridge/hub-client.json`
- 中心配置：`config.toml`
- Cloudflare 配置：`/etc/cloudflared/config.yml`
- 代码入口：
  - `core/hub_tunnel.go`：配对、隧道、relay
  - `core/hub.go`：节点列表、inventory
  - `core/management.go`：管理 API 路由
  - `cmd/omp-lark-bridge/hub_cli.go`：`hub pair` 命令
  - `web/src/components/NodesPage.jsx`：节点页
