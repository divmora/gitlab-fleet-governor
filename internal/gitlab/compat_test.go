package gitlab

import (
	"testing"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

func TestProjectDeletionScheduledAt(t *testing.T) {
	on := gitlab.ISOTime(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	at := gitlab.ISOTime(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))

	tests := []struct {
		name    string
		project *gitlab.Project
		want    *gitlab.ISOTime
	}{
		{name: "nil project", project: nil, want: nil},
		{name: "not pending deletion", project: &gitlab.Project{}, want: nil},
		{name: "current field", project: &gitlab.Project{MarkedForDeletionOn: &on}, want: &on},
		{
			name:    "legacy field from older GitLab",
			project: &gitlab.Project{MarkedForDeletionAt: &at}, //nolint:staticcheck // SA1019: exercises the legacy field.
			want:    &at,
		},
		{
			name:    "current field wins over legacy",
			project: &gitlab.Project{MarkedForDeletionOn: &on, MarkedForDeletionAt: &at}, //nolint:staticcheck // SA1019: exercises the legacy field.
			want:    &on,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProjectDeletionScheduledAt(tt.project)
			if (got == nil) != (tt.want == nil) || (got != nil && !time.Time(*got).Equal(time.Time(*tt.want))) {
				t.Fatalf("ProjectDeletionScheduledAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProjectLegacyTagList(t *testing.T) {
	if got := ProjectLegacyTagList(nil); got != nil {
		t.Fatalf("nil project: got %v, want nil", got)
	}
	p := &gitlab.Project{TagList: []string{"legacy"}} //nolint:staticcheck // SA1019: exercises the legacy field.
	if got := ProjectLegacyTagList(p); len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("got %v, want [legacy]", got)
	}
}
