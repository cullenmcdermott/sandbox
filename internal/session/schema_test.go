package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// schema_test.go is the drift gate for the event-model contract. It asserts
// that the hand-written Go payload structs in event.go match schema/events.json
// (the source of truth) field-for-field, and that the generated AllEventTypes
// matches the schema's eventTypes list. A one-sided change to either the schema
// or a Go struct fails here. The TS side is enforced separately by the `just
// gen` diff gate in CI. See docs/architecture.md.

type schemaPayload struct {
	Fields []struct {
		JSON     string `json:"json"`
		Type     string `json:"type"`
		Optional bool   `json:"optional"`
		Items    string `json:"items"`
	} `json:"fields"`
}

type schemaFile struct {
	TypeVocabulary map[string]struct {
		Go string `json:"go"`
		TS string `json:"ts"`
	} `json:"typeVocabulary"`
	EventTypes []string                 `json:"eventTypes"`
	Objects    map[string]schemaPayload `json:"objects"`
	Payloads   map[string]schemaPayload `json:"payloads"`
}

// payloadRegistry maps each schema payload name to its hand-written Go struct.
// When you add a new event payload, add it here AND to schema/events.json; the
// set-equality check below fails if the two disagree.
func payloadRegistry() map[string]reflect.Type {
	return map[string]reflect.Type{
		"SessionStartedPayload":  reflect.TypeOf(SessionStartedPayload{}),
		"SessionStatusPayload":   reflect.TypeOf(SessionStatusPayload{}),
		"TerminatingPayload":     reflect.TypeOf(TerminatingPayload{}),
		"MessagePayload":         reflect.TypeOf(MessagePayload{}),
		"ToolPayload":            reflect.TypeOf(ToolPayload{}),
		"PermissionPayload":      reflect.TypeOf(PermissionPayload{}),
		"UsagePayload":           reflect.TypeOf(UsagePayload{}),
		"RateLimitPayload":       reflect.TypeOf(RateLimitPayload{}),
		"WorkspaceStatusPayload": reflect.TypeOf(WorkspaceStatusPayload{}),
		"SessionTitlePayload":    reflect.TypeOf(SessionTitlePayload{}),
		"ModelsAvailablePayload": reflect.TypeOf(ModelsAvailablePayload{}),
		"TodoUpdatedPayload":     reflect.TypeOf(TodoUpdatedPayload{}),
		"ErrorPayload":           reflect.TypeOf(ErrorPayload{}),
	}
}

// objectRegistry maps each schema nested-object name (the "objects" section) to
// its hand-written Go struct. objectArray payload fields reference these.
func objectRegistry() map[string]reflect.Type {
	return map[string]reflect.Type{
		"TodoItem":  reflect.TypeOf(TodoItem{}),
		"ModelInfo": reflect.TypeOf(ModelInfo{}),
	}
}

func loadSchema(t *testing.T) schemaFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schema", "events.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var s schemaFile
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return s
}

// goCategory reduces a Go field type to the schema's type vocabulary. The
// json.RawMessage check must precede the slice case because RawMessage is a
// []byte under the hood. For a slice of structs it returns the "objectArray"
// category plus the element struct's name (e.g. "TodoItem"); elem is empty for
// every other category.
func goCategory(t reflect.Type) (category, elem string, ok bool) {
	if t == reflect.TypeOf(json.RawMessage(nil)) {
		return "raw", "", true
	}
	switch t.Kind() {
	case reflect.String:
		return "string", "", true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int", "", true
	case reflect.Float32, reflect.Float64:
		return "float", "", true
	case reflect.Bool:
		return "bool", "", true
	case reflect.Slice:
		switch t.Elem().Kind() {
		case reflect.String:
			return "stringArray", "", true
		case reflect.Struct:
			return objectArrayCategory, t.Elem().Name(), true
		}
	case reflect.Pointer:
		switch t.Elem().Kind() {
		case reflect.Int:
			return "intPtr", "", true
		case reflect.Float32, reflect.Float64:
			return "floatPtr", "", true
		}
	}
	return "", "", false
}

// objectArrayCategory is the schema type for a slice of a named nested object.
const objectArrayCategory = "objectArray"

type fieldInfo struct {
	category string
	optional bool
	// elem is the element struct name when category == objectArrayCategory.
	elem string
}

// structFields extracts the json-tagged fields of a struct as a map keyed by
// json name, recording the coarse type category and whether ,omitempty is set.
func structFields(t reflect.Type) (map[string]fieldInfo, error) {
	out := map[string]fieldInfo{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		if name == "" {
			continue
		}
		optional := false
		for _, p := range parts[1:] {
			if p == "omitempty" {
				optional = true
			}
		}
		cat, elem, ok := goCategory(f.Type)
		if !ok {
			return nil, &unsupportedTypeError{field: name, typ: f.Type.String()}
		}
		out[name] = fieldInfo{category: cat, optional: optional, elem: elem}
	}
	return out, nil
}

type unsupportedTypeError struct {
	field string
	typ   string
}

func (e *unsupportedTypeError) Error() string {
	return "field " + e.field + ": unsupported Go type " + e.typ +
		" (add it to the schema type vocabulary and goCategory)"
}

func TestEventTypesMatchSchema(t *testing.T) {
	s := loadSchema(t)

	got := make([]string, len(AllEventTypes))
	for i, et := range AllEventTypes {
		got[i] = string(et)
	}
	if !reflect.DeepEqual(got, s.EventTypes) {
		t.Errorf("AllEventTypes (eventtypes.gen.go) != schema eventTypes.\n got: %v\nwant: %v\n(run `just gen` after editing schema/events.json)", got, s.EventTypes)
	}

	seen := map[string]bool{}
	for _, et := range s.EventTypes {
		if seen[et] {
			t.Errorf("duplicate event type in schema: %q", et)
		}
		seen[et] = true
	}
}

func TestPayloadStructsMatchSchema(t *testing.T) {
	s := loadSchema(t)
	compareSet(t, "payload", "payloadRegistry", s.Payloads, payloadRegistry())
	compareSet(t, "object", "objectRegistry", s.Objects, objectRegistry())
}

// compareSet asserts set equality between a schema section (payloads or
// objects) and its Go struct registry, then field-for-field matches each pair.
func compareSet(t *testing.T, kind, registryName string, defs map[string]schemaPayload, registry map[string]reflect.Type) {
	t.Helper()
	for name := range defs {
		if _, ok := registry[name]; !ok {
			t.Errorf("schema %s %q has no Go struct in %s", kind, name, registryName)
		}
	}
	for name := range registry {
		if _, ok := defs[name]; !ok {
			t.Errorf("Go struct %q is not a %s in schema/events.json", name, kind)
		}
	}
	for name, sp := range defs {
		rt, ok := registry[name]
		if !ok {
			continue
		}
		compareStruct(t, name, sp, rt)
	}
}

// compareStruct asserts a single Go struct matches its schema definition field
// for field (type category, omitempty, and the items element for objectArrays).
func compareStruct(t *testing.T, name string, sp schemaPayload, rt reflect.Type) {
	t.Helper()
	fields, err := structFields(rt)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return
	}

	schemaNames := map[string]bool{}
	for _, f := range sp.Fields {
		schemaNames[f.JSON] = true
		info, ok := fields[f.JSON]
		if !ok {
			t.Errorf("%s: schema field %q missing from Go struct", name, f.JSON)
			continue
		}
		if info.category != f.Type {
			t.Errorf("%s.%s: Go type category %q != schema type %q", name, f.JSON, info.category, f.Type)
		}
		if info.optional != f.Optional {
			t.Errorf("%s.%s: Go omitempty=%v != schema optional=%v", name, f.JSON, info.optional, f.Optional)
		}
		if f.Type == objectArrayCategory && info.elem != f.Items {
			t.Errorf("%s.%s: Go element type %q != schema items %q", name, f.JSON, info.elem, f.Items)
		}
	}
	var extra []string
	for jsonName := range fields {
		if !schemaNames[jsonName] {
			extra = append(extra, jsonName)
		}
	}
	sort.Strings(extra)
	for _, jsonName := range extra {
		t.Errorf("%s: Go field %q not in schema (run `just gen` and update schema/events.json)", name, jsonName)
	}
}
