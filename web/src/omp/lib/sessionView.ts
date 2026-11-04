// 从 ompweb 的 lib/session-reader.ts + lib/omp/session-files.ts 里裁出的**纯逻辑**：
// 只有“读取文件”依赖 fs，而 olb 后端把 jsonl 原文直接发给浏览器，所以这里
// 只保留解析、版本迁移、分支选择与 entry → UI message 的转换。
// 语义与 ompweb 完全一致（同一套 leaf→root 路径、compaction 折叠、时间戳归一）。

import { isRecord } from "./type-guards";
import { normalizeToolCalls } from "./normalize";
import { PASSIVE_TOOL_CONTEXT } from "./chat-transcript-plan";
import type {
  AgentMessage,
  CustomMessage,
  SessionContext,
  SessionEntry,
} from "./types";

/** omp 的会话文件版本；老文件（v1/v2）缺 id/parentId/tree 结构。 */
const CURRENT_SESSION_VERSION = 3;

const SUPERSEDED_COMPACTION_SUMMARY = "[Superseded compaction summary elided after a newer compaction]";

type MutableEntry = SessionEntry & Record<string, unknown>;

export interface SessionHeaderLike {
  type: "session";
  version?: number;
  id: string;
  title?: string;
  timestamp?: string;
  cwd?: string;
  [key: string]: unknown;
}

/** JSONL 宽松解析：坏行跳过（写入撕裂/半行），不致命。 */
export function parseJsonlLenient<T>(body: string): T[] {
  const out: T[] = [];
  for (const rawLine of body.split("\n")) {
    const line = rawLine.trim();
    if (!line) continue;
    try {
      out.push(JSON.parse(line) as T);
    } catch {
      // 跳过破损行
    }
  }
  return out;
}

function generateEntryId(taken: Set<string>): string {
  for (let i = 0; i < 100; i++) {
    const id = crypto.randomUUID().slice(-8);
    if (!taken.has(id)) return id;
  }
  return crypto.randomUUID();
}

/** v1 → v2：补 id/parentId 链表。就地改写。 */
function migrateV1ToV2(header: SessionHeaderLike, entries: MutableEntry[]): void {
  header.version = 2;
  const ids = new Set<string>();
  let prevId: string | null = null;
  for (const entry of entries) {
    entry.id = generateEntryId(ids);
    ids.add(entry.id);
    entry.parentId = prevId;
    prevId = entry.id;
    if (entry.type === "compaction" && typeof entry.firstKeptEntryIndex === "number") {
      const target = entry.firstKeptEntryIndex >= 1 ? entries[entry.firstKeptEntryIndex - 1] : undefined;
      if (target) entry.firstKeptEntryId = target.id;
      delete entry.firstKeptEntryIndex;
    }
  }
}

/** v2 → v3：hookMessage 角色改名为 custom。 */
function migrateV2ToV3(header: SessionHeaderLike, entries: MutableEntry[]): void {
  header.version = 3;
  for (const entry of entries) {
    if (entry.type === "message") {
      const message = entry.message as { role?: string } | null | undefined;
      if (message?.role === "hookMessage") message.role = "custom";
    }
  }
}
/** 把一份 jsonl 原文解析成 header + entries（等价于 ompweb 的 loadSessionFile）。
 *  首行常常是固定宽度的 title slot（{"type":"title",…}），它的标题要折叠进
 *  header，再跳过它去找真正的 session 行。 */
export function parseSessionFile(text: string): { header: SessionHeaderLike | null; entries: SessionEntry[] } {
  const records = parseJsonlLenient<Record<string, unknown>>(text);
  const titleSlot = records[0]?.type === "title" ? records[0] : undefined;
  const start = titleSlot ? 1 : 0;
  const headerRecord = records[start];
  if (!headerRecord || headerRecord.type !== "session" || typeof headerRecord.id !== "string") {
    return { header: null, entries: [] };
  }
  const header = headerRecord as unknown as SessionHeaderLike;
  if (typeof titleSlot?.title === "string" && titleSlot.title) header.title = titleSlot.title;
  const entries = records.slice(start + 1).filter((record) => record && record.type !== "session") as unknown as MutableEntry[];
  const version = header.version ?? 1;
  if (version < 2) migrateV1ToV2(header, entries);
  if (version < 3) migrateV2ToV3(header, entries);
  return { header, entries };
}

function parseEntryTimestamp(timestamp: string): number | undefined {
  const parsed = Date.parse(timestamp);
  return Number.isNaN(parsed) ? undefined : parsed;
}

function normalizeMessageTimestamp(value: unknown): number | undefined {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
  if (typeof value === "string") return parseEntryTimestamp(value);
  return undefined;
}

function withNormalizedTimestamp(message: AgentMessage): AgentMessage {
  const raw = message.timestamp;
  const timestamp = normalizeMessageTimestamp(raw);
  return timestamp === raw ? message : { ...message, timestamp };
}

function compactionUiMessage(raw: SessionEntry, active: boolean): CustomMessage {
  // 裁剪后 entry 的联合类型不再可完全表达（compaction 的字段太多），
  // 统一按 MutableEntry 读；字段存在性由运行时 ?? 兜底。
  const entry: MutableEntry = raw as MutableEntry;
  return {
    role: "custom",
    customType: "compaction",
    content: active ? ((entry.summary as string) ?? "") : SUPERSEDED_COMPACTION_SUMMARY,
    display: true,
    details: {
      tokensBefore: entry.tokensBefore,
      ...(entry.tokensAfter !== undefined ? { tokensAfter: entry.tokensAfter } : {}),
      ...(entry.method !== undefined ? { method: entry.method } : {}),
      firstKeptEntryId: entry.firstKeptEntryId,
    },
    timestamp: parseEntryTimestamp(entry.timestamp),
  };
}

/** 把活动分支上的 entry 转成 UI 消息；不参与对话历史的 entry 返回 null。 */
export function entryToUiMessage(rawEntry: SessionEntry): AgentMessage | null {
  // 同上：按 MutableEntry 读，避免每处内联断言。
  const entry: MutableEntry = rawEntry as MutableEntry;
  const e = entry;
  switch (entry.type) {
    case "message": {
      const raw = e.message;
      if (!isRecord(raw)) return null;
      if (raw.role === "developer") {
        const passive = raw.passiveToolContext === true;
        return {
          role: "custom",
          customType: passive ? PASSIVE_TOOL_CONTEXT : "developer",
          content: raw.content,
          display: !passive,
          timestamp: normalizeMessageTimestamp(raw.timestamp),
        } as AgentMessage;
      }
      if (raw.role === "pythonExecution") {
        const output = raw.output ? `\n\`\`\`\n${raw.output}\n\`\`\`` : "\n(no output)";
        const status = raw.cancelled
          ? "\n(execution cancelled)"
          : raw.exitCode ? `\nExecution failed with code ${raw.exitCode}` : "";
        return {
          role: "custom",
          customType: "python-execution",
          content: `Ran Python:\n\`\`\`python\n${raw.code}\n\`\`\`${output}${status}`,
          display: true,
          timestamp: normalizeMessageTimestamp(raw.timestamp),
        } as AgentMessage;
      }
      if (raw.role === "fileMention") {
        const files = Array.isArray(raw.files)
          ? (raw.files as unknown[]).filter((f): f is { path: string } =>
              !!f && typeof f === "object" && typeof (f as { path?: unknown }).path === "string")
          : [];
        return {
          role: "custom",
          customType: "file-mention",
          content: `Attached file${files.length === 1 ? "" : "s"}:\n${files.map((f) => `- ${f.path}`).join("\n")}`,
          display: true,
          timestamp: normalizeMessageTimestamp(raw.timestamp),
        } as AgentMessage;
      }
      return withNormalizedTimestamp(normalizeToolCalls(raw as never) as AgentMessage);
    }
    case "branch_summary":
      if (!e.summary) return null;
      return {
        role: "user",
        content: `*The conversation briefly explored another branch and returned with this summary:*\n\n${e.summary}`,
        timestamp: parseEntryTimestamp(entry.timestamp),
        branchSummary: true,
      } as AgentMessage;
    case "custom_message":
      return {
        role: "custom",
        customType: e.customType,
        content: e.content,
        display: e.display,
        details: e.details,
        timestamp: parseEntryTimestamp(entry.timestamp),
      } as AgentMessage;
    default:
      // model_change / thinking_level_change / label / title_change 等元数据不渲染
      return null;
  }
}

/** 从 leaf 沿 parentId 回溯到 root，返回渲染上下文。 */
export function buildSessionContext(entries: SessionEntry[], leafId?: string | null): SessionContext {
  const empty: SessionContext = { messages: [], entryIds: [], thinkingLevel: "off", model: null, todoPhases: [] };
  const byId = new Map<string, SessionEntry>();
  for (const e of entries) byId.set(e.id, e);
  if (leafId === null) return empty;
  const leaf = (leafId ? byId.get(leafId) : undefined) ?? entries.at(-1);
  if (!leaf) return empty;

  const path: SessionEntry[] = [];
  const seen = new Set<string>();
  let current: SessionEntry | undefined = leaf;
  while (current && !seen.has(current.id)) {
    seen.add(current.id);
    path.push(current);
    current = current.parentId ? byId.get(current.parentId) : undefined;
  }
  path.reverse();

  let thinkingLevel = "off";
  let compaction: MutableEntry | null = null;
  for (const step of path) {
    const e = step as MutableEntry;
    if (step.type === "thinking_level_change") {
      thinkingLevel = (e.thinkingLevel as string) ?? "off";
    } else if (step.type === "compaction") {
      compaction = e;
    }
  }

  const selected: SessionEntry[] = [];
  const appendEntry = (step: SessionEntry) => {
    const e = step as MutableEntry;
    if (step.type === "compaction" || step.type === "custom_message"
      || (step.type === "message" && isRecord(e.message))
      || (step.type === "branch_summary" && e.summary)) {
      selected.push(step);
    }
  };
  if (compaction) {
    const activeCompaction = compaction;
    appendEntry(activeCompaction);
    const compactionIdx = path.findIndex((e) => e.type === "compaction" && e.id === activeCompaction.id);
    let foundFirstKept = false;
    for (let i = 0; i < compactionIdx; i++) {
      const entry = path[i];
      if (entry.id === activeCompaction.firstKeptEntryId) foundFirstKept = true;
      if (foundFirstKept) appendEntry(entry);
    }
    for (let i = compactionIdx + 1; i < path.length; i++) appendEntry(path[i]);
  } else {
    for (const entry of path) appendEntry(entry);
  }

  const messages: AgentMessage[] = [];
  const entryIds: string[] = [];
  for (const entry of selected) {
    const message = entry.type === "compaction"
      ? compactionUiMessage(entry, entry.id === compaction?.id)
      : entryToUiMessage(entry);
    if (!message) continue;
    messages.push(message);
    entryIds.push(entry.id);
  }
  return { messages, entryIds, thinkingLevel, model: null, todoPhases: [] };
}
