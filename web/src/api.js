// omp-lark-bridge management API 封装。
// 端点全部形如 {ok, data} / {ok:false, error}（见 core/management.go）。
// base：默认相对路径走 vite dev proxy（/api/v1 → 127.0.0.1:31990），
// 生产可 VITE_API_BASE 指向同源反代地址或绝对地址。
// token 存 cookie（30 天，每次成功请求自动续期），页面以此判断是否已登录。

const BASE = import.meta.env.VITE_API_BASE || '/api/v1'

const TOKEN_KEY = 'ompLarkToken'
const TOKEN_MAX_AGE_DAYS = 30

function readCookie(name) {
  const m = document.cookie.match(new RegExp('(?:^|;\\s*)' + name + '=([^;]*)'))
  return m ? decodeURIComponent(m[1]) : ''
}

function writeCookie(name, value, days) {
  const expires = days > 0 ? `; Max-Age=${days * 24 * 60 * 60}; expires=${new Date(Date.now() + days * 24 * 60 * 60 * 1000).toUTCString()}` : ''
  document.cookie = `${name}=${encodeURIComponent(value)}${expires}; Path=/; SameSite=Lax`
}

export function getToken() {
  return readCookie(TOKEN_KEY)
}
export function setToken(t) {
  writeCookie(TOKEN_KEY, t, TOKEN_MAX_AGE_DAYS)
}
export function clearToken() {
  writeCookie(TOKEN_KEY, '', -1)
}
export function hasToken() {
  return readCookie(TOKEN_KEY) !== ''
}
// 续期：把 30 天窗口从最近一次成功请求重新算起。
function renewToken() {
  const t = getToken()
  if (t) writeCookie(TOKEN_KEY, t, TOKEN_MAX_AGE_DAYS)
}

async function request(path, { method = 'GET', body } = {}) {
  const headers = { 'Content-Type': 'application/json' }
  const tk = getToken()
  if (tk) headers.Authorization = `Bearer ${tk}`

  let res
  try {
    res = await fetch(`${BASE}${path}`, { method, headers, body: body ? JSON.stringify(body) : undefined })
  } catch {
    throw new Error('无法连接 management API —— 确认 omp-lark-bridge 已开启 [management]，且地址正确（右上角「设置」）')
  }

  let data = null
  try { data = await res.json() } catch {}

  // 只有真正成功才续期。
  if (data && data.ok === true) renewToken()

  if (res.status === 401) {
    // token 缺失或错误：清掉 cookie，回到登录页重新输入。
    clearToken()
    const err = new Error('认证失败：token 缺失或错误')
    err.status = 401
    throw err
  }
  return data.data
}

export const api = {
  // GET /api/v1/cron → {jobs:[CronJob]}
  list: () => request('/cron'),
  // POST /api/v1/cron → CronJob（CronAddRequest）
  add: (payload) => request('/cron', { method: 'POST', body: payload }),
  // PATCH /api/v1/cron/{id} {field:value,...} → CronJob
  patch: (id, fields) => request(`/cron/${encodeURIComponent(id)}`, { method: 'PATCH', body: fields }),
  // DELETE /api/v1/cron/{id}
  del: (id) => request(`/cron/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  // POST /api/v1/cron/{id}/exec 立即运行
  exec: (id) => request(`/cron/${encodeURIComponent(id)}/exec`, { method: 'POST', body: {} }),

  // GET /api/v1/projects → {projects:[{name, agent_type, platforms, sessions_count}]}
  listProjects: () => request('/projects'),
  // GET /api/v1/projects/{name}/models → {models:[string], current:string}
  getModels: (project) => request(`/projects/${encodeURIComponent(project)}/models`),
  // GET /api/v1/projects/{name}/sessions → {sessions:[{id, session_key, ...}], active_keys}
  getSessions: (project) => request(`/projects/${encodeURIComponent(project)}/sessions`),

  // GET /api/v1/projects/{name}/transcripts → {transcripts:[{id,name,size,updated_at}]}
  listTranscripts: (project) => request(`/projects/${encodeURIComponent(project)}/transcripts`),
  getTranscript: (project, id) => request(`/projects/${encodeURIComponent(project)}/transcripts/${encodeURIComponent(id)}`),

  // GET /api/v1/transcripts/{id} → 单条会话事件流（不依赖项目，刷新/直链可用）
  libraryTranscript: (id) => request(`/transcripts/${encodeURIComponent(id)}`),

  // POST /projects/{name}/transcripts/{id}/chat → SSE 流式继续对话。
  // 返回原始 Response（前端读 response.body 流），不是解析后的 JSON。
  chatTranscript: (project, id, payload) => {
    const headers = { 'Content-Type': 'application/json' }
    const tk = getToken()
    if (tk) headers.Authorization = `Bearer ${tk}`
    return fetch(`${BASE}/projects/${encodeURIComponent(project)}/transcripts/${encodeURIComponent(id)}/chat`, {
      method: 'POST', headers, body: JSON.stringify(payload),
    })
  },

  // POST /projects/{name}/transcripts/{id}/title → 用最近对话一键生成会话标题
  regenerateTitle: (project, id) => request(`/projects/${encodeURIComponent(project)}/transcripts/${encodeURIComponent(id)}/title`, { method: 'POST', body: {} }),

  // GET /api/v1/projects/{name} → 项目详情（work_dir/settings/platform_configs...）
  getProject: (project) => request(`/projects/${encodeURIComponent(project)}`),
  // PATCH /api/v1/projects/{name} {field:value,...} → 改项目参数（work_dir/model/admin_from/...）
  patchProject: (project, fields) => request(`/projects/${encodeURIComponent(project)}`, { method: 'PATCH', body: fields }),
  // DELETE /api/v1/projects/{name} → 移除项目
  delProject: (project) => request(`/projects/${encodeURIComponent(project)}`, { method: 'DELETE' }),
  // GET /api/v1/transcripts?view=&q=&since= → 全局会话库（含 cwd / job_id / archived）
  // since 只影响服务端「自动补标题」的入选范围，不减少返回结果。
  library: (view, q, since) => {
    const qs = new URLSearchParams()
    if (view) qs.set('view', view)
    if (q) qs.set('q', q)
    if (since) qs.set('since', new Date(since).toISOString())
    const s = qs.toString()
    return request(`/transcripts${s ? `?${s}` : ''}`)
  },
  // POST /api/v1/transcripts/archive {ids} → 软删除（隐藏）
  archiveSessions: (ids) => request('/transcripts/archive', { method: 'POST', body: { ids } }),
  // POST /api/v1/transcripts/unarchive {ids} → 从归档恢复
  unarchiveSessions: (ids) => request('/transcripts/unarchive', { method: 'POST', body: { ids } }),
  // GET/PATCH /api/v1/transcripts/settings → 自动归档设置
  archiveSettings: () => request('/transcripts/settings'),
  saveArchiveSettings: (fields) => request('/transcripts/settings', { method: 'PATCH', body: fields }),
  // POST /api/v1/transcripts/run → 立即执行一次自动归档
  runArchive: () => request('/transcripts/run', { method: 'POST' }),
  // GET /api/v1/cron/{id}/runs → 任务执行历史（含失败记录）
  cronRuns: (id) => request(`/cron/${encodeURIComponent(id)}/runs`),
  // POST /api/v1/projects → 创建项目（服务端生成不可变 ID；name 仅作显示名）
  createProject: (fields) => request('/projects', { method: 'POST', body: fields }),
  // POST /api/v1/projects/{name}/add-platform → 为已有项目追加平台
  addPlatform: (project, fields) => request(`/projects/${encodeURIComponent(project)}/add-platform`, { method: 'POST', body: fields }),
  // POST /api/v1/projects/{name}/bot/qr/begin {platform?, app_id?} → 扫码绑定流程
  beginBotQR: (project, fields) => request(`/projects/${encodeURIComponent(project)}/bot/qr/begin`, { method: 'POST', body: fields }),
  // POST /api/v1/projects/{name}/bot/qr/poll {device_code, platform?} → {status, app_id?, hot_swapped?}
  pollBotQR: (project, payload) => request(`/projects/${encodeURIComponent(project)}/bot/qr/poll`, { method: 'POST', body: payload }),
  // GET /api/v1/fs/dirs?path= → {path, parent, dirs:[{name, path}]}（work_dir 选择）
  listDirs: (path) => request(`/fs/dirs?path=${encodeURIComponent(path || '')}`),
  // omp agent personas（bundled/user/project）
  // GET /api/v1/omp-agents → {agents:[{name,description,scope,model,tools,source,read_only,project}]}
  listAgents: () => request('/omp-agents'),
  // GET /api/v1/omp-agents/{name} → {agent:{...content}}
  getAgent: (name) => request(`/omp-agents/${encodeURIComponent(name)}`),
  // POST /api/v1/omp-agents {name,description,model,tools,body} → 创建 user agent
  addAgent: (payload) => request('/omp-agents', { method: 'POST', body: payload }),
  // PUT /api/v1/omp-agents/{name} {description,model,tools,body} → 更新 user agent
  updateAgent: (name, payload) => request(`/omp-agents/${encodeURIComponent(name)}`, { method: 'PUT', body: payload }),
  // DELETE /api/v1/omp-agents/{name}
  delAgent: (name) => request(`/omp-agents/${encodeURIComponent(name)}`, { method: 'DELETE' }),
}
