// Command gen-eventschema generates the normalized session event model from
// schema/events.json (the source of truth). It emits:
//
//   - internal/session/eventtypes.gen.go — the EventType consts + AllEventTypes
//   - runner/src/events.gen.ts           — the EventType union, ALL_EVENT_TYPES,
//     and the payload interfaces
//
// Go payload structs (internal/session/event.go) are hand-written but validated
// against the schema by internal/session/schema_test.go. Run via `just gen`
// (from the repo root); never hand-edit a *.gen.* file.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
)

// schema mirrors the shape of schema/events.json.
type schema struct {
	TypeVocabulary map[string]struct {
		Go string `json:"go"`
		TS string `json:"ts"`
	} `json:"typeVocabulary"`
	EventTypes []string                 `json:"eventTypes"`
	Objects    map[string]payloadSchema `json:"objects"`
	Payloads   map[string]payloadSchema `json:"payloads"`
}

type payloadSchema struct {
	Doc    string        `json:"doc"`
	Fields []fieldSchema `json:"fields"`
}

type fieldSchema struct {
	JSON     string `json:"json"`
	Type     string `json:"type"`
	Optional bool   `json:"optional"`
	Doc      string `json:"doc"`
	// Items names an entry in the schema's "objects" section when Type is
	// "objectArray"; the field is then a slice of that object type.
	Items string `json:"items"`
}

// objectArrayType is the field type for a slice of a named nested object
// (declared in the schema's "objects" section via the field's "items" key).
const objectArrayType = "objectArray"

// tsFieldType returns the TypeScript type for a field, resolving objectArray
// to "<Item>[]" and everything else through the type vocabulary.
func (s schema) tsFieldType(f fieldSchema) string {
	if f.Type == objectArrayType {
		return f.Items + "[]"
	}
	return s.TypeVocabulary[f.Type].TS
}

// objectOrder is the order nested-object interfaces are emitted in the TS
// output (before the payload interfaces that reference them). Kept explicit so
// generated output is stable.
var objectOrder = []string{
	"TodoItem",
}

// payloadOrder is the order payload interfaces are emitted in the TS output.
// Kept explicit (rather than map iteration) so generated output is stable.
var payloadOrder = []string{
	"SessionStartedPayload",
	"SessionStatusPayload",
	"TerminatingPayload",
	"MessagePayload",
	"ToolPayload",
	"PermissionPayload",
	"UsagePayload",
	"RateLimitPayload",
	"WorkspaceStatusPayload",
	"SessionTitlePayload",
	"TodoUpdatedPayload",
	"ErrorPayload",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen-eventschema:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(root, "schema", "events.json"))
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	var s schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}
	if err := s.validate(); err != nil {
		return err
	}

	goSrc, err := s.genGo()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "session", "eventtypes.gen.go"), goSrc, 0o644); err != nil {
		return err
	}

	tsSrc := s.genTS()
	if err := os.WriteFile(filepath.Join(root, "runner", "src", "events.gen.ts"), tsSrc, 0o644); err != nil {
		return err
	}

	fmt.Println("gen-eventschema: wrote internal/session/eventtypes.gen.go and runner/src/events.gen.ts")
	return nil
}

// repoRoot walks up from CWD looking for go.mod so the generator works whether
// invoked from the repo root (just gen) or a subdirectory.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod above %s", dir)
		}
		dir = parent
	}
}

func (s schema) validate() error {
	if len(s.EventTypes) == 0 {
		return fmt.Errorf("schema has no eventTypes")
	}
	seen := map[string]bool{}
	for _, et := range s.EventTypes {
		if seen[et] {
			return fmt.Errorf("duplicate event type %q", et)
		}
		seen[et] = true
	}
	if err := s.validateFields("payload", s.Payloads); err != nil {
		return err
	}
	if err := s.validateFields("object", s.Objects); err != nil {
		return err
	}
	// Every object must be in objectOrder so TS output is deterministic.
	inObjOrder := map[string]bool{}
	for _, n := range objectOrder {
		inObjOrder[n] = true
		if _, ok := s.Objects[n]; !ok {
			return fmt.Errorf("objectOrder lists %s but schema has no such object", n)
		}
	}
	for n := range s.Objects {
		if !inObjOrder[n] {
			return fmt.Errorf("object %s is not in cmd/gen-eventschema objectOrder (add it)", n)
		}
	}
	// Every payload must be in payloadOrder so TS output is deterministic.
	inOrder := map[string]bool{}
	for _, n := range payloadOrder {
		inOrder[n] = true
		if _, ok := s.Payloads[n]; !ok {
			return fmt.Errorf("payloadOrder lists %s but schema has no such payload", n)
		}
	}
	for n := range s.Payloads {
		if !inOrder[n] {
			return fmt.Errorf("payload %s is not in cmd/gen-eventschema payloadOrder (add it)", n)
		}
	}
	return nil
}

// validateFields checks that every field of every entry in defs uses either a
// known vocabulary type or "objectArray" with an "items" key naming a defined
// object. kind is "payload" or "object" for error messages.
func (s schema) validateFields(kind string, defs map[string]payloadSchema) error {
	for name, p := range defs {
		for _, f := range p.Fields {
			if f.Type == objectArrayType {
				if f.Items == "" {
					return fmt.Errorf("%s %s field %s: objectArray requires an \"items\" object name", kind, name, f.JSON)
				}
				if _, ok := s.Objects[f.Items]; !ok {
					return fmt.Errorf("%s %s field %s: objectArray items %q is not a defined object", kind, name, f.JSON, f.Items)
				}
				continue
			}
			if _, ok := s.TypeVocabulary[f.Type]; !ok {
				return fmt.Errorf("%s %s field %s: unknown type %q", kind, name, f.JSON, f.Type)
			}
		}
	}
	return nil
}

// constName maps an event-type string to its Go const name, e.g.
// "session.status_changed" -> "EventSessionStatusChanged".
func constName(eventType string) string {
	segs := strings.FieldsFunc(eventType, func(r rune) bool { return r == '.' || r == '_' })
	var b strings.Builder
	b.WriteString("Event")
	for _, seg := range segs {
		if seg == "" {
			continue
		}
		b.WriteString(strings.ToUpper(seg[:1]))
		b.WriteString(seg[1:])
	}
	return b.String()
}

const genHeader = "// Code generated by cmd/gen-eventschema; DO NOT EDIT.\n" +
	"// Source of truth: schema/events.json. Run `just gen` to regenerate.\n"

func (s schema) genGo() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(genHeader)
	b.WriteString("\npackage session\n\n")
	b.WriteString("// EventType consts. EventType itself is declared in event.go.\n")
	b.WriteString("const (\n")
	for _, et := range s.EventTypes {
		fmt.Fprintf(&b, "\t%s EventType = %q\n", constName(et), et)
	}
	b.WriteString(")\n\n")
	b.WriteString("// AllEventTypes is every event type, in schema order. The drift test in\n")
	b.WriteString("// schema_test.go asserts this matches schema/events.json exactly.\n")
	b.WriteString("var AllEventTypes = []EventType{\n")
	for _, et := range s.EventTypes {
		fmt.Fprintf(&b, "\t%s,\n", constName(et))
	}
	b.WriteString("}\n")

	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt generated Go: %w\n%s", err, b.String())
	}
	return formatted, nil
}

func (s schema) genTS() []byte {
	var b bytes.Buffer
	b.WriteString(genHeader)
	b.WriteString("\n/** Canonical event type enum. */\n")
	b.WriteString("export type EventType =\n")
	for i, et := range s.EventTypes {
		end := ";"
		if i < len(s.EventTypes)-1 {
			end = ""
		}
		fmt.Fprintf(&b, "  | '%s'%s\n", et, end)
	}
	b.WriteString("\n/** Every event type, in schema order. */\n")
	b.WriteString("export const ALL_EVENT_TYPES: EventType[] = [\n")
	for _, et := range s.EventTypes {
		fmt.Fprintf(&b, "  '%s',\n", et)
	}
	b.WriteString("];\n")

	// Nested object interfaces are emitted before the payloads that reference
	// them so the generated file type-checks top-to-bottom.
	for _, name := range objectOrder {
		s.writeTSInterface(&b, name, s.Objects[name])
	}
	for _, name := range payloadOrder {
		s.writeTSInterface(&b, name, s.Payloads[name])
	}
	return b.Bytes()
}

// writeTSInterface emits a single `export interface` block for an object or
// payload definition.
func (s schema) writeTSInterface(b *bytes.Buffer, name string, p payloadSchema) {
	b.WriteString("\n")
	if p.Doc != "" {
		fmt.Fprintf(b, "/** %s */\n", p.Doc)
	}
	fmt.Fprintf(b, "export interface %s {\n", name)
	for _, f := range p.Fields {
		if f.Doc != "" {
			fmt.Fprintf(b, "  /** %s */\n", f.Doc)
		}
		opt := ""
		if f.Optional {
			opt = "?"
		}
		fmt.Fprintf(b, "  %s%s: %s;\n", f.JSON, opt, s.tsFieldType(f))
	}
	b.WriteString("}\n")
}
