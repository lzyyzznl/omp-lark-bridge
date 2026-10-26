package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Scalar settings CLI support: `config get <dotted.key>` / `config set
// <dotted.key> <value>`. Keys address TOML scalars by their toml tag path
// ("management.token", "language", "log.level"), so the names users type match
// the file they would otherwise hand-edit. Writes preserve comments and
// formatting by rewriting the single `key = value` line, and are rejected
// unless the result still parses.

// LookupScalar resolves a dotted TOML key against cfg.
func LookupScalar(cfg *Config, dotted string) (reflect.Value, error) {
	path := strings.Split(strings.TrimSpace(dotted), ".")
	if len(path) == 0 || path[0] == "" {
		return reflect.Value{}, fmt.Errorf("empty key")
	}
	cur := reflect.ValueOf(cfg).Elem()
	for i, name := range path {
		for cur.Kind() == reflect.Pointer {
			if cur.IsNil() {
				// A nil pointer (e.g. unset *bool) has no value: report the zero
				// value of its element so `get` prints something meaningful.
				return reflect.Zero(cur.Type().Elem()), nil
			}
			cur = cur.Elem()
		}
		if cur.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("key %q: %q is not a table", dotted, strings.Join(path[:i], "."))
		}
		f, ok := fieldByTag(cur, name)
		if !ok {
			return reflect.Value{}, fmt.Errorf("unknown key %q", dotted)
		}
		cur = f
	}
	for cur.Kind() == reflect.Pointer {
		if cur.IsNil() {
			return reflect.Zero(cur.Type().Elem()), nil
		}
		cur = cur.Elem()
	}
	return cur, nil
}

// fieldByTag returns the struct field whose toml tag (name before the comma)
// matches name.
func fieldByTag(v reflect.Value, name string) (reflect.Value, bool) {
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		tag := sf.Tag.Get("toml")
		if tag == "" || tag == "-" {
			continue
		}
		if strings.Split(tag, ",")[0] == name {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// IsScalar reports whether a resolved setting can be read or written as a TOML
// scalar. Tables (struct), arrays (slice) and maps are not addressable by the
// get/set CLI and are rejected with a clear message instead of a reflection
// error deeper in the stack.
func IsScalar(kind reflect.Kind) bool {
	switch kind {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

// ScalarLiteral renders raw as a TOML literal for a field of the given kind,
// so `config set management.port abcd` fails instead of writing a broken file.
func ScalarLiteral(kind reflect.Kind, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch kind {
	case reflect.String:
		return strconv.Quote(raw), nil
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return "", fmt.Errorf("expected true or false, got %q", raw)
		}
		return strconv.FormatBool(b), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return "", fmt.Errorf("expected an integer, got %q", raw)
		}
		return strconv.FormatInt(n, 10), nil
	default:
		return "", fmt.Errorf("unsupported value type %s (only string, bool and integer settings can be set)", kind)
	}
}

// ScalarString renders a resolved scalar for display.
func ScalarString(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}

// SetScalarInFile rewrites `key = literal` inside [section] of the TOML file at
// path, preserving comments and surrounding formatting. Missing keys are
// inserted right under the section header; missing sections are appended. The
// edited document must still parse, otherwise nothing is written.
func SetScalarInFile(path, dotted, literal string) error {
	section, key, err := splitDottedKey(dotted)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	lines := strings.Split(string(raw), "\n")
	header := "[" + section + "]"

	start := -1
	end := len(lines)
	if section != "" {
		start = -1
		for i, ln := range lines {
			if strings.TrimSpace(ln) == header {
				start = i
				break
			}
		}
		if start < 0 {
			if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
				lines = append(lines, "")
			}
			lines = append(lines, header, key+" = "+literal, "")
			return validateAndWrite(path, lines)
		}
		for i := start + 1; i < len(lines); i++ {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "[") {
				end = i
				break
			}
		}
	} else {
		// Top-level keys live before the first table header; start stays -1 so
		// the scan below begins at line 0 and insertion lands on line 0.
		for i, ln := range lines {
			if strings.HasPrefix(strings.TrimSpace(ln), "[") {
				end = i
				break
			}
		}
	}

	for i := start + 1; i < end; i++ {
		if keyName(lines[i]) == key {
			lines[i] = rewriteValue(lines[i], literal)
			return validateAndWrite(path, lines)
		}
	}

	// Key absent: insert it as the first entry of the section.
	inserted := append([]string{}, lines[:start+1]...)
	inserted = append(inserted, key+" = "+literal)
	inserted = append(inserted, lines[start+1:]...)
	return validateAndWrite(path, inserted)
}

func splitDottedKey(dotted string) (section, key string, err error) {
	path := strings.Split(strings.TrimSpace(dotted), ".")
	switch len(path) {
	case 1:
		return "", path[0], nil
	case 2:
		return path[0], path[1], nil
	default:
		return "", "", fmt.Errorf("key %q: only <key> and <table>.<key> are supported", dotted)
	}
}

// keyName reports the assignment target of a TOML line, or "" when the line is
// not a `key = value` pair.
func keyName(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return ""
	}
	eq := strings.Index(trimmed, "=")
	if eq < 0 {
		return ""
	}
	return strings.TrimSpace(trimmed[:eq])
}

// rewriteValue swaps the value of a `key = value` line while keeping the
// original indentation and trailing comment.
func rewriteValue(line, literal string) string {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	rest := line[eq+1:]
	comment := valueComment(rest)
	return indent + strings.TrimSpace(line[:eq]) + " = " + literal + comment
}

// valueComment returns the trailing comment (leading space included) of a TOML
// value, ignoring '#' inside a quoted string. Returns "" when there is none.
func valueComment(rest string) string {
	inString := false
	for i, r := range rest {
		switch {
		case r == '"':
			inString = !inString
		case r == '#' && !inString:
			// Keep the whitespace that separated the value from its comment.
			j := i
			for j > 0 && (rest[j-1] == ' ' || rest[j-1] == '\t') {
				j--
			}
			return rest[j:]
		}
	}
	return ""
}

func validateAndWrite(path string, lines []string) error {
	out := strings.Join(lines, "\n")
	var probe Config
	if _, err := toml.Decode(out, &probe); err != nil {
		return fmt.Errorf("edited config does not parse: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(out); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}
