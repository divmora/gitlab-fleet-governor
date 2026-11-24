package templates_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/templates"
)

func validate(t *testing.T, name string, policy []byte) *config.PolicyConfig {
	t.Helper()
	cfg, err := config.Unmarshal(policy)
	require.NoError(t, err, name)
	cfg.SetDefaults()
	require.NoError(t, cfg.Validate(), name)
	return cfg
}

// Every template is a valid policy as shipped and once filled in, so the
// library can't drift from the schema.
func TestTemplatesAreValidPolicies(t *testing.T) {
	values := templates.Values{Group: "acme/platform", EmailDomain: "acme.co.uk", ReviewerGroup: "acme/security"}
	for _, tpl := range templates.List() {
		t.Run(tpl.Name, func(t *testing.T) {
			raw, err := templates.Policy(tpl.Name)
			require.NoError(t, err)
			validate(t, tpl.Name+" (raw)", raw)

			rendered, missing := templates.Render(raw, values)
			assert.Empty(t, missing)
			cfg := validate(t, tpl.Name+" (rendered)", rendered)

			assert.Equal(t, []string{"acme/platform"}, cfg.Targets.GroupSelector.GroupPathsInclude)
			assert.Equal(t, `@acme\.co\.uk$`, cfg.Policies.PushRules.AuthorEmailRegex)
			require.NotNil(t, cfg.Policies.GroupSettings)
			assert.True(t, *cfg.Policies.GroupSettings.RequireTwoFactorAuth, "every template requires 2FA")
			assert.True(t, *cfg.Settings.DryRun, "templates start in dry-run")
			assert.True(t, regexp.MustCompile(`@acme\.co\.uk$`).MatchString("dev@acme.co.uk"))
			assert.False(t, regexp.MustCompile(cfg.Policies.PushRules.AuthorEmailRegex).MatchString("dev@acmeXcoXuk"), "the domain is regex-escaped")
		})
	}
}

// The catalogue lists exactly the template directories on disk.
func TestCatalogueMatchesDirectories(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
			assert.FileExists(t, filepath.Join(e.Name(), "policy.yaml"))
			assert.FileExists(t, filepath.Join(e.Name(), "README.md"))
		}
	}
	var listed []string
	for _, tpl := range templates.List() {
		listed = append(listed, tpl.Name)
	}
	sort.Strings(dirs)
	sort.Strings(listed)
	assert.Equal(t, dirs, listed)
}

// Templates must not claim compliance or certification: they support
// evidence, and only an auditor attests.
func TestTemplateWording(t *testing.T) {
	forbidden := regexp.MustCompile(`(?i)\b(certified|guarantee[sd]?|fully compliant|attestation|attests?)\b`)
	index, err := os.ReadFile("README.md")
	require.NoError(t, err)
	assert.NotRegexp(t, forbidden, string(index))
	for _, tpl := range templates.List() {
		readme, err := templates.Readme(tpl.Name)
		require.NoError(t, err)
		text := string(readme)
		assert.Contains(t, text, "not a certification", tpl.Name)
		assert.Contains(t, text, "## Control mapping", tpl.Name)
		assert.NotRegexp(t, forbidden, text, tpl.Name)
		raw, _ := templates.Policy(tpl.Name)
		assert.Contains(t, string(raw), "not a\n# certification", tpl.Name)
	}
}

func TestRenderAndValues(t *testing.T) {
	raw, err := templates.Policy("pci-dss")
	require.NoError(t, err)
	_, missing := templates.Render(raw, templates.Values{Group: "acme"})
	assert.Equal(t, []string{templates.PlaceholderEmailDomain, templates.PlaceholderReviewerGroup}, missing)

	out, _ := templates.Render(raw, templates.Values{Group: "/acme/platform/", EmailDomain: "@acme.com"})
	assert.Contains(t, string(out), `["acme/platform"]`)
	assert.Contains(t, string(out), `'@acme\.com$'`)

	for _, bad := range []templates.Values{
		{Group: `acme"]\nsettings: {dry_run: false}`},
		{ReviewerGroup: "a b"},
		{EmailDomain: "acme.com'$"},
		{EmailDomain: "localhost"},
	} {
		assert.Error(t, bad.Validate(), "%+v", bad)
	}
	assert.NoError(t, templates.Values{Group: "acme/platform-eng", ReviewerGroup: "acme/sec.ops", EmailDomain: "mail.acme.io"}.Validate())

	_, err = templates.Policy("gdpr")
	assert.ErrorContains(t, err, `unknown template "gdpr"`)
}

func TestIndexListsEveryTemplate(t *testing.T) {
	index, err := os.ReadFile("README.md")
	require.NoError(t, err)
	for _, tpl := range templates.List() {
		assert.True(t, strings.Contains(string(index), "("+tpl.Name+"/README.md)"), tpl.Name)
	}
}
