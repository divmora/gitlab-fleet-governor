package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

type validateFlags struct {
	Quiet    bool
	JSON     bool
	NoEnvVar bool
}

// ValidateJSONOutput represents structured JSON validation output.
type ValidateJSONOutput struct {
	Status   string                     `json:"status"`
	Valid    bool                       `json:"valid"`
	Source   string                     `json:"source"`
	Targets  *TargetSummaryJSON         `json:"targets,omitempty"`
	Policies *PolicySummaryJSON         `json:"policies,omitempty"`
	Errors   []config.ValidationError   `json:"errors,omitempty"`
	Warnings []config.ValidationWarning `json:"warnings,omitempty"`
}

// TargetSummaryJSON summarizes the discovered target rules.
type TargetSummaryJSON struct {
	GroupIDsIncluded    int  `json:"group_ids_included"`
	GroupPathsIncluded  int  `json:"group_paths_included"`
	HasProjectSelectors bool `json:"has_project_selectors"`
}

// PolicySummaryJSON summarizes the configured policy modules.
type PolicySummaryJSON struct {
	PushRules             bool `json:"push_rules"`
	ProtectedBranches     int  `json:"protected_branches"`
	ProtectedEnvironments int  `json:"protected_environments"`
	ApprovalRules         int  `json:"approval_rules"`
	ProjectSettings       bool `json:"project_settings"`
	PipelineRetention     bool `json:"pipeline_retention"`
	Variables             int  `json:"variables"`
	Runners               int  `json:"runners"`
	Compliance            bool `json:"compliance"`
	Webhooks              int  `json:"webhooks"`
	Members               int  `json:"members"`
	TargetBranchRules     int  `json:"target_branch_rules"`
}

func newValidateCmd() *cobra.Command {
	var flags validateFlags

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate policy configuration syntax, schema, regular expressions, and enums",
		Long: `Validate performs comprehensive offline syntax and semantic validation of a
policy configuration file. It verifies YAML/JSON schema, RE2 regular expressions,
GitLab access levels, variable naming, webhook URLs, and enum constraints without
making remote API calls.`,
		Example: `  # Validate a local YAML configuration
  gitlab-fleet-governor validate -c examples/config.sample.yaml

  # Validate a JSON configuration quietly (exit code only)
  gitlab-fleet-governor validate -c examples/config.sample.json --quiet

  # Output validation report in structured JSON format
  gitlab-fleet-governor validate -c config.yaml --json

  # Validate config piped from stdin
  cat policy.yaml | gitlab-fleet-governor validate -c -`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeValidate(cmd, flags)
		},
	}

	cmd.Flags().BoolVarP(&flags.Quiet, "quiet", "q", false, "Suppress output on success; only report errors")
	cmd.Flags().BoolVar(&flags.JSON, "json", false, "Output validation result in JSON format")

	return cmd
}

func executeValidate(cmd *cobra.Command, flags validateFlags) error {
	ctx := cmd.Context()
	loader := config.NewLoader(config.WithStdin(cmd.InOrStdin()))

	rawBytes, sourceDesc, loadErr := loader.LoadRaw(ctx, globalFlags.ConfigPath)
	if loadErr != nil {
		return handleValidateError(cmd.OutOrStdout(), flags, sourceDesc, loadErr, nil)
	}

	expanded, expErr := config.ExpandEnv(string(rawBytes))
	if expErr != nil {
		return handleValidateError(cmd.OutOrStdout(), flags, sourceDesc, expErr, nil)
	}

	var cfg config.PolicyConfig
	if unmarshalErr := config.UnmarshalStrict([]byte(expanded), &cfg); unmarshalErr != nil {
		return handleValidateError(cmd.OutOrStdout(), flags, sourceDesc, unmarshalErr, nil)
	}

	cfg.SetDefaults()

	valErr, warnings := cfg.ValidateWithWarnings()
	if valErr != nil {
		return handleValidateError(cmd.OutOrStdout(), flags, sourceDesc, valErr, warnings)
	}

	// Validation Succeeded
	if flags.JSON {
		return outputJSONValidationResult(cmd.OutOrStdout(), sourceDesc, &cfg, warnings)
	}

	if !flags.Quiet {
		if len(warnings) > 0 {
			for _, w := range warnings {
				if globalFlags.NoColor {
					fmt.Fprintf(cmd.OutOrStdout(), "WARNING: %s: %s\n", w.Field, w.Message)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "\033[33m⚠ WARNING: %s: %s\033[0m\n", w.Field, w.Message)
				}
			}
		}
		if globalFlags.NoColor {
			fmt.Fprintf(cmd.OutOrStdout(), "SUCCESS: Configuration in '%s' is valid.\n", sourceDesc)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "\033[32m✔ Configuration in '%s' is valid and complies with all schema rules.\033[0m\n", sourceDesc)
		}
	}

	return nil
}

func outputJSONValidationResult(out io.Writer, sourceDesc string, cfg *config.PolicyConfig, warnings []config.ValidationWarning) error {
	targetSummary := &TargetSummaryJSON{
		HasProjectSelectors: cfg.Targets.ProjectSelector != nil,
	}
	if cfg.Targets.GroupSelector != nil {
		targetSummary.GroupIDsIncluded = len(cfg.Targets.GroupSelector.GroupIDsInclude)
		targetSummary.GroupPathsIncluded = len(cfg.Targets.GroupSelector.GroupPathsInclude)
	}

	policySummary := &PolicySummaryJSON{
		PushRules:             cfg.Policies.PushRules != nil,
		ProtectedBranches:     len(cfg.Policies.ProtectedBranches),
		ProtectedEnvironments: len(cfg.Policies.ProtectedEnvironments),
		ProjectSettings:       cfg.Policies.ProjectSettings != nil,
		PipelineRetention:     cfg.Policies.PipelineRetention != nil,
		Variables:             len(cfg.Policies.Variables),
		Compliance:            cfg.Policies.Compliance != nil,
		Webhooks:              len(cfg.Policies.Webhooks),
	}
	if cfg.Policies.ApprovalRules != nil {
		policySummary.ApprovalRules = len(cfg.Policies.ApprovalRules.Rules)
	}
	if cfg.Policies.Runners != nil {
		policySummary.Runners = len(cfg.Policies.Runners.Runners)
	}
	if cfg.Policies.Members != nil {
		policySummary.Members = len(cfg.Policies.Members.AllowedMembers)
	}
	if cfg.Policies.TargetBranchRules != nil {
		policySummary.TargetBranchRules = len(cfg.Policies.TargetBranchRules.Rules)
	}

	warnOutput := warnings
	if warnOutput == nil {
		warnOutput = []config.ValidationWarning{}
	}

	res := ValidateJSONOutput{
		Status:   "VALID",
		Valid:    true,
		Source:   sourceDesc,
		Targets:  targetSummary,
		Policies: policySummary,
		Errors:   []config.ValidationError{},
		Warnings: warnOutput,
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(out, string(data))
	return nil
}

func handleValidateError(out io.Writer, flags validateFlags, source string, err error, warnings []config.ValidationWarning) error {
	if flags.JSON {
		var valErrors []config.ValidationError
		if ve, ok := err.(config.ValidationErrors); ok {
			valErrors = ve.Errors()
		} else {
			valErrors = []config.ValidationError{{Field: "syntax", Message: err.Error()}}
		}

		warnOutput := warnings
		if warnOutput == nil {
			warnOutput = []config.ValidationWarning{}
		}

		resp := ValidateJSONOutput{
			Status:   "INVALID",
			Valid:    false,
			Source:   source,
			Errors:   valErrors,
			Warnings: warnOutput,
		}
		data, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Fprintln(out, string(data))
		return err
	}

	if !flags.Quiet {
		if len(warnings) > 0 {
			for _, w := range warnings {
				if os.Getenv("NO_COLOR") != "" {
					fmt.Fprintf(out, "WARNING: %s: %s\n", w.Field, w.Message)
				} else {
					fmt.Fprintf(out, "\033[33m⚠ WARNING: %s: %s\033[0m\n", w.Field, w.Message)
				}
			}
		}
		if os.Getenv("NO_COLOR") != "" {
			fmt.Fprintf(out, "FAILED: Configuration validation error in '%s':\n%v\n", source, err)
		} else {
			fmt.Fprintf(out, "\033[31m✖ Configuration validation failed for '%s':\033[0m\n%v\n", source, err)
		}
	}

	return err
}
