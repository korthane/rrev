package config

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// ParseError reports a configuration file rrev refused to parse, naming the
// file and the offending line so the user can fix it instead of silently
// running on defaults.
type ParseError struct {
	File string
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
}

// entry is one setting as it was written in a source.
type entry struct {
	raw  string
	line int
}

// parseINI reads INI-style `key = value` lines. Inline comments are not
// supported so that an unquoted `#` in a value (a hex colour, say) survives.
func parseINI(file string, r io.Reader) (map[string]entry, error) {
	values := make(map[string]entry)
	scanner := bufio.NewScanner(r)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			return nil, &ParseError{File: file, Line: lineNo, Msg: "section headers are not supported; use flat `key = value` lines"}
		}
		key, raw, found := strings.Cut(line, "=")
		if !found {
			return nil, &ParseError{File: file, Line: lineNo, Msg: fmt.Sprintf("expected `key = value`, got %q", line)}
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, &ParseError{File: file, Line: lineNo, Msg: "empty setting name"}
		}
		if _, known := fieldByKey[key]; !known {
			return nil, &ParseError{File: file, Line: lineNo, Msg: fmt.Sprintf("unknown setting %q", key)}
		}
		values[key] = entry{raw: strings.TrimSpace(raw), line: lineNo}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	return values, nil
}
