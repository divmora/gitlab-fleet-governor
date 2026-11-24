package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/templates"
)

type initFlags struct {
	Template      string
	List          bool
	Readme        bool
	Force         bool
	Group         string
	EmailDomain   string
	ReviewerGroup string
}

func newInitCmd() *cobra.Command {
	var flags initFlags
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a policy from a compliance starter template",
		Long: `Init writes a starter policy for a compliance framework (SOC 2, ISO/IEC 27001,
PCI DSS, HIPAA, NIST SP 800-53, FedRAMP Moderate), fills in its placeholders
and validates it. The templates support evidence for the framework's controls;
they are a starting point, not a certification.`,
		Example: `  # List the templates
  gitlab-fleet-governor init --list

  # Write a SOC 2 starter policy
  gitlab-fleet-governor init --template soc2 --group acme/platform \
    --email-domain acme.com --reviewer-group acme/security -o policy.yaml

  # Print a template's control mapping
  gitlab-fleet-governor init --template pci-dss --readme`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeInit(cmd, flags)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&flags.Template, "template", "t", "", "Template name (see --list)")
	f.BoolVar(&flags.List, "list", false, "List the available templates")
	f.BoolVar(&flags.Readme, "readme", false, "Print the template's overview and control mapping instead of writing a policy")
	f.BoolVar(&flags.Force, "force", false, "Overwrite the output file if it exists")
	f.StringVar(&flags.Group, "group", "", "Top-level group to govern (fills "+templates.PlaceholderGroup+")")
	f.StringVar(&flags.EmailDomain, "email-domain", "", "Corporate commit email domain, e.g. acme.com (fills "+templates.PlaceholderEmailDomain+")")
	f.StringVar(&flags.ReviewerGroup, "reviewer-group", "", "Group that approves merge requests (fills "+templates.PlaceholderReviewerGroup+")")
	return cmd
}

func executeInit(cmd *cobra.Command, flags initFlags) error {
	out := cmd.OutOrStdout()
	if flags.List {
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "TEMPLATE\tFRAMEWORK")
		for _, t := range templates.List() {
			fmt.Fprintf(w, "%s\t%s\n", t.Name, t.Framework)
		}
		return w.Flush()
	}
	if strings.TrimSpace(flags.Template) == "" {
		return errors.New("--template is required (see --list)")
	}
	if flags.Readme {
		readme, err := templates.Readme(flags.Template)
		if err != nil {
			return err
		}
		_, err = out.Write(readme)
		return err
	}

	raw, err := templates.Policy(flags.Template)
	if err != nil {
		return err
	}
	values := templates.Values{Group: flags.Group, EmailDomain: flags.EmailDomain, ReviewerGroup: flags.ReviewerGroup}
	if err := values.Validate(); err != nil {
		return err
	}
	rendered, missing := templates.Render(raw, values)

	// The result must be a valid policy; placeholders are valid values.
	cfg, err := config.Unmarshal(rendered)
	if err == nil {
		cfg.SetDefaults()
		err = cfg.Validate()
	}
	if err != nil {
		return fmt.Errorf("template %q is not valid with these values: %w", flags.Template, err)
	}

	path := strings.TrimSpace(globalFlags.OutputFile)
	if path == "" {
		path = "policy.yaml"
	}
	if path == "-" {
		if _, err := out.Write(rendered); err != nil {
			return err
		}
	} else {
		path = filepath.Clean(path)
		if !flags.Force {
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists; pass --force to overwrite it", path)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		if err := os.WriteFile(path, rendered, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s starter policy to %s\n", flags.Template, path)
	}

	errOut := cmd.ErrOrStderr()
	if len(missing) > 0 {
		fmt.Fprintf(errOut, "Still to fill in: %s\n", strings.Join(missing, ", "))
	}
	fmt.Fprintf(errOut, "Next: gitlab-fleet-governor validate -c %s, then run it in dry-run. Control mapping: gitlab-fleet-governor init --template %s --readme\n", displayPath(path), flags.Template)
	fmt.Fprintln(errOut, "This template supports evidence for the framework's controls; it is not a certification.")
	return nil
}

func displayPath(p string) string {
	if p == "-" {
		return "<file>"
	}
	return p
}
