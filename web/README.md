# omp-lark-bridge · 定时任务看板

面向 omp-lark-bridge 的 **cron 管理看板**（React + Vite，粗野主义风格），
通过 omp-lark-bridge 自带的 HTTP Management API（`/api/v1/cron`）读写定时任务。

```
web/
├── index.html
├── vite.config.js         # dev 代理：/api → management 端口
├── package.json
└── src/
    ├── main.jsx           # 入口
    ├── App.jsx            # 布局 / 统计 / 路由状态
    ├── api.js             # management API 封装（token 存 cookie，30 天自动续期）
    ├── styles.css         # 粗野主义令牌 + 全局样式
    ├── lib/time.js        # cron 解析 / 可读化 / 时间格式化
    ├── hooks/useCron.js   # 列表拉取 + 15s 轮询（后台降频 60s）
    └── components/        # CronTable / JobDialog / ConfirmDialog / Settings / Toast / Badge
```

## 登录与 token

- 打开看板 **必须先填 token**（右上角「设置」或首次进入的门禁页）。
- token 写入 cookie（`ompLarkToken`，30 天自动续期）：每次成功请求后
  重新计 30 天窗口，过期后需重新输入。
- 默认 token 为 `1`；修改：编辑 `config.toml` 的
  `[management].token` 后重启 omp-lark-bridge。

## 使用（推荐：嵌入二进制）

前端已用 `//go:embed` 嵌入 omp-lark-bridge 二进制（`web/embed.go`）。
启动 omp-lark-bridge 后直接访问：

```
http://127.0.0.1:31990/
```

无需另起静态服务器或 Vite 进程；构建：

```bash
cd web && npm run build     # 产出 web/dist/
cd .. && make build         # 先 web-dist 再 go build
```

## 开发模式（Vite dev server）

```bash
cd web
npm install
npm run dev                 # http://localhost:5173，/api 代理到 31990
```

仅开发迭代需要；生产请用嵌入二进制。dev 下 token 同样 30 天 cookie。

## 功能

- **概览**：全部 / 正常启用 / 失败中（`fail_streak`）/ 静音 计数
- **任务表格**：状态徽章（启用·停用·静音·失败×n）、描述、cron 表达式 +
  中文可读 + 下次运行倒计时（`cron-parser` 前端解析）、上次运行、静音标记
- **操作**：立即运行、启用/停用、静音/取消、编辑、删除（确认）
- **新建 / 编辑**：描述、5 段 cron（实时校验 + 下次运行预览）、
  Agent 提示词 / Shell 命令 二选一、会话锚点、会话模式、静音
- **自动刷新**：15s 轮询，切后台降频到 60s；右上角可手动刷新

## API 对齐

对接 omp-lark-bridge `core/management.go` 的 REST：
`GET/POST /api/v1/cron`、`PATCH/DELETE /api/v1/cron/{id}`、
`POST /api/v1/cron/{id}/exec`。响应统一 `{ok, data}` / `{ok:false, error}`，
认证用 `Authorization: Bearer <token>`（或 `?token=`）。

## 说明

- `config.toml`（含飞书 / management token）在 `.gitignore` 中，**不入库**。
- 样式遵守排版纪律：CJK 行高 1.3-1.4、强调色克制、动效 <300ms、
  尊重 `prefers-reduced-motion`；无 emoji 图标（粗野主义文字按钮）。
