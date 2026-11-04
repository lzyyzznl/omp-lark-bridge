# Install / 安装指南（给 agent 看的踩坑清单）

> 给**自动化安装 agent** 看的分步手册。目标是「照抄命令就能装好, 不用猜」。
> 覆盖 Linux / macOS / Windows 三种 host。每个「坑」都附**现象**和**修法**。
> 假设：你（agent）已经联网、能执行命令、目标机有 shell。以下命令在 bash 类 shell 下给出；Windows 请见 §6。

---

## 0. 三十秒决策表

| 场景 | 命令 | 说明 |
|---|---|---|
| 有 Go 工具链，想从源码跑 | `make build`（Linux/macOS）或 §4.2（Windows 手工） | 产物 `omp-lark-bridge` / `.exe` |
| 只要二进制、不想编译 | `curl -sSL https://raw.githubusercontent.com/lzyyzznl/omp-lark-bridge/main/skills/omp-lark-bridge/install.sh | sh` | 只支持 Linux/Darwin；依赖 `gh` 或 `go` |
| 已经编译好 | `./omp-lark-bridge feishu setup --project <name>` → `daemon install` | 最快上线路径，见 §5 |
| 纯配置文件跑 | 抄 `config.example.toml` → `./omp-lark-bridge --config config.toml` | 不经 daemon，前台跑 |

> **TL;DR（最快路径）**：装 Go → `make build`（或 go build）→ `feishu setup` 扫码 → `daemon install` → 完。

---

## 1. 前置条件

| 依赖 | 最低版本 | 用途 | 验证 |
|---|---|---|---|
| Go 工具链 | **go 1.25+**（`go.mod` 里 `go 1.25.0`） | 编译；也用于 `go install` 兜底 | `go version` |
| node + npm | 任意现代版（vite 5 需要） | 编译内嵌 web UI（`go:embed`） | `node -v && npm -v` |
| git | 任意 | 克隆源码 | `git --version` |
| （可选）`gh` | 任意 | `install.sh` 走 Release 下载 | `gh auth status` |

- **Go 版本别低于 1.25**：编译会直接报语法/依赖错误。Windows 装 Go 建议用官方 msi 或 `winget install GoLang.Go`。
- **npm 依赖非必需**：`web/dist` 已经被 gitignore（只提交 `.keep` 占位）。**但是** `go:embed all:dist` 要求 `web/dist/` 存在（哪怕空目录 + `.keep` 也行）；`npm install && npm run build` 才产生真实 UI。**没跑 web build 也能 go build 成功**（嵌入的是 .keep），但管理面板是空的——别以为装坏了。
- 国内网络（中国大陆网络）见 §7——**大概率必须配镜像**，否则 `go mod download` / `npm install` 超时。

---

## 2. 获取源码

```bash
git clone https://github.com/lzyyzznl/omp-lark-bridge.git
cd omp-lark-bridge
```

> 大陆网络连 GitHub 可能超时：见 §7.1。

---

## 3. 从源码构建（Linux / macOS）

```bash
cd omp-lark-bridge
make build          # 等价于:cd web && npm install && npm run build && go build -o omp-lark-bridge ./cmd/omp-lark-bridge
./omp-lark-bridge --version
```

- 产物是仓库根目录的 `omp-lark-bridge`。
- **不要**用 `go build ./...` 当产物——它只验证编译通过，不输出单个可执行文件；真正入口是 `./cmd/omp-lark-bridge`（`make build` 用的就是它）。
- 只想快速编译、不想要 web UI 依赖：先 `mkdir -p web/dist`（把 `.keep` 保住），再 `go build -o omp-lark-bridge ./cmd/omp-lark-bridge`。

### 3.1 只装二进制（不编译）

```bash
# 需要 gh 已登录；或 go ≥1.25 会走 go install
curl -sSL https://raw.githubusercontent.com/lzyyzznl/omp-lark-bridge/main/skills/omp-lark-bridge/install.sh | sh
# 验证
omp-lark-bridge --version
```

`install.sh` 逻辑：`gh release download` 预编译资产 → 失败则 `go install` → 再失败打印手动指引。注意它**只支持 Linux/Darwin**；Windows 请直接编译或用 go install。

---

## 4. Windows 从源码构建

Windows 没有 `make`，直接调 Go/npm。**先确保模块下载走代理**（§7.2），否则 `go build` 卡死。

### 4.1 一次性环境（当前 shell，推荐）

```bash
REM 在 cmd / PowerShell 里（不是 bash）
set GOPROXY=https://goproxy.cn,direct
set GOCACHE=D:\gocache
set GOTMPDIR=D:\gotmp
```

> - 大陆网络必须设 `GOPROXY`（§7.2），否则 `go mod download` 直连超时。
> - `GOCACHE` / `GOTMPDIR` 挑个**非系统盘**目录，避开 PATH 过长/权限问题；不是必须，但能少踩坑。

### 4.2 编译

```bash
# 1) web UI（可选但推荐）
cd web
npm install --registry=https://registry.npmmirror.com
npm run build
cd ..

# 2) Go 主程序（入口是 ./cmd/omp-lark-bridge！）
go build -p 1 -o omp-lark-bridge.exe ./cmd/omp-lark-bridge

# 3) 验证
.\omp-lark-bridge.exe --version
```

**Windows 专属坑：**
- **入口包必须是 `./cmd/omp-lark-bridge`**，不是仓库根。根目录只有个 `embed.go`（`package ccconnect`），`go build .` 会出 15KB 的残件，embed 的 web UI 全丢——现象是 `omp-lark-bridge.exe` 很小、`/` 管理面板 404。
- **并行编译会随机炸**：这台机器（及其它受进程创建限制/杀软的环境）`go build` / `go test` 默认并行会偶发 `error obtaining buildID` 或 `exit status 0xc0000043`（STATUS_SHARING_VIOLATION）。**修法：加 `-p 1`**。`go test` 也一样要 `-p 1`。
- **非标准 shell（MSYS/Cygwin/Git-bash）里 `./x.exe` 不可执行**：必须用绝对路径 `C:/Users/<you>/apps/omp-lark-bridge/omp-lark-bridge.exe`，否则报 `command not found: ./omp-lark-bridge.exe`。
- 想省事让 agent 少敲命令，可把下面这个裸脚本存成 `build.cmd`（放在仓库根，随项目提交都行）：

```bat
@echo off
setlocal
set GOCACHE=%LOCALAPPDATA%\gocache
set GOTMPDIR=%TEMP%\gotmp
set GOPROXY=https://goproxy.cn,direct
pushd "%~dp0web"
call npm install --registry=https://registry.npmmirror.com
call npm run build
popd
go build -p 1 -ldflags "-s -w" -o omp-lark-bridge.exe ./cmd/omp-lark-bridge
echo done %~dp0omp-lark-bridge.exe
```

> 要不要提交 `build.cmd` 你定；agent 看到「仓库根有 build.cmd」跑它就行，比解释一堆 set 快。若无 node 也可注释掉 web 两步。

---

## 5. 配置 + 上膛（Linux/macOS/Windows 通用）

### 5.1 快速接入飞书（推荐，扫码）

```bash
# 交互式：生成二维码用手机飞书扫码，自动写回 config.toml 的 app_id/app_secret
./omp-lark-bridge feishu setup --project <project-name>
```

- 二维码是本地生成的 PNG（默认 `qr.png`）+ 终端内打印；agent 帮你打开图片（Windows 用照片查看器，`start qr.png`；Linux `xdg-open qr.png`）。
- 授权 URL 形如 `https://open.feishu.cn/page/launcher?user_code=XXXX-XXXX`，扫码后在飞书开放平台确认即可。
- 之后**用生成好的 config.toml 跑**，不要再手写凭据。

### 5.2 手动配置

```bash
cp config.example.toml config.toml
# 编辑：填 name / app_id / app_secret / work_dir
./omp-lark-bridge --config config.toml
```

`config.toml` 含应用密钥，**已被 .gitignore 排除**，别 `git add` 它。

### 5.3 注册开机自启（daemon）

```bash
./omp-lark-bridge daemon install --work-dir "$(pwd)"
./omp-lark-bridge daemon status      # Running?
./omp-lark-bridge daemon restart
```

- **Linux** → systemd unit；**macOS** → launchd；**Windows** → 计划任务（schtasks，注册到当前登录用户，日志在 `%USERPROFILE%\.omp-lark-bridge\logs\`）。
- 装完 daemon 后**二进制被改名加锁**？不会；但**更新二进制前先 `daemon stop`**（Windows 计划任务不会锁文件，但为干净起见停一下）。
- 常见症状：`daemon status` 显示 Running 但 bot 无响应 → 看日志 `tail -f ~/.omp-lark-bridge/logs/omp-lark-bridge.log`，等 `bot identified` / `engine started` 出现才算真正就绪。

### 5.4 后台管理看板（默认开启）

服务启动后**默认**在 `http://localhost:31990/` 提供内置 Web 看板（定时任务、
会话、文件浏览 + `REST /api/v1/*`）。它由 `config.toml` 的 `[management]`
段控制：

```toml
[management]
enabled = true      # 默认开启；显式 false 才关闭
port = 31990        # 默认端口
token = "1"         # 进入看板要填的 token，建议改成自己的后重启
# host = "127.0.0.1"  # 不设 = 监听所有网卡；只在本地访问就设为 127.0.0.1
```

> ⚠️ 默认 `enabled=true` + `token="1"` + 全部网卡监听：若机器可达局域网，别人
> 也能看到看板登录页。**只在本机用就设 `host = "127.0.0.1"` 并改掉 token。**

验证：`curl -s -o /dev/null -w "%{http_code}" http://localhost:31990/` 应返回 200。

### 5.5 Web 工作台 ompweb（强依赖，随服务一起拉起）

ompweb（omp 的网页工作台，默认 `http://127.0.0.1:30177`）是 **硬依赖**：
装 OLB 就等于装它，且随 OLB 服务一起启动。

- `daemon install` 会先确保 `ompweb` 可用：PATH 里没有就自动
  `npm install -g @kahme247/ompweb`，失败则安装中止（不降级）。
- 它同时确保 `ompweb.service` 存在（已有则 `enable --now`；没有则调用
  `ompweb systemd install`），并在 `omp-lark-bridge.service` 里写入
  `Wants=ompweb.service` + `After=ompweb.service`，由 systemd 与 OLB 一起拉起。
- ompweb 仍作为独立服务运行（保留其 `BindPaths` 会话挂载与自动更新），
  OLB 只负责依赖编排，不再进程内另起一份。
- 前置：Node.js **>= 24**（ompweb 的 npm engines 要求）。
- 配置在 `config.toml` 的 `[ompweb]` 段（`enabled` / `port` / `bin`）；
  `enabled = false` 时不写依赖、不拉起。

验证：`curl -s -o /dev/null -w "%{http_code}" http://localhost:30177/` 应返回 200；看板右上角「ompweb ↗」可直接打开。

> 说明：unit 依赖编排目前**完整支持 systemd**（Linux）；macOS/Windows 下仍会确保 ompweb CLI 已安装，但「随服务一起拉起」需对应平台的 launchd/schtasks 编排（暂未内置）。

### 5.6 验收（跑通才算装完）


1. `./omp-lark-bridge daemon status` → Running
2. 日志出现 `platform ready` / `omp-lark-bridge is running`
3. 在飞书里 ping bot（发 `/help`）能收到卡片
4. 后台看板可开：`curl -s -o /dev/null -w "%{http_code}" http://localhost:31990/` → `200`；浏览器打开 `http://localhost:31990/` 填 token `1` 能进看板
5. ompweb 随服务拉起：`curl -s -o /dev/null -w "%{http_code}" http://localhost:30177/` → `200`；看板「ompweb ↗」可打开
6. 特权命令默认全开（本仓库已硬编码为全权限，无 admin 开关）：`/shell` `/show` `/dir` `/restart` `/upgrade` 对通过 `allow_from` 的用户可用；`allow_from` 未设 = 对所有人开放。

> ⚠️ 默认 `allow_from` 未设 = **企业内任何飞书用户都能对这台机器执行 shell**。若担心，配好之后设 `allow_from = ["<你的 open_id>"]`（用户对 bot 发 `/whoami` 拿 open_id）。

---

## 6. Windows 快速参考（上面散落点的汇总）

| 目标 | 命令（cmd/PowerShell） |
|---|---|
| 装 Go | `winget install GoLang.Go` |
| 配代理（当前会话） | `set GOPROXY=https://goproxy.cn,direct` |
| web UI | `cd web && npm install --registry=https://registry.npmmirror.com && npm run build && cd ..` |
| 编译 | `go build -p 1 -o omp-lark-bridge.exe ./cmd/omp-lark-bridge` |
| 二进制绝对路径调用 | `C:\path\to\omp-lark-bridge.exe ...`（别用 `.\` 在非标准 shell） |
| 扫码 | `omp-lark-bridge.exe feishu setup --project <name>` |
| 自启 | `omp-lark-bridge.exe daemon install --work-dir C:\path\to\repo` |
| 打开后台看板 | `start http://localhost:31990/`（填默认 token `1`） |
| 打开 ompweb 工作台 | `start http://localhost:30177/`（随服务自动拉起） |

---

## 7. 网络镜像（大陆网络必看）

GitHub / proxy.golang.org / npm 官方源在部分网络下直连超时。症状分别是：

| 症状 | 原因 | 修法 |
|---|---|---|
| `go build` 卡住不动 / `go mod download` 超时 | `proxy.golang.org` 连不上 | `GOPROXY=https://goproxy.cn,direct` |
| `git clone https://github.com/...` 超时 | github.com 443 被干扰 | 见 §7.1 |
| `npm install` 超时/几十分钟 | registry.npmjs.org 慢 | `--registry=https://registry.npmmirror.com` |
| `git push` / `git fetch` 超时 | github.com 443 被干扰，DNS 解析 IP 不通 | 见 §7.1 |

### 7.1 GitHub 直连被干扰

现象：`curl https://github.com` 超时（60s+），`git clone` 超时，但 `curl https://gitee.com` 正常 → **是网络层对 github.com 特定 IP 的干扰，不是没网**。

修法一（换可用 IP，实测有效）：
```bash
# 找出可连的 github.com IP（多试几个）：
for ip in 140.82.112.4 140.82.114.4 20.205.243.166 140.82.121.4; do
  timeout 5 curl -s -o /dev/null -w "$ip %{http_code}\n" --resolve github.com:443:$ip https://github.com
done
# 用返回 200 的 IP 做推送/拉取：
git -c http.curlopt.resolve="github.com:443:140.82.112.4" push origin main
```

修法二（一次性给当前 repo 写死，省得每次带参数）：
```bash
git config http.curlopt.resolve "github.com:443:140.82.112.4"
```

> 注意 `140.82.121.4`（常被 DNS 返回）已知不通；`112/114/243` 通常可通。IP 会变，失效就重测。

### 7.2 Go module 代理

```bash
go env -w GOPROXY=https://goproxy.cn,direct   # 全局写死（推荐）
# 或单次：GOPROXY=https://goproxy.cn,direct go build ./...
```
> goproxy.cn（七牛）/ goproxy.io / 阿里 mirrors.aliyun.com/goproxy 三选一，都能用。`direct` 后缀保证私有模块仍直连。

### 7.3 npm registry

```bash
npm config set registry https://registry.npmmirror.com   # 全局
# 或单次：npm install --registry=https://registry.npmmirror.com
```

---

## 8. 故障速查

| 现象 | 原因 | 修法 |
|---|---|---|
| `omp-lark-bridge.exe` 只有 ~15KB，管理面板 404 | 错 build 了根包 | 用 `./cmd/omp-lark-bridge` 重编（§4.2） |
| `go build`/`go test` 报 `0xc0000043` / buildID 错 | 并行编译被环境限制 | 加 `-p 1` |
| `command not found: ./omp-lark-bridge.exe` | 非标准 shell 不认 `./` | 绝对路径：`C:\...\omp-lark-bridge.exe`（§6） |
| `go mod download` / clone / install 超时 | 网络镜像缺失 | §7 |
| daemon Running 但 bot 无回复 | 服务没真正就绪 | 看日志等 `platform ready`；`daemon restart` 后再看 |
| `/` 面板或卡片交互异常 | 飞书开放平台没订阅事件 | 在开放平台订阅 `card.action.trigger`；核验 slash command 权限与版本发布 |
| `feishu setup` 扫码后没写回配置 | 没连内网/LAN 限制 | 确认能从机器访问 `open.feishu.cn`；重新 `feishu setup` |
| 飞书收不到消息但 token 对 | app 未通过审核/未发布 | 开放平台 → 版本管理与发布 |

---

## 9. Agent 检查清单（装完逐条打勾）

- [ ] `omp-lark-bridge --version` 有输出
- [ ] `go build -p 1 -o omp-lark-bridge ./cmd/omp-lark-bridge` 退出码 0
- [ ] `web/dist/index.html` 存在（说明 UI 打进去了；没有就 `npm run build`）
- [ ] `config.toml` 存在且含真实 `app_id`/`app_secret`（脚本写好，别手填）
- [ ] `daemon status` = Running
- [ ] 日志出现 `bot identified` / `platform ready` / `omp-lark-bridge is running`
- [ ] 飞书里对 bot 发 `/help` 收到卡片
- [ ] `git status` 干净（`config.toml`、`*.exe`、`web/dist`、`node_modules` 都不入库）

---

## 10. 维护心智

- 每次改 Go 源码后：`go build -p 1 ...` 重新编译，再 `daemon restart` 生效。
- 改了 `web/src/**` 要重新 `npm run build`，否则 UI 不更新（embed 的是编译时快照）。
- `daemon restart` 是热重载的好办法；`daemon status` 只管常驻，不代表 bot 就绪——看日志。
- Release 由 `make release-publish` 出（需 `gh` 认证）；日常开发不用管。
