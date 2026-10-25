package omp

import (
	"testing"

	"github.com/lzyyzznl/omp-lark-bridge/core"
)

func TestParseSkillList(t *testing.T) {
	in := []byte(`{"skills":[{"name":"edw-zmail","description":"邮件","source":"user","hide":false},
		{"name":"secret-skill","description":"隐藏","source":"project","hide":true}],"warnings":[]}`)
	skills, err := parseSkillList(in)
	if err != nil {
		t.Fatalf("parseSkillList error = %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("skills = %d, want 2", len(skills))
	}
	want := core.Skill{Name: "edw-zmail", Description: "邮件", Source: "user", Hide: false}
	if skills[0] != want {
		t.Errorf("skills[0] = %+v, want %+v", skills[0], want)
	}
	if !skills[1].Hide {
		t.Errorf("skills[1].Hide = false, want true")
	}
}

func TestParseSkillList_BadJSON(t *testing.T) {
	if _, err := parseSkillList([]byte(`{nope`)); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
