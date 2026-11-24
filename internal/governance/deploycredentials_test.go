package governance_test

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gogitlab "gitlab.com/gitlab-org/api/client-go"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/governance"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil/mockserver"
)

// ed25519Key returns an authorized_keys line for a fake ed25519 key whose
// 32 key bytes are all seed.
func ed25519Key(seed byte) string {
	var blob []byte
	put := func(b []byte) {
		n := make([]byte, 4)
		binary.BigEndian.PutUint32(n, uint32(len(b)))
		blob = append(blob, n...)
		blob = append(blob, b...)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed
	}
	put([]byte("ssh-ed25519"))
	put(key)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " test"
}

func fp(t *testing.T, key string) string {
	t.Helper()
	f, err := config.SSHKeyFingerprint(key)
	require.NoError(t, err)
	return f
}

func diffActions(res *governance.PlanResult) map[string]governance.ActionType {
	out := map[string]governance.ActionType{}
	for _, d := range res.Diffs {
		for _, f := range d.Fields {
			out[d.Resource+"."+f.Field] = f.Action
		}
	}
	return out
}

func TestDeployKeys_ManageDenyPushAndAge(t *testing.T) {
	ctx := context.Background()
	srv, client, proj := seededSecurityServer(t)
	old := time.Now().AddDate(0, 0, -200)
	release, newKey := ed25519Key(1), ed25519Key(2)
	srv.State().AddDeployKey(101, gogitlab.ProjectDeployKey{Title: "old-ci", Key: ed25519Key(9), FingerprintSHA256: fp(t, ed25519Key(9)), CanPush: true, CreatedAt: &old})
	srv.State().AddDeployKey(101, gogitlab.ProjectDeployKey{Title: "renamed-release", Key: release, FingerprintSHA256: fp(t, release)})

	cfg := &config.PolicyConfig{Policies: config.PoliciesConfig{DeployKeys: &config.DeployKeysConfig{
		Keys: []config.DeployKeyConfig{
			{Title: "release", Key: release, CanPush: gogitlab.Ptr(true)}, // matched by fingerprint despite the title
			{Title: "deployer", Key: newKey},
			{Title: "manual-only"}, // no public key: reported, never added
		},
		DenyPush:   gogitlab.Ptr(true),
		MaxAgeDays: gogitlab.Ptr(90),
	}}}
	rec := governance.NewDeployKeysReconciler()

	plan, err := rec.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.ActionUpdate, plan.Action)
	assert.Equal(t, map[string]governance.ActionType{
		"deploy_key:release.can_push":    governance.ActionUpdate,
		"deploy_key:deployer.can_push":   governance.ActionCreate,
		"deploy_key:manual-only.missing": governance.ActionAudit,
		"deploy_key:old-ci.can_push":     governance.ActionUpdate,
		"deploy_key:old-ci.age_days":     governance.ActionAudit,
	}, diffActions(plan))

	_, err = rec.Apply(ctx, client, proj, cfg)
	require.NoError(t, err)
	keys := srv.State().DeployKeys(101)
	require.Len(t, keys, 3)
	byTitle := map[string]gogitlab.ProjectDeployKey{}
	for _, k := range keys {
		byTitle[k.Title] = k
	}
	assert.False(t, byTitle["old-ci"].CanPush, "unlisted keys become read-only")
	assert.True(t, byTitle["renamed-release"].CanPush)
	assert.False(t, byTitle["deployer"].CanPush, "can_push defaults to read-only")
	assert.Equal(t, fp(t, newKey), byTitle["deployer"].FingerprintSHA256)

	again, err := rec.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, governance.ActionAudit, again.Action, "only findings that need a person remain")
	assert.Equal(t, map[string]governance.ActionType{
		"deploy_key:manual-only.missing": governance.ActionAudit,
		"deploy_key:old-ci.age_days":     governance.ActionAudit,
	}, diffActions(again))
}

func TestDeployKeys_PruneAndTitleMatching(t *testing.T) {
	ctx := context.Background()
	srv, client, proj := seededSecurityServer(t)
	srv.State().AddDeployKey(101, gogitlab.ProjectDeployKey{Title: "CI Reader", Key: ed25519Key(3), FingerprintSHA256: fp(t, ed25519Key(3))})
	srv.State().AddDeployKey(101, gogitlab.ProjectDeployKey{Title: "leftover", Key: ed25519Key(4), FingerprintSHA256: fp(t, ed25519Key(4))})

	cfg := &config.PolicyConfig{Policies: config.PoliciesConfig{DeployKeys: &config.DeployKeysConfig{
		Keys:  []config.DeployKeyConfig{{Title: "ci reader"}},
		Prune: gogitlab.Ptr(true),
	}}}
	rec := governance.NewDeployKeysReconciler()
	plan, err := rec.Plan(ctx, client, proj, cfg)
	require.NoError(t, err)
	assert.Equal(t, map[string]governance.ActionType{"deploy_key:leftover.deploy_key": governance.ActionDelete}, diffActions(plan))

	_, err = rec.Apply(ctx, client, proj, cfg)
	require.NoError(t, err)
	keys := srv.State().DeployKeys(101)
	require.Len(t, keys, 1)
	assert.Equal(t, "CI Reader", keys[0].Title)
}

func tokenPolicy(revoke bool) *config.PolicyConfig {
	return &config.PolicyConfig{Policies: config.PoliciesConfig{DeployTokens: &config.DeployTokensConfig{
		AllowedScopes:      []string{"read_repository", "read_registry"},
		RequireExpiresAt:   gogitlab.Ptr(true),
		MaxExpirationDays:  gogitlab.Ptr(365),
		RevokeExpired:      gogitlab.Ptr(revoke),
		RevokeNoncompliant: gogitlab.Ptr(revoke),
	}}}
}

func seedTokens(srv *mockserver.MockGitLabServer, kind string, owner int64) {
	day := 24 * time.Hour
	past, soon, far := time.Now().Add(-day), time.Now().Add(30*day), time.Now().Add(400*day)
	srv.State().AddDeployToken(kind, owner, gogitlab.DeployToken{Name: "expired", ExpiresAt: &past, Scopes: []string{"read_repository"}})
	srv.State().AddDeployToken(kind, owner, gogitlab.DeployToken{Name: "forever", Scopes: []string{"read_repository"}})
	srv.State().AddDeployToken(kind, owner, gogitlab.DeployToken{Name: "writer", ExpiresAt: &soon, Scopes: []string{"read_repository", "write_registry"}})
	srv.State().AddDeployToken(kind, owner, gogitlab.DeployToken{Name: "too-long", ExpiresAt: &far, Scopes: []string{"read_registry"}})
	srv.State().AddDeployToken(kind, owner, gogitlab.DeployToken{Name: "good", ExpiresAt: &soon, Scopes: []string{"read_registry"}})
	srv.State().AddDeployToken(kind, owner, gogitlab.DeployToken{Name: "already-revoked", Revoked: true, Scopes: []string{"write_registry"}})
}

func activeTokenNames(srv *mockserver.MockGitLabServer, kind string, owner int64) []string {
	var names []string
	for _, tok := range srv.State().DeployTokens(kind, owner) {
		if !tok.Revoked {
			names = append(names, tok.Name)
		}
	}
	return names
}

func TestDeployTokens_ReportOnlyByDefault(t *testing.T) {
	ctx := context.Background()
	srv, client, proj := seededSecurityServer(t)
	seedTokens(srv, "project", 101)
	rec := governance.NewDeployTokensReconciler()

	plan, err := rec.Plan(ctx, client, proj, tokenPolicy(false))
	require.NoError(t, err)
	assert.Equal(t, governance.ActionAudit, plan.Action)
	assert.Equal(t, map[string]governance.ActionType{
		"deploy_token:expired.expired":        governance.ActionAudit,
		"deploy_token:forever.non_compliant":  governance.ActionAudit,
		"deploy_token:writer.non_compliant":   governance.ActionAudit,
		"deploy_token:too-long.non_compliant": governance.ActionAudit,
	}, diffActions(plan))

	_, err = rec.Apply(ctx, client, proj, tokenPolicy(false))
	require.NoError(t, err)
	assert.Len(t, activeTokenNames(srv, "project", 101), 5, "nothing is revoked without revoke_*")
}

func TestDeployTokens_RevokeProjectAndGroup(t *testing.T) {
	ctx := context.Background()
	srv, client, proj := seededSecurityServer(t)
	seedTokens(srv, "project", 101)
	seedTokens(srv, "group", 10)
	rec := governance.NewDeployTokensReconciler()

	plan, err := rec.Plan(ctx, client, proj, tokenPolicy(true))
	require.NoError(t, err)
	assert.Equal(t, governance.ActionDelete, plan.Action)
	assert.Len(t, activeTokenNames(srv, "project", 101), 5, "plan revokes nothing")

	_, err = rec.Apply(ctx, client, proj, tokenPolicy(true))
	require.NoError(t, err)
	assert.Equal(t, []string{"good"}, activeTokenNames(srv, "project", 101))
	again, err := rec.Plan(ctx, client, proj, tokenPolicy(true))
	require.NoError(t, err)
	assert.Equal(t, governance.ActionNoop, again.Action)

	group, _ := srv.State().GetGroup(10)
	_, err = rec.ApplyGroup(ctx, client, group, tokenPolicy(true))
	require.NoError(t, err)
	assert.Equal(t, []string{"good"}, activeTokenNames(srv, "group", 10))
}

func TestDeployCredentials_NotConfigured(t *testing.T) {
	ctx := context.Background()
	_, client, proj := seededSecurityServer(t)
	kp, err := governance.NewDeployKeysReconciler().Plan(ctx, client, proj, &config.PolicyConfig{})
	require.NoError(t, err)
	assert.Equal(t, governance.ActionNoop, kp.Action)
	tp, err := governance.NewDeployTokensReconciler().Plan(ctx, client, proj, &config.PolicyConfig{})
	require.NoError(t, err)
	assert.Equal(t, governance.ActionNoop, tp.Action)
}
