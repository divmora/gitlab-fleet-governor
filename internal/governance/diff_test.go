package governance_test

import (
	"testing"

	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/stretchr/testify/assert"
)

func TestFieldDiff_Formatting(t *testing.T) {
	t.Run("Create", func(t *testing.T) {
		fd := governance.FieldDiff{
			Field:    "branch_name_regex",
			OldValue: nil,
			NewValue: "^main$",
			Action:   governance.ActionCreate,
		}
		assert.Equal(t, "+ branch_name_regex: ^main$", fd.String())
		assert.Contains(t, fd.ColoredString(), "+ branch_name_regex: ^main$")
	})

	t.Run("Delete", func(t *testing.T) {
		fd := governance.FieldDiff{
			Field:    "unmanaged_key",
			OldValue: "old-val",
			NewValue: nil,
			Action:   governance.ActionDelete,
		}
		assert.Equal(t, "- unmanaged_key: old-val", fd.String())
		assert.Contains(t, fd.ColoredString(), "- unmanaged_key: old-val")
	})

	t.Run("Update", func(t *testing.T) {
		fd := governance.FieldDiff{
			Field:    "max_file_size",
			OldValue: 10,
			NewValue: 20,
			Action:   governance.ActionUpdate,
		}
		assert.Equal(t, "~ max_file_size: 10 -> 20", fd.String())
		assert.Contains(t, fd.ColoredString(), "~ max_file_size: 10 -> 20")
	})

	t.Run("Audit", func(t *testing.T) {
		fd := governance.FieldDiff{
			Field:    "access_level",
			OldValue: 50,
			NewValue: 40,
			Action:   governance.ActionAudit,
		}
		assert.Equal(t, "! access_level: 50 -> 40", fd.String())
		assert.Contains(t, fd.ColoredString(), "! access_level: 50 -> 40")

		fdSingle := governance.FieldDiff{
			Field:    "denied_member",
			OldValue: "evil_user",
			Action:   governance.ActionAudit,
		}
		assert.Equal(t, "! denied_member: evil_user", fdSingle.String())
	})

	t.Run("Noop", func(t *testing.T) {
		fd := governance.FieldDiff{
			Field:    "name",
			OldValue: "main",
			Action:   governance.ActionNoop,
		}
		assert.Equal(t, "  name: main", fd.String())
		assert.Equal(t, "  name: main", fd.ColoredString())
	})

	t.Run("UnknownAction", func(t *testing.T) {
		fd := governance.FieldDiff{
			Field:    "test",
			OldValue: "a",
			NewValue: "b",
			Action:   governance.ActionType("UNKNOWN"),
		}
		assert.Equal(t, "? test: a -> b", fd.String())
	})
}

func TestDiff_HasChanges_And_Formatting(t *testing.T) {
	t.Run("EmptyDiffNoop", func(t *testing.T) {
		d := governance.Diff{
			Resource: "push_rule",
			Action:   governance.ActionNoop,
		}
		assert.False(t, d.HasChanges())
		assert.Equal(t, "[NOOP] push_rule (no field changes)", d.String())
		assert.Equal(t, "[NOOP] push_rule", d.ColoredString())
	})

	t.Run("DiffWithDetailsOnly", func(t *testing.T) {
		d := governance.Diff{
			Resource: "runner:1",
			Action:   governance.ActionCreate,
			Details:  "Runner assertion defined in policy",
		}
		assert.True(t, d.HasChanges())
		assert.Equal(t, "[CREATE] runner:1: Runner assertion defined in policy", d.String())
		assert.Contains(t, d.ColoredString(), "Runner assertion defined in policy")
	})

	t.Run("DiffWithFields", func(t *testing.T) {
		builder := governance.NewDiffBuilder()
		builder.AddField("author_email_regex", nil, "@corp\\.com$", governance.ActionCreate)
		builder.AddField("prevent_secrets", false, true, governance.ActionUpdate)
		builder.SetDetails("Rule update")
		d := builder.Build("push_rule", governance.ActionUpdate)

		assert.True(t, d.HasChanges())
		assert.Len(t, d.Fields, 2)
		assert.Contains(t, d.String(), "[UPDATE] push_rule (Rule update):")
		assert.Contains(t, d.String(), "+ author_email_regex: @corp\\.com$")
		assert.Contains(t, d.String(), "~ prevent_secrets: false -> true")
		assert.Contains(t, d.ColoredString(), "\033[1m[UPDATE] push_rule:\033[0m")
	})
}

func TestDiffBuilder_Helpers(t *testing.T) {
	builder := governance.NewDiffBuilder()
	assert.False(t, builder.HasChanges())
	assert.Empty(t, builder.Fields())

	fd := governance.CompareString("branch", "feat", "main")
	builder.Add(fd)
	builder.Add(nil) // gracefully ignore nil

	assert.True(t, builder.HasChanges())
	assert.Len(t, builder.Fields(), 1)

	d := builder.Build("branch_diff", governance.ActionUpdate)
	assert.Equal(t, governance.ActionUpdate, d.Action)
	assert.Equal(t, "branch_diff", d.Resource)

	emptyBuilder := governance.NewDiffBuilder()
	emptyDiff := emptyBuilder.Build("empty", governance.ActionUpdate)
	assert.Equal(t, governance.ActionNoop, emptyDiff.Action)
}

func TestComparisonHelpers(t *testing.T) {
	t.Run("CompareString", func(t *testing.T) {
		assert.Nil(t, governance.CompareString("f", "main", ""))
		assert.Nil(t, governance.CompareString("f", "main", "main"))
		diff := governance.CompareString("f", "old", "new")
		assert.NotNil(t, diff)
		assert.Equal(t, "old", diff.OldValue)
		assert.Equal(t, "new", diff.NewValue)
	})

	t.Run("CompareStringPtr", func(t *testing.T) {
		assert.Nil(t, governance.CompareStringPtr("f", "val", nil))
		desired := "val"
		assert.Nil(t, governance.CompareStringPtr("f", "val", &desired))
		desiredDiff := "new"
		diff := governance.CompareStringPtr("f", "val", &desiredDiff)
		assert.NotNil(t, diff)
		assert.Equal(t, "val", diff.OldValue)
		assert.Equal(t, "new", diff.NewValue)
	})

	t.Run("CompareBoolPtr", func(t *testing.T) {
		assert.Nil(t, governance.CompareBoolPtr("f", true, nil))
		desiredTrue := true
		assert.Nil(t, governance.CompareBoolPtr("f", true, &desiredTrue))
		desiredFalse := false
		diff := governance.CompareBoolPtr("f", true, &desiredFalse)
		assert.NotNil(t, diff)
		assert.Equal(t, true, diff.OldValue)
		assert.Equal(t, false, diff.NewValue)
	})

	t.Run("CompareIntPtr", func(t *testing.T) {
		assert.Nil(t, governance.CompareIntPtr("f", 10, nil))
		desired10 := 10
		assert.Nil(t, governance.CompareIntPtr("f", 10, &desired10))
		desired20 := 20
		diff := governance.CompareIntPtr("f", 10, &desired20)
		assert.NotNil(t, diff)
		assert.Equal(t, 10, diff.OldValue)
		assert.Equal(t, 20, diff.NewValue)
	})

	t.Run("CompareStringSlice", func(t *testing.T) {
		assert.Nil(t, governance.CompareStringSlice("f", []string{"a"}, nil))
		assert.Nil(t, governance.CompareStringSlice("f", []string{"b", "a"}, []string{"a", "b"}))
		diff := governance.CompareStringSlice("f", []string{"a"}, []string{"a", "b"})
		assert.NotNil(t, diff)
		assert.Equal(t, []string{"a"}, diff.OldValue)
		assert.Equal(t, []string{"a", "b"}, diff.NewValue)
	})
}
