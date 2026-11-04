// 会话体渲染：结构与 ompweb 的 ChatWindow 一致（turn 分组 + Process details 折叠），
// 但只保留只读需要的那部分（没有流式、搜索高亮、分支导航、编辑/分叉）。
// 数据来自 lib/sessionView 的 buildSessionContext，产物与 omtweb 同一套 MessageView。

import { useMemo, useState, type ReactNode } from "react";
import { ChevronDown, Layers } from "lucide-react";
import { MessageView } from "./MessageView";
import {
  collectToolResults,
  planTranscriptRows,
  type ActivityPiece,
} from "../lib/chat-transcript-plan";
import { useI18n } from "../lib/i18n";
import type { AgentMessage, AssistantContentBlock, AssistantMessage, ToolResultMessage } from "../lib/types";

function withAssistantBlocks(
  message: AssistantMessage,
  content: AssistantContentBlock[],
  options: { omitUsage?: boolean } = {},
): AssistantMessage {
  const next = { ...message, content };
  if (options.omitUsage) next.usage = undefined;
  return next;
}

type RenderMessage = (idx: number, options?: { keyPrefix?: string; messageOverride?: AgentMessage; showTimestamp?: boolean }) => ReactNode;

/** 折叠的一组活动（思考 / 工具调用），与 omtweb 的文案与交互一致。 */
function ProcessDetailsGroup({ messageCount, toolCallCount, children }: { messageCount: number; toolCallCount: number; children: () => ReactNode }) {
  const { t, tn } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const parts = [t("chatWindow.processDetails"), tn("chatWindow.messageCount", messageCount)];
  if (toolCallCount > 0) parts.push(tn("chatWindow.toolCallCount", toolCallCount));

  return (
    <div style={{ marginBottom: 4 }}>
      <button
        type="button"
        aria-expanded={expanded}
        onClick={() => setExpanded((v) => !v)}
        className="process-details-toggle"
        title={expanded ? t("chatWindow.collapseProcessDetails") : t("chatWindow.expandProcessDetails")}
      >
        <Layers size={12} strokeWidth={1.8} aria-hidden="true" style={{ flexShrink: 0, color: "var(--accent)" }} />
        <ChevronDown
          size={12}
          strokeWidth={1.8}
          aria-hidden="true"
          style={{ flexShrink: 0, transform: expanded ? "rotate(180deg)" : "none", transition: "transform var(--dur-fast) var(--ease-out-warm)" }}
        />
        <span className="process-details-label">{parts.join(" · ")}</span>
      </button>
      {expanded && <div style={{ marginTop: 3 }}>{children()}</div>}
    </div>
  );
}

function renderActivityPieces(messages: AgentMessage[], pieces: ActivityPiece[], renderMessage: RenderMessage): ReactNode[] {
  return pieces.map((piece, pieceIdx) => {
    if (!piece.blocks) return renderMessage(piece.index, { keyPrefix: "activity" });
    const msg = messages[piece.index] as AssistantMessage;
    return renderMessage(piece.index, {
      keyPrefix: `activity-${pieceIdx}`,
      messageOverride: { ...withAssistantBlocks(msg, piece.blocks, { omitUsage: true }), errorMessage: undefined },
      showTimestamp: false,
    });
  });
}

interface TranscriptBodyProps {
  messages: AgentMessage[];
  cwd?: string;
  modelNames?: Record<string, string>;
  toolCallsDefaultCollapsed?: boolean;
  expandThinkingByDefault?: boolean;
  hideThinking?: boolean;
}

export function TranscriptBody({ messages, cwd, modelNames, toolCallsDefaultCollapsed = true, expandThinkingByDefault = false, hideThinking = false }: TranscriptBodyProps) {
  const toolResults: Map<string, ToolResultMessage> = useMemo(() => collectToolResults(messages), [messages]);
  const rows = useMemo(
    () => planTranscriptRows(messages, { hideThinking, showThinking: expandThinkingByDefault, showToolCalls: !toolCallsDefaultCollapsed }),
    [messages, hideThinking, expandThinkingByDefault, toolCallsDefaultCollapsed],
  );

  const renderMessage: RenderMessage = (idx, options = {}) => {
    const message = options.messageOverride ?? messages[idx];
    if (!message) return null;
    return (
      <MessageView
        key={`${options.keyPrefix ?? "msg"}-${idx}`}
        message={message}
        toolResults={toolResults}
        modelNames={modelNames}
        cwd={cwd}
        showTimestamp={options.showTimestamp}
        toolCallsDefaultCollapsed={toolCallsDefaultCollapsed}
        expandThinkingByDefault={expandThinkingByDefault}
        hideThinking={hideThinking}
      />
    );
  };

  const rendered: ReactNode[] = [];
  rows.forEach((row, rowIdx) => {
    if (row.kind === "message") {
      rendered.push(renderMessage(row.index, { keyPrefix: `row-${rowIdx}` }));
      return;
    }
    rendered.push(renderMessage(row.userIndex, { keyPrefix: `anchor-${rowIdx}` }));
    row.segments.forEach((segment, segmentIdx) => {
      if (segment.kind === "text") {
        const msg = messages[segment.index] as AssistantMessage;
        const override = withAssistantBlocks(msg, segment.blocks, { omitUsage: !segment.last });
        if (!segment.last) override.errorMessage = undefined;
        rendered.push(renderMessage(segment.index, {
          keyPrefix: `text-${rowIdx}-${segmentIdx}`,
          messageOverride: override,
          ...(segment.last ? {} : { showTimestamp: false }),
        }));
        return;
      }
      rendered.push(
        <div key={`activity-${rowIdx}-${segmentIdx}`}>
          <ProcessDetailsGroup messageCount={segment.stepCount} toolCallCount={segment.toolCallCount}>
            {() => renderActivityPieces(messages, segment.pieces, renderMessage)}
          </ProcessDetailsGroup>
        </div>,
      );
    });
  });

  return <div className="omp-transcript">{rendered}</div>;
}
