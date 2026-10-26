package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const sampleConfig = `language = "zh"

[management]
enabled = true
port = 9820   # 看板端口
token = "old-secret"

[[projects]]
name = "demo"
`

// 设置已存在的键：只换值，注释与其余内容原样保留。
func TestSetScalarInFile_ReplacesValueKeepsComment(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)

	if err := SetScalarInFile(path, "management.port", "9900"); err != nil {
		t.Fatalf("SetScalarInFile: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "port = 9900   # 看板端口") {
		t.Errorf("comment or spacing lost:\n%s", got)
	}
	if !strings.Contains(got, `token = "old-secret"`) {
		t.Errorf("unrelated key changed:\n%s", got)
	}
	if strings.Contains(got, "port = 9820") {
		t.Errorf("old value still present:\n%s", got)
	}
}

// 设置缺省的键：插到段首，不破坏其它段。
func TestSetScalarInFile_InsertsMissingKey(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)

	if err := SetScalarInFile(path, "management.host", `"127.0.0.1"`); err != nil {
		t.Fatalf("SetScalarInFile: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "[management]\nhost = \"127.0.0.1\"\n") {
		t.Errorf("key not inserted under its table:\n%s", got)
	}
	var cfg Config
	if _, err := toml.Decode(got, &cfg); err != nil {
		t.Fatalf("edited file does not parse: %v", err)
	}
	if cfg.Management.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1", cfg.Management.Host)
	}
}

// 设置不存在的段：追加到文件尾。
func TestSetScalarInFile_AppendsMissingTable(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)

	if err := SetScalarInFile(path, "log.level", `"debug"`); err != nil {
		t.Fatalf("SetScalarInFile: %v", err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "[log]\nlevel = \"debug\"") {
		t.Errorf("missing table not appended:\n%s", got)
	}
}

// 顶层键（无段）不能落到第一个段的内部。
func TestSetScalarInFile_TopLevelKey(t *testing.T) {
	path := writeTempConfig(t, sampleConfig)

	if err := SetScalarInFile(path, "language", `"en"`); err != nil {
		t.Fatalf("SetScalarInFile: %v", err)
	}
	got := readFile(t, path)
	if !strings.HasPrefix(got, `language = "en"`) {
		t.Errorf("top-level key not rewritten in place:\n%s", got)
	}
}

// 值类型不匹配要在写盘前拒绝。
func TestScalarLiteral_RejectsWrongType(t *testing.T) {
	if _, err := ScalarLiteral(reflect.Int, "abc"); err == nil {
		t.Error("ScalarLiteral(int, \"abc\") should fail")
	}
	if _, err := ScalarLiteral(reflect.Bool, "yes-please"); err == nil {
		t.Error("ScalarLiteral(bool, \"yes-please\") should fail")
	}
	if got, err := ScalarLiteral(reflect.String, "1"); err != nil || got != `"1"` {
		t.Errorf("ScalarLiteral(string, \"1\") = %q, %v; want quoted \"1\"", got, err)
	}
}

// 键解析：嵌套表与未设置的 *bool。
func TestLookupScalar(t *testing.T) {
	cfg, err := LoadRaw(writeTempConfig(t, sampleConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	v, err := LookupScalar(cfg, "management.token")
	if err != nil || v.String() != "old-secret" {
		t.Errorf("management.token = %v, %v", v, err)
	}
	v, err = LookupScalar(cfg, "language")
	if err != nil || v.String() != "zh" {
		t.Errorf("language = %v, %v", v, err)
	}
	if _, err := LookupScalar(cfg, "management.nope"); err == nil {
		t.Error("unknown key should fail")
	}
	if _, err := LookupScalar(cfg, "nope.token"); err == nil {
		t.Error("unknown table should fail")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
