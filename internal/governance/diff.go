package governance

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// FieldDiff captures the difference in a single property or attribute.
type FieldDiff struct {
	Field    string     `json:"field"`
	OldValue any        `json:"old_value"`
	NewValue any        `json:"new_value"`
	Action   ActionType `json:"action"`
}

// String returns a clean single-line representation of the field diff.
func (fd FieldDiff) String() string {
	switch fd.Action {
	case ActionCreate:
		return fmt.Sprintf("+ %s: %v", fd.Field, fd.NewValue)
	case ActionDelete:
		return fmt.Sprintf("- %s: %v", fd.Field, fd.OldValue)
	case ActionUpdate:
		return fmt.Sprintf("~ %s: %v -> %v", fd.Field, fd.OldValue, fd.NewValue)
	case ActionAudit:
		if fd.OldValue != nil && fd.NewValue != nil {
			return fmt.Sprintf("! %s: %v -> %v", fd.Field, fd.OldValue, fd.NewValue)
		}
		return fmt.Sprintf("! %s: %v", fd.Field, fd.OldValue)
	case ActionNoop:
		return fmt.Sprintf("  %s: %v", fd.Field, fd.OldValue)
	default:
		return fmt.Sprintf("? %s: %v -> %v", fd.Field, fd.OldValue, fd.NewValue)
	}
}

// ColoredString returns an ANSI color-coded line for CLI terminal rendering.
func (fd FieldDiff) ColoredString() string {
	switch fd.Action {
	case ActionCreate:
		return fmt.Sprintf("\033[32m+ %s: %v\033[0m", fd.Field, fd.NewValue)
	case ActionDelete:
		return fmt.Sprintf("\033[31m- %s: %v\033[0m", fd.Field, fd.OldValue)
	case ActionUpdate:
		return fmt.Sprintf("\033[33m~ %s: %v -> %v\033[0m", fd.Field, fd.OldValue, fd.NewValue)
	case ActionAudit:
		return fmt.Sprintf("\033[35m! %s: %v -> %v\033[0m", fd.Field, fd.OldValue, fd.NewValue)
	default:
		return fmt.Sprintf("  %s: %v", fd.Field, fd.OldValue)
	}
}

// Diff represents a collection of attribute-level changes for a specific resource entity.
type Diff struct {
	Resource string      `json:"resource"`
	Action   ActionType  `json:"action"`
	Fields   []FieldDiff `json:"fields,omitempty"`
	Details  string      `json:"details,omitempty"`
}

// HasChanges returns true if the diff contains actionable field modifications or audit findings.
func (d Diff) HasChanges() bool {
	return d.Action != ActionNoop && d.Action != ActionSkipped && (len(d.Fields) > 0 || d.Details != "")
}

// String formats the resource diff with indented field diff lines.
func (d Diff) String() string {
	if len(d.Fields) == 0 {
		if d.Details != "" {
			return fmt.Sprintf("[%s] %s: %s", d.Action, d.Resource, d.Details)
		}
		return fmt.Sprintf("[%s] %s (no field changes)", d.Action, d.Resource)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] %s", d.Action, d.Resource))
	if d.Details != "" {
		sb.WriteString(fmt.Sprintf(" (%s)", d.Details))
	}
	sb.WriteString(":\n")
	for _, f := range d.Fields {
		sb.WriteString(fmt.Sprintf("    %s\n", f.String()))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ColoredString formats the resource diff with ANSI colored output.
func (d Diff) ColoredString() string {
	if len(d.Fields) == 0 {
		if d.Details != "" {
			return fmt.Sprintf("\033[1m[%s] %s:\033[0m %s", d.Action, d.Resource, d.Details)
		}
		return fmt.Sprintf("[%s] %s", d.Action, d.Resource)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("\033[1m[%s] %s:\033[0m\n", d.Action, d.Resource))
	for _, f := range d.Fields {
		sb.WriteString(fmt.Sprintf("    %s\n", f.ColoredString()))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// DiffBuilder is a fluent helper for constructing structured Diff instances.
type DiffBuilder struct {
	fields  []FieldDiff
	details string
}

// NewDiffBuilder initializes an empty DiffBuilder.
func NewDiffBuilder() *DiffBuilder {
	return &DiffBuilder{fields: make([]FieldDiff, 0)}
}

// Add appends a non-nil FieldDiff to the builder.
func (b *DiffBuilder) Add(fd *FieldDiff) *DiffBuilder {
	if fd != nil {
		b.fields = append(b.fields, *fd)
	}
	return b
}

// AddField directly creates and appends a FieldDiff.
func (b *DiffBuilder) AddField(field string, oldVal, newVal any, action ActionType) *DiffBuilder {
	b.fields = append(b.fields, FieldDiff{
		Field:    field,
		OldValue: oldVal,
		NewValue: newVal,
		Action:   action,
	})
	return b
}

// SetDetails sets optional descriptive context for the diff.
func (b *DiffBuilder) SetDetails(details string) *DiffBuilder {
	b.details = details
	return b
}

// Fields returns all accumulated field diffs.
func (b *DiffBuilder) Fields() []FieldDiff {
	return b.fields
}

// HasChanges returns true if any field diffs or details exist.
func (b *DiffBuilder) HasChanges() bool {
	return len(b.fields) > 0 || b.details != ""
}

// Build constructs the final Diff object.
func (b *DiffBuilder) Build(resource string, defaultAction ActionType) Diff {
	action := defaultAction
	if len(b.fields) == 0 && b.details == "" {
		action = ActionNoop
	}
	return Diff{
		Resource: resource,
		Action:   action,
		Fields:   b.fields,
		Details:  b.details,
	}
}

// ----------------------------------------------------------------------------
// Generic Field Comparison Helpers
// ----------------------------------------------------------------------------

// CompareString evaluates differences between string values.
func CompareString(field, live, desired string) *FieldDiff {
	if desired == "" || live == desired {
		return nil
	}
	return &FieldDiff{
		Field:    field,
		OldValue: live,
		NewValue: desired,
		Action:   ActionUpdate,
	}
}

// CompareStringPtr evaluates differences when desired is an optional *string.
func CompareStringPtr(field string, live string, desired *string) *FieldDiff {
	if desired == nil {
		return nil
	}
	if live == *desired {
		return nil
	}
	return &FieldDiff{
		Field:    field,
		OldValue: live,
		NewValue: *desired,
		Action:   ActionUpdate,
	}
}

// CompareBoolPtr evaluates differences when desired is an optional *bool.
func CompareBoolPtr(field string, live bool, desired *bool) *FieldDiff {
	if desired == nil {
		return nil
	}
	if live == *desired {
		return nil
	}
	return &FieldDiff{
		Field:    field,
		OldValue: live,
		NewValue: *desired,
		Action:   ActionUpdate,
	}
}

// CompareIntPtr evaluates differences when desired is an optional *int.
func CompareIntPtr(field string, live int, desired *int) *FieldDiff {
	if desired == nil {
		return nil
	}
	if live == *desired {
		return nil
	}
	return &FieldDiff{
		Field:    field,
		OldValue: live,
		NewValue: *desired,
		Action:   ActionUpdate,
	}
}

// CompareStringSlice evaluates order-independent differences between string slices.
func CompareStringSlice(field string, live, desired []string) *FieldDiff {
	if desired == nil {
		return nil
	}
	sLive := make([]string, len(live))
	copy(sLive, live)
	sort.Strings(sLive)

	sDesired := make([]string, len(desired))
	copy(sDesired, desired)
	sort.Strings(sDesired)

	if reflect.DeepEqual(sLive, sDesired) {
		return nil
	}

	return &FieldDiff{
		Field:    field,
		OldValue: live,
		NewValue: desired,
		Action:   ActionUpdate,
	}
}
