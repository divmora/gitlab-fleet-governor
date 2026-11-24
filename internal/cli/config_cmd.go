package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// redacted replaces secret values in rendered output.
const redacted = "<redacted>"

type renderFlags struct {
	Instance string
	Format   string
}

// RenderedPolicy is the effective policy for one GitLab instance.
type RenderedPolicy struct {
	// Instance is the instance name, empty for a settings.gitlab policy.
	Instance            string `yaml:"instance,omitempty" json:"instance,omitempty"`
	config.PolicyConfig `yaml:",inline"`
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect policy configuration",
	}
	cmd.AddCommand(newConfigRenderCmd())
	return cmd
}

func newConfigRenderCmd() *cobra.Command {
	var flags renderFlags
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Print the effective policy for each GitLab instance",
		Long: `Render loads and validates a policy, applies each instance's connection
settings, targets and policy overrides, and prints the policy that each
instance is governed with. It makes no API calls.

GitLab tokens, the licence key, webhook secret tokens and the values of
masked variables are shown as "` + redacted + `".`,
		Example: `  # Effective policy for every instance
  gitlab-fleet-governor config render -c policy.yaml

  # One instance, as JSON
  gitlab-fleet-governor config render -c policy.yaml --instance self-managed --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeConfigRender(cmd, flags)
		},
	}
	cmd.Flags().StringVar(&flags.Instance, "instance", "", "Render only this instance")
	cmd.Flags().StringVar(&flags.Format, "format", "yaml", "Output format: yaml or json")
	return cmd
}

func executeConfigRender(cmd *cobra.Command, flags renderFlags) error {
	format := strings.ToLower(strings.TrimSpace(flags.Format))
	if format != "yaml" && format != "json" {
		return fmt.Errorf("invalid --format %q (must be yaml or json)", flags.Format)
	}

	cfg, _, err := config.Load(cmd.Context(), globalFlags.ConfigPath, config.LoadOptions{
		LoaderOptions: []config.LoaderOption{config.WithStdin(cmd.InOrStdin())},
	})
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	runs, err := cfg.InstanceRuns()
	if err != nil {
		return err
	}

	var out []RenderedPolicy
	var names []string
	for _, run := range runs {
		names = append(names, run.Name)
		if flags.Instance != "" && run.Name != flags.Instance {
			continue
		}
		out = append(out, renderRun(run))
	}
	if flags.Instance != "" && len(out) == 0 {
		if !cfg.IsMultiInstance() {
			return fmt.Errorf("--instance %q: the policy has no instances block", flags.Instance)
		}
		return fmt.Errorf("--instance %q: no such instance (have %s)", flags.Instance, strings.Join(names, ", "))
	}

	return writeRendered(cmd.OutOrStdout(), out, format)
}

// renderRun returns a redacted copy of one instance's effective policy.
func renderRun(run config.InstanceRun) RenderedPolicy {
	c := *run.Config
	c.Instances = nil

	if c.Settings.GitLab.Token != "" {
		c.Settings.GitLab.Token = redacted
	}
	if c.Settings.License.Key != "" {
		c.Settings.License.Key = redacted
	}

	if len(c.Policies.Variables) > 0 {
		vars := make([]config.VariableConfig, len(c.Policies.Variables))
		copy(vars, c.Policies.Variables)
		for i := range vars {
			if vars[i].Masked != nil && *vars[i].Masked && vars[i].Value != "" {
				vars[i].Value = redacted
			}
		}
		c.Policies.Variables = vars
	}
	if len(c.Policies.Webhooks) > 0 {
		hooks := make([]config.WebhookConfig, len(c.Policies.Webhooks))
		copy(hooks, c.Policies.Webhooks)
		for i := range hooks {
			if hooks[i].SecretToken != "" {
				hooks[i].SecretToken = redacted
			}
		}
		c.Policies.Webhooks = hooks
	}

	return RenderedPolicy{Instance: run.Name, PolicyConfig: c}
}

func writeRendered(w io.Writer, docs []RenderedPolicy, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if len(docs) == 1 {
			return enc.Encode(docs[0])
		}
		return enc.Encode(docs)
	}

	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	for _, d := range docs {
		if err := enc.Encode(d); err != nil {
			return err
		}
	}
	return enc.Close()
}
