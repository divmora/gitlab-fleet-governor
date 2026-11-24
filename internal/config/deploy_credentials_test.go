package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
)

// A throwaway ed25519 public key and its fingerprint as printed by
// "ssh-keygen -lf".
const (
	testPublicKey   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIF/vpozeOfglQVen/VISALOttaoe9jAGubLaCCz02V80"
	testFingerprint = "SHA256:3+l2zztZvcsCJ5rYMz9NXMsm1cv9mJooUXy6v1qjPY0"
)

func TestSSHKeyFingerprint(t *testing.T) {
	got, err := config.SSHKeyFingerprint(testPublicKey + " comment")
	require.NoError(t, err)
	assert.Equal(t, testFingerprint, got)

	for _, bad := range []string{"", "ssh-ed25519", "ssh-ed25519 !!notbase64!!"} {
		_, err := config.SSHKeyFingerprint(bad)
		assert.Error(t, err, bad)
	}
}

func TestDeployCredentials_Validation(t *testing.T) {
	valid := `
version: v1
policies:
  deploy_keys:
    keys:
      - title: release
        key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIF/vpozeOfglQVen/VISALOttaoe9jAGubLaCCz02V80 release"
        fingerprint: "SHA256:3+l2zztZvcsCJ5rYMz9NXMsm1cv9mJooUXy6v1qjPY0"
        can_push: true
      - title: reader
        fingerprint: "SHA256:3+l2zztZvcsCJ5rYMz9NXMsm1cv9mJooUXy6v1qjPY0"
    deny_push: true
    max_age_days: 90
    prune: false
  deploy_tokens:
    allowed_scopes: [read_repository, read_registry]
    require_expires_at: true
    max_expiration_days: 365
    revoke_expired: true
    revoke_noncompliant: false
`
	cfg, err := config.Unmarshal([]byte(valid))
	require.NoError(t, err)
	cfg.SetDefaults()
	require.NoError(t, cfg.Validate())

	for name, tc := range map[string]struct{ yaml, field string }{
		"no title":        {"deploy_keys:\n    keys:\n      - key: \"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIF/vpozeOfglQVen/VISALOttaoe9jAGubLaCCz02V80\"", "policies.deploy_keys.keys[0].title"},
		"duplicate title": {"deploy_keys:\n    keys:\n      - title: a\n      - title: A", "policies.deploy_keys.keys[1].title"},
		"bad key":         {"deploy_keys:\n    keys:\n      - title: a\n        key: not-a-key", "policies.deploy_keys.keys[0].key"},
		"bad fingerprint": {"deploy_keys:\n    keys:\n      - title: a\n        fingerprint: MD5:aa", "policies.deploy_keys.keys[0].fingerprint"},
		"mismatch":        {"deploy_keys:\n    keys:\n      - title: a\n        key: \"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIF/vpozeOfglQVen/VISALOttaoe9jAGubLaCCz02V80\"\n        fingerprint: \"SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"", "does not match key"},
		"max age":         {"deploy_keys:\n    max_age_days: 0", "policies.deploy_keys.max_age_days"},
		"scope":           {"deploy_tokens:\n    allowed_scopes: [api]", "policies.deploy_tokens.allowed_scopes[0]"},
		"max expiration":  {"deploy_tokens:\n    max_expiration_days: -5", "policies.deploy_tokens.max_expiration_days"},
		"revoke no rules": {"deploy_tokens:\n    revoke_noncompliant: true", "policies.deploy_tokens.revoke_noncompliant"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := config.Unmarshal([]byte("version: v1\npolicies:\n  " + tc.yaml + "\n"))
			require.NoError(t, err)
			cfg.SetDefaults()
			err = cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.field)
		})
	}
}
