package gitlab

import (
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// This file centralizes reads of GitLab API fields that client-go marks as
// deprecated but that older self-managed GitLab versions still return in place
// of their replacements. Keeping the legacy reads here means the rest of the
// codebase uses only the current fields, and the fallbacks can be dropped in
// one place once the oldest supported GitLab version no longer needs them.

// ProjectDeletionScheduledAt returns the date GitLab scheduled the project for
// deletion, or nil when the project is not pending deletion.
//
// It prefers marked_for_deletion_on and falls back to the legacy
// marked_for_deletion_at that older GitLab versions return instead.
func ProjectDeletionScheduledAt(p *gitlab.Project) *gitlab.ISOTime {
	if p == nil {
		return nil
	}
	if p.MarkedForDeletionOn != nil {
		return p.MarkedForDeletionOn
	}
	return p.MarkedForDeletionAt //nolint:staticcheck // SA1019: legacy field still returned by older GitLab versions.
}

// ProjectLegacyTagList returns the project's legacy tag_list, which GitLab
// versions that predate topics return instead of the topics field.
func ProjectLegacyTagList(p *gitlab.Project) []string {
	if p == nil {
		return nil
	}
	return p.TagList //nolint:staticcheck // SA1019: legacy field still returned by older GitLab versions.
}
