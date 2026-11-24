package cli

import (
	"fmt"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// requireSingleInstance rejects a multi-instance policy for commands that do
// not support one yet. Falling back to settings.gitlab would silently target
// whatever GITLAB_BASE_URL and GITLAB_TOKEN point at.
func requireSingleInstance(command string, cfg *config.PolicyConfig) error {
	if cfg.IsMultiInstance() {
		return fmt.Errorf("%s does not support policies with an instances block yet; use a policy with settings.gitlab for the instance to %s", command, command)
	}
	return nil
}
