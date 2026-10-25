// in-one-chat management API 封装。
// 端点全部形如 {ok, data} / {ok:false, error}（见 core/management.go）。
// base：默认相对路径走 vite dev proxy（/api/v1 → 127.0.0.1:31990），
// 生产可 VITE_API_BASE 指向同源反代地址或绝对地址。

const BASE = import.meta.env.VITE_API_BASE || '/api/v1'
const TOKEN_KEY = 'inOneChat.token'

export function getToken() {
  try { return localStorage.getItem(TOKEN_KEY) || '' } catch { return '' }
}
export function setToken(t) {
  try { t ? localStorage.setItem(TOKEN_KEY, t) : localStorage.removeItem(TOKEN_KEY) } catch {}
}

async function request(path, { method = 'GET', body } = {}) {
  const headers = { 'Content-Type': 'application/json' }
  const tk = getToken()
  if (tk) headers.Authorization = `Bearer ${tk}`

  let res
  try {
    res = await fetch(`${BASE}${path}`, { method, headers, body: body ? JSON.stringify(body) : undefined })
  } catch {
    throw new Error('无法连接 management API —— 确认 in-one-chat 已开启 [management]，且地址正确（右上角「设置」）')
  }

  let data = null
  try { data = await res.json() } catch {}

  if (res.status === 401) {
    throw new Error('认证失败：token 缺失或错误（右上角「设置」填写）')
  }
  if (!data || data.ok !== true) {
    throw new Error(data?.error || `请求失败（HTTP ${res.status}）`)
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

  // GET /api/v1/projects/{name} → 项目详情（work_dir/settings/platform_configs...）
  getProject: (project) => request(`/projects/${encodeURIComponent(project)}`),
  // PATCH /api/v1/projects/{name} {field:value,...} → 改项目参数（work_dir/model/admin_from/...）
  patchProject: (project, fields) => request(`/projects/${encodeURIComponent(project)}`, { method: 'PATCH', body: fields }),
  // DELETE /api/v1/projects/{name} → 移除项目
  delProject: (project) => request(`/projects/${encodeURIComponent(project)}`, { method: 'DELETE' }),
  // POST /api/v1/projects/{name}/add-platform → 新建项目（type=feishu+work_dir+agent_type）或增平台
  addPlatform: (project, fields) => request(`/projects/${encodeURIComponent(project)}/add-platform`, { method: 'POST', body: fields }),
}
