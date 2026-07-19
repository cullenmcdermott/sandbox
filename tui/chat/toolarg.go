package chat

// toolarg.go — helpers for deriving a ToolCall's headline argument and result
// summary from raw tool I/O. A host reducer uses these to populate ToolCall.Arg
// and ToolCall.Summary without re-implementing the extraction. They are pure and
// protocol-neutral (they key off common tool names but degrade to a best-effort
// field scan for anything else).

import (
	"encoding/json"
	"strings"
)

// ToolArg extracts the most informative single argument from a tool's JSON input
// for the card label: a file path, command, pattern, or url. Paths are shortened
// to their last two segments. Returns "" when nothing informative is present.
func ToolArg(tool string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	get := func(keys ...string) string {
		var raw map[string]any
		if json.Unmarshal(input, &raw) != nil {
			return ""
		}
		for _, k := range keys {
			if v, ok := raw[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch tool {
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		if p := get("file_path", "notebook_path", "path"); p != "" {
			return shortenPath(p)
		}
	case "Bash":
		return collapseSpaces(get("command"))
	case "Grep", "Glob":
		return get("pattern")
	case "WebFetch":
		return get("url")
	case "WebSearch":
		return get("query")
	}
	if p := get("file_path", "path", "command", "pattern", "url", "query"); p != "" {
		return collapseSpaces(p)
	}
	return ""
}

// ToolSummary condenses a tool's output into a short result note: a line count
// for multi-line output ("42 lines"), else the collapsed first line.
func ToolSummary(output string) string {
	if output == "" {
		return ""
	}
	n := strings.Count(output, "\n")
	if strings.TrimRight(output, "\n") != output {
		n--
	}
	if n >= 1 {
		return formatInt(n+1) + " lines"
	}
	return collapseSpaces(firstLine(output))
}
