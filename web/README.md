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
    ├── api.js             # management API 封装（Bearer token）
    ├── styles.css         # 粗野主义令牌 + 全局样式
    ├── lib/time.js        # cron 解析 / 可读化 / 时间格式化
    ├── hooks/useCron.js   # 列表拉取 + 15s 轮询（后台降频 60s）
    └── components/        # CronTable / JobDialog / ConfirmDialog / Settings / Toast / Badge
```

## 快速开始（开发）

1. **开启 management API**：在 `config.toml` 加
   ```toml
   [management]
   enabled = true
   port = 31990            # 默认 9820
   token = "<你自己的随机串>"   # 前端请求要用，别用默认值
   cors_origins = ["http://localhost:5173"]
   ```
   重启 omp-lark-bridge。

2. 安装依赖并启动：
   ```bash
   cd web
   npm install
   npm run dev
   ```

3. 打开 http://localhost:5173，点右上角「设置」填入 `token`。

dev 模式下 `/api` 由 Vite 代理转发到 management 端口（`vite.config.js`）。

## 生产部署

```bash
npm run build   # 产出 dist/
```

把 `dist/` 交给任意静态服务器，并配一层反向代理把 `/api/v1`
转发到 management 端口，例如 nginx：

```nginx
location /api/ {
    proxy_pass http://127.0.0.1:31990;
}
location / {
    root /path/to/dist;
    try_files $uri /index.html;
}
```

前端请求走相对 `/api/v1`（同源），无需 CORS；token 仍需在页面右上角
「设置」里填一次（存 `localStorage`）。也可 `VITE_API_BASE` 指向其他地址。

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
