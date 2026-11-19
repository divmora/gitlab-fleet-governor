package governance

import "testing"

func TestAutoMergeAcceptOptions_SendsCurrentAndLegacyParams(t *testing.T) {
	for _, wait := range []bool{true, false} {
		opts := autoMergeAcceptOptions(wait)
		if opts.AutoMerge == nil || *opts.AutoMerge != wait {
			t.Errorf("wait=%v: auto_merge = %v, want %v", wait, opts.AutoMerge, wait)
		}
		legacy := opts.MergeWhenPipelineSucceeds //nolint:staticcheck // SA1019: verifies the legacy param for older GitLab.
		if legacy == nil || *legacy != wait {
			t.Errorf("wait=%v: merge_when_pipeline_succeeds = %v, want %v", wait, legacy, wait)
		}
	}
}
