package core

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSessionBody = `{"type":"session","version":3,"id":"01a17738","timestamp":"2026-10-26T07:59:28.982Z","cwd":"C:\\work"}
{"type":"message","id":"a1","role":"user","content":"hi"}
`

func writeTempTranscript(t *testing.T, firstLine string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(firstLine+"\n"+testSessionBody), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

// omp refuses to resume a session unless its first line is a 255-byte title
// slot carrying a "pad" field; a compact title line silently breaks the file.
func TestWriteTranscriptTitleKeepsOmpSlotShape(t *testing.T) {
	slot, ok := marshalOmpTitleSlot("", "", "2026-10-26T08:30:53.532Z")
	if !ok {
		t.Fatal("empty title did not fit the slot")
	}
	path := writeTempTranscript(t, string(slot))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if err := writeTranscriptTitle(path, "介绍 omp 智能体能力"); err != nil {
		t.Fatalf("writeTranscriptTitle: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("in-place rewrite changed file size: %d -> %d", len(before), len(after))
	}
	lines := bytes.SplitN(after, []byte("\n"), 2)
	if len(lines[0]) != ompTitleSlotWidth {
		t.Fatalf("slot width = %d, want %d", len(lines[0]), ompTitleSlotWidth)
	}
	var got ompTitleSlot
	if err := json.Unmarshal(lines[0], &got); err != nil {
		t.Fatalf("slot is not valid JSON: %v", err)
	}
	if got.Type != "title" || got.V != 1 || got.Title != "介绍 omp 智能体能力" || got.Pad == "" {
		t.Fatalf("unexpected slot: %+v", got)
	}
	if string(lines[1]) != testSessionBody {
		t.Fatalf("body changed:\n%s", lines[1])
	}
}

// Sessions written by older builds start with a compact title line and can no
// longer be resumed; rewriting the title must heal them.
func TestWriteTranscriptTitleRepairsLegacyTitleLine(t *testing.T) {
	legacy := `{"title":"old","type":"title","updatedAt":"2026-10-26T08:21:40.1791781Z","v":1}`
	path := writeTempTranscript(t, legacy)

	if err := writeTranscriptTitle(path, "修复后的标题"); err != nil {
		t.Fatalf("writeTranscriptTitle: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	lines := bytes.SplitN(after, []byte("\n"), 2)
	if len(lines[0]) != ompTitleSlotWidth {
		t.Fatalf("slot width = %d, want %d", len(lines[0]), ompTitleSlotWidth)
	}
	var got ompTitleSlot
	if err := json.Unmarshal(lines[0], &got); err != nil {
		t.Fatalf("slot is not valid JSON: %v", err)
	}
	if got.Title != "修复后的标题" || got.Pad == "" {
		t.Fatalf("unexpected slot: %+v", got)
	}
	if string(lines[1]) != testSessionBody {
		t.Fatalf("body changed:\n%s", lines[1])
	}
}

func TestMarshalOmpTitleSlotTruncatesLongTitles(t *testing.T) {
	long := strings.Repeat("很长", 120)
	slot, ok := marshalOmpTitleSlot(long, "auto", "2026-10-26T08:30:53.532Z")
	if !ok {
		t.Fatal("long title could not be shortened to fit")
	}
	if len(slot) != ompTitleSlotWidth {
		t.Fatalf("slot width = %d, want %d", len(slot), ompTitleSlotWidth)
	}
	var got ompTitleSlot
	if err := json.Unmarshal(slot, &got); err != nil {
		t.Fatalf("slot is not valid JSON: %v", err)
	}
	if got.Source != "auto" || got.Title == "" || len([]rune(got.Title)) >= len([]rune(long)) {
		t.Fatalf("unexpected truncated slot: %+v", got)
	}
}
