package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lzyyzznl/omp-lark-bridge/config"
)

// flag 包遇到第一个位置参数就停，用法示例却是 flag 后置；解析必须两种顺序都认。
func TestSplitConfigArgs_FlagAfterPositionals(t *testing.T) {
	path, positional, err := splitConfigArgs([]string{"management.token", "1", "--config", `C:\x\config.toml`})
	if err != nil {
		t.Fatalf("splitConfigArgs: %v", err)
	}
	if path != `C:\x\config.toml` {
		t.Errorf("configPath = %q", path)
	}
	if len(positional) != 2 || positional[0] != "management.token" || positional[1] != "1" {
		t.Errorf("positional = %v", positional)
	}

	path, positional, err = splitConfigArgs([]string{"--config=/tmp/c.toml", "management.port", "28461"})
	if err != nil || path != "/tmp/c.toml" || len(positional) != 2 {
		t.Errorf("flag-first form: path=%q positional=%v err=%v", path, positional, err)
	}

	if _, _, err := splitConfigArgs([]string{"management.token", "1", "--config"}); err == nil {
		t.Error("dangling --config should fail")
	}
}

// 整表/数组不能被 get/set 当标量处理。
func TestLookupScalar_RejectsTables(t *testing.T) {
	cfg := &config.Config{}
	key := "management"
	v, err := config.LookupScalar(cfg, key)
	if err != nil {
		t.Fatalf("LookupScalar(%q): %v", key, err)
	}
	if config.IsScalar(v.Kind()) {
		t.Errorf("%s resolved to %s, IsScalar should be false", key, v.Kind())
	}
	if _, err := config.ScalarLiteral(v.Kind(), "x"); err == nil {
		t.Error("ScalarLiteral should reject a struct value")
	}
	if config.IsScalar(reflect.String) != true || config.IsScalar(reflect.Slice) != false {
		t.Error("IsScalar kind handling is wrong")
	}
}

// 字符串类型必须写成带引号的字面量，否则下次加载会变成整型。
func TestScalarLiteral_StringIsQuoted(t *testing.T) {
	got, err := config.ScalarLiteral(reflect.String, "1")
	if err != nil || got != `"1"` {
		t.Errorf(`ScalarLiteral(string, "1") = %s, %v; want "1" quoted`, got, err)
	}
	if !strings.HasPrefix(got, `"`) {
		t.Error("string literal must be quoted")
	}
}
