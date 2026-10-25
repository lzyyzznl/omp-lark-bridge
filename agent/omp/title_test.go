package omp

import (
	"strings"
	"testing"
)

func TestCleanTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  查询今日末封邮件 \n", "查询今日末封邮件"},
		{"# 会话标题\n解释文字", "会话标题"},
		{"```\n标题\n```", "标题"},
		{"```text\n标题\n```", "标题"},
		{"\n\n  你好问候对话  \n\n", "你好问候对话"},
		{"", ""},
	}
	for _, c := range cases {
		if got := cleanTitle(c.in); got != c.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	long := strings.Repeat("字", 60)
	if got := cleanTitle(long); len([]rune(got)) != 40 {
		t.Errorf("long title len = %d runes, want 40", len([]rune(got)))
	}
}
