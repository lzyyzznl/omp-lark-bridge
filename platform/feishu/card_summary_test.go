package feishu

import (
	"encoding/json"
	"testing"

	"github.com/lzyyzznl/omp-lark-bridge/core"
)

func cardSummaryContent(t *testing.T, cardJSON string) (string, bool) {
	t.Helper()
	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("card JSON is invalid: %v\n%s", err, cardJSON)
	}
	cfg, _ := card["config"].(map[string]any)
	summary, ok := cfg["summary"].(map[string]any)
	if !ok {
		return "", false
	}
	content, _ := summary["content"].(string)
	return content, true
}

// cardHasButton reports whether any element (at any depth) is a button.
func cardHasButton(t *testing.T, cardJSON string) bool {
	t.Helper()
	var card map[string]any
	if err := json.Unmarshal([]byte(cardJSON), &card); err != nil {
		t.Fatalf("card JSON is invalid: %v\n%s", err, cardJSON)
	}
	found := false
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if tag, _ := v["tag"].(string); tag == "button" {
				found = true
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(card)
	return found
}

func TestBuildRichCard_SettledCardHasNoButtons(t *testing.T) {
	for _, status := range []core.CardStatus{core.CardStatusDone, core.CardStatusError} {
		cardJSON := buildRichCard(status, "", []core.ToolStep{
			{Kind: core.ToolStepKindTool, Name: "Bash", Summary: "go build"},
		}, "answer", false, "", core.LangChinese)
		if cardHasButton(t, cardJSON) {
			t.Fatalf("status %v card must stay button-free: %s", status, cardJSON)
		}
	}
}

func TestBuildRichCard_SettledCardCarriesSummary(t *testing.T) {
	cardJSON := buildRichCard(core.CardStatusDone, "", nil, "## 结论：可以升级\n\n细节内容", false, "", core.LangEnglish)

	summary, ok := cardSummaryContent(t, cardJSON)
	if !ok {
		t.Fatalf("settled card should carry a config.summary preview: %s", cardJSON)
	}
	if summary != "结论：可以升级" {
		t.Fatalf("summary = %q, want the first body line with markdown stripped", summary)
	}
}

func TestBuildRichCard_StreamingCardKeepsDefaultPreview(t *testing.T) {
	cardJSON := buildRichCard(core.CardStatusWorking, "", []core.ToolStep{
		{Kind: core.ToolStepKindThinking, Summary: "reading files"},
	}, "partial answer", true, "", core.LangEnglish)

	if _, ok := cardSummaryContent(t, cardJSON); ok {
		t.Fatalf("streaming card should keep Feishu's default preview, not a custom summary: %s", cardJSON)
	}
}

func TestBuildRichCard_ChineseLabels(t *testing.T) {
	cardJSON := buildRichCard(core.CardStatusDone, "", []core.ToolStep{
		{Kind: core.ToolStepKindThinking, Summary: "看代码"},
		{Kind: core.ToolStepKindTool, Name: "Bash", Summary: "go build"},
	}, "完成内容", false, "", core.LangChinese)

	panels := collectCardPanels(t, cardJSON)
	if len(panels) != 2 {
		t.Fatalf("panel count = %d, want 2: %#v", len(panels), panels)
	}
	if got := cardPanelTitle(panels[0]); got != "思考 (1)" {
		t.Fatalf("reasoning panel title = %q, want 思考 (1)", got)
	}
	if got := cardPanelTitle(panels[1]); got != "工具 (1)" {
		t.Fatalf("tools panel title = %q, want 工具 (1)", got)
	}
}

func TestRichCardSummary_SkipsEmptyAndTableLines(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"\n\n":                   "",
		"| a | b |\n|---|---|\n": "",
		"**粗体结论**\n正文":           "粗体结论",
		"### 标题\n正文":             "标题",
		"- 列表项":                  "列表项",
	}
	for input, want := range cases {
		if got := richCardSummary(input); got != want {
			t.Fatalf("richCardSummary(%q) = %q, want %q", input, got, want)
		}
	}
	long := ""
	for range cardSummaryMaxRunes + 10 {
		long += "字"
	}
	if got := richCardSummary(long); len([]rune(got)) != cardSummaryMaxRunes {
		t.Fatalf("long summary rune length = %d, want %d (truncated, ellipsis included)", len([]rune(got)), cardSummaryMaxRunes)
	}
}
