package license_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	liblicense "github.com/divmora/license-go/pkg/license"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/internal/testutil"
	"github.com/divmora/gitlab-fleet-governor/pkg/version"
)

var staticReleasePrivKey = ed25519.NewKeyFromSeed([]byte("divmora-rel-test-seed-32bytes!!!"))
var staticReleasePubKey = staticReleasePrivKey.Public().(ed25519.PublicKey)

func TestVerify_Success(t *testing.T) {
	pub := testutil.GetTestPublicKey()

	// Compact DIV1 token
	status, err := license.ParseAndVerify(testutil.ValidCompactToken, pub)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.True(t, status.Valid)
	assert.False(t, status.InGracePeriod)
	assert.True(t, status.DaysRemaining >= 364)
	assert.Equal(t, "lic_test_12345", status.Claims.ID)
	assert.Equal(t, "Fintech Global Corp", status.Claims.Customer.Name)
	assert.Equal(t, "enterprise", status.Claims.Plan)
	assert.True(t, status.Claims.HasFeature("cloud_secrets"))

	// Armored PEM token
	statusArmored, err := license.ParseAndVerify(testutil.ValidArmoredToken, pub)
	require.NoError(t, err)
	assert.True(t, statusArmored.Valid)
	assert.Equal(t, "lic_test_12345", statusArmored.Claims.ID)
}

func TestVerify_WithinGracePeriod(t *testing.T) {
	pub := testutil.GetTestPublicKey()
	evalTime := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)

	status, err := license.ParseAndVerifyAt(testutil.GracePeriodToken, pub, evalTime)
	require.NoError(t, err)
	assert.True(t, status.Valid)
	assert.True(t, status.InGracePeriod)
	assert.Contains(t, status.Message, "operating within 14-day grace period")
}

func TestVerify_ExpiredBeyondGracePeriod(t *testing.T) {
	pub := testutil.GetTestPublicKey()
	evalTime := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)

	status, err := license.ParseAndVerifyAt(testutil.ExpiredToken, pub, evalTime)
	require.Error(t, err)
	assert.False(t, status.Valid)
}

func TestVerify_TamperedToken(t *testing.T) {
	pub := testutil.GetTestPublicKey()

	parts := strings.Split(testutil.ValidCompactToken, ".")
	require.Len(t, parts, 3)

	// Tamper payload (parts[1])
	tamperedToken := parts[0] + "." + parts[1] + "xyz." + parts[2]
	_, err := license.ParseAndVerify(tamperedToken, pub)
	require.Error(t, err)

	// Tamper signature (parts[2])
	tamperedSigToken := parts[0] + "." + parts[1] + ".AAAA" + parts[2][4:]
	_, err = license.ParseAndVerify(tamperedSigToken, pub)
	require.Error(t, err)
}

func TestEnforce_Scenarios(t *testing.T) {
	pub := testutil.GetTestPublicKey()

	t.Run("Dry Run Exemption: 500 projects with no license is permitted", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 500,
			IsDryRun:           true,
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
		assert.Contains(t, status.Message, "dry-run")
	})

	t.Run("Free Community Tier: 25 projects in production without license is permitted", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 25,
			IsDryRun:           false,
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
		assert.Contains(t, status.Message, "Free Community Tier")
	})

	t.Run("Exceeded Free Tier: 26 projects in production without license fails", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 26,
			IsDryRun:           false,
			PublicKey:          pub,
		})
		require.Error(t, err)
		assert.Nil(t, status)
		assert.Contains(t, err.Error(), "COMMERCIAL LICENSE REQUIRED")
		assert.Contains(t, err.Error(), "26 projects")
	})

	t.Run("Over Free Tier With Valid License: 80 projects with 100-project license succeeds", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 80,
			IsDryRun:           false,
			LicenseKey:         testutil.ValidCompactToken,
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
		assert.Equal(t, "lic_test_12345", status.Claims.ID)
	})

	t.Run("Capacity Exceeded: 150 projects with 100-project license fails", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 150,
			IsDryRun:           false,
			LicenseKey:         testutil.ValidCompactToken,
			PublicKey:          pub,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FLEET CAPACITY EXCEEDED")
		assert.Contains(t, err.Error(), "150 production projects exceeds your licensed capacity of 100")
		assert.NotNil(t, status)
	})

	t.Run("Commercial License in DryRun Mode: retains claims and validity", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 80,
			IsDryRun:           true,
			LicenseKey:         testutil.ValidCompactToken,
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
		require.NotNil(t, status.Claims)
		assert.Equal(t, "lic_test_12345", status.Claims.ID)
		assert.Equal(t, "Fintech Global Corp", status.Claims.Customer.Name)
	})
}

func TestEnforce_PixelvideLicense(t *testing.T) {
	pub := testutil.GetTestPublicKey()
	evalTime := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)

	t.Run("DryRun Mode with 437 projects", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 437,
			IsDryRun:           true,
			LicenseKey:         testutil.PixelvideToken,
			EvaluationTime:     evalTime,
			GitLabBaseURL:      "https://gitlab.pixelvide.com/api/v4",
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
		require.NotNil(t, status.Claims)
		assert.Equal(t, "lic_1f9326c6", status.Claims.ID)
		assert.Equal(t, "PIXELVIDE DESIGN SOLUTIONS LLP", status.Claims.Customer.Name)
		maxProjects, ok := status.Claims.GetLimit("max_projects")
		assert.True(t, ok)
		assert.Equal(t, int64(500), maxProjects)
		assert.Equal(t, "enterprise", status.Claims.Plan)
	})

	t.Run("Production Mode with 437 projects", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 437,
			IsDryRun:           false,
			LicenseKey:         testutil.PixelvideToken,
			EvaluationTime:     evalTime,
			GitLabBaseURL:      "https://gitlab.pixelvide.com/api/v4",
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
		require.NotNil(t, status.Claims)
		assert.Equal(t, "PIXELVIDE DESIGN SOLUTIONS LLP", status.Claims.Customer.Name)
	})
}

func TestResolveToken_FromFilesAndEnv(t *testing.T) {
	tempDir := t.TempDir()
	keyFile := filepath.Join(tempDir, "license.key")
	err := os.WriteFile(keyFile, []byte("DIV1.token.fromfile\n"), 0600)
	require.NoError(t, err)

	// Direct key
	k1, err := license.ResolveToken("DIV1.direct.token", "")
	require.NoError(t, err)
	assert.Equal(t, "DIV1.direct.token", k1)

	// File path
	k2, err := license.ResolveToken("", keyFile)
	require.NoError(t, err)
	assert.Equal(t, "DIV1.token.fromfile", k2)

	// Environment variable
	t.Setenv("DIVMORA_LICENSE_KEY", "DIV1.token.fromenv")
	k3, err := license.ResolveToken("", "")
	require.NoError(t, err)
	assert.Equal(t, "DIV1.token.fromenv", k3)

	// Environment variable file
	t.Setenv("DIVMORA_LICENSE_KEY", "")
	t.Setenv("DIVMORA_LICENSE_FILE", keyFile)
	k4, err := license.ResolveToken("", "")
	require.NoError(t, err)
	assert.Equal(t, "DIV1.token.fromfile", k4)
}

func TestResolveToken_Hierarchy(t *testing.T) {
	tempDir := t.TempDir()
	divmoraFile := filepath.Join(tempDir, "divmora.key")
	cliFile := filepath.Join(tempDir, "cli.key")

	require.NoError(t, os.WriteFile(divmoraFile, []byte("DIV1.divmora.filetoken\n"), 0600))
	require.NoError(t, os.WriteFile(cliFile, []byte("DIV1.cli.filetoken\n"), 0600))

	t.Run("Direct key overrides everything", func(t *testing.T) {
		t.Setenv("DIVMORA_LICENSE_KEY", "DIV1.env.divmorakey")
		t.Setenv("DIVMORA_LICENSE_FILE", divmoraFile)

		tok, err := license.ResolveToken("DIV1.cli.key", cliFile)
		require.NoError(t, err)
		assert.Equal(t, "DIV1.cli.key", tok)
	})

	t.Run("Direct file overrides all env vars", func(t *testing.T) {
		t.Setenv("DIVMORA_LICENSE_KEY", "DIV1.env.divmorakey")
		t.Setenv("DIVMORA_LICENSE_FILE", divmoraFile)

		tok, err := license.ResolveToken("", cliFile)
		require.NoError(t, err)
		assert.Equal(t, "DIV1.cli.filetoken", tok)
	})

	t.Run("DIVMORA_LICENSE_KEY takes precedence over DIVMORA_LICENSE_FILE", func(t *testing.T) {
		t.Setenv("DIVMORA_LICENSE_KEY", "DIV1.divmora.key")
		t.Setenv("DIVMORA_LICENSE_FILE", divmoraFile)

		tok, err := license.ResolveToken("", "")
		require.NoError(t, err)
		assert.Equal(t, "DIV1.divmora.key", tok)
	})

	t.Run("DIVMORA_LICENSE_FILE is used when DIVMORA_LICENSE_KEY is empty", func(t *testing.T) {
		t.Setenv("DIVMORA_LICENSE_KEY", "")
		t.Setenv("DIVMORA_LICENSE_FILE", divmoraFile)

		tok, err := license.ResolveToken("", "")
		require.NoError(t, err)
		assert.Equal(t, "DIV1.divmora.filetoken", tok)
	})

	t.Run("Empty resolution when nothing is set", func(t *testing.T) {
		t.Setenv("DIVMORA_LICENSE_KEY", "")
		t.Setenv("DIVMORA_LICENSE_FILE", "")

		tok, err := license.ResolveToken("", "")
		require.NoError(t, err)
		assert.Empty(t, tok)
	})

	t.Run("Error when DIVMORA_LICENSE_FILE points to nonexistent path", func(t *testing.T) {
		t.Setenv("DIVMORA_LICENSE_KEY", "")
		t.Setenv("DIVMORA_LICENSE_FILE", filepath.Join(tempDir, "nonexistent-divmora.key"))

		_, err := license.ResolveToken("", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "DIVMORA_LICENSE_FILE")
	})
}

func TestProductClaims_Validation(t *testing.T) {
	pub := testutil.GetTestPublicKey()

	tests := []struct {
		name          string
		token         string
		claimProduct  string
		expectAllowed bool
		expectErr     string
	}{
		{
			name:          "Wildcard product (*)",
			token:         testutil.ProductTokenWildcard,
			claimProduct:  "*",
			expectAllowed: true,
		},
		{
			name:          "DIVMORA suite license",
			token:         testutil.ProductTokenDivmoraSuite,
			claimProduct:  "divmora-suite",
			expectAllowed: true,
		},
		{
			name:          "Matching product gitlab-fleet-governor",
			token:         testutil.ProductTokenGitlabFleetGovernor,
			claimProduct:  "gitlab-fleet-governor",
			expectAllowed: true,
		},
		{
			name:          "Unrecognized alias fleet-governor rejected",
			token:         testutil.ProductTokenFleetGovernor,
			claimProduct:  "fleet-governor",
			expectAllowed: false,
			expectErr:     "license: product mismatch",
		},
		{
			name:          "Case-insensitive matching",
			token:         testutil.ProductTokenGitLabFleetGovernorCase,
			claimProduct:  "GitLab-Fleet-Governor",
			expectAllowed: true,
		},
		{
			name:          "Mismatched product (github-fleet-governor)",
			token:         testutil.ProductTokenGithubFleetGovernor,
			claimProduct:  "github-fleet-governor",
			expectAllowed: false,
			expectErr:     "license: product mismatch",
		},
		{
			name:          "Mismatched product (cloud-compliance-engine)",
			token:         testutil.ProductTokenCloudComplianceEngine,
			claimProduct:  "cloud-compliance-engine",
			expectAllowed: false,
			expectErr:     "license: product mismatch",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claims := &license.Claims{
				ID:      "lic_prod_test",
				Product: tc.claimProduct,
			}

			// Direct helper check
			assert.Equal(t, tc.expectAllowed, claims.IsValidForProduct("gitlab-fleet-governor"))

			// Cryptographic parse & verify check
			status, err := license.ParseAndVerify(tc.token, pub)
			if tc.expectAllowed {
				require.NoError(t, err)
				require.NotNil(t, status)
				assert.True(t, status.Valid)
				assert.Equal(t, tc.claimProduct, status.Claims.Product)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectErr)
			}
		})
	}
}

func TestHostValidation_Scoping(t *testing.T) {
	t.Run("ExtractHost utility", func(t *testing.T) {
		assert.Equal(t, "gitlab.com", license.ExtractHost(""))
		assert.Equal(t, "gitlab.com", license.ExtractHost("https://gitlab.com/api/v4"))
		assert.Equal(t, "gitlab.mycorp.internal", license.ExtractHost("https://gitlab.mycorp.internal:8443/api/v4"))
		assert.Equal(t, "code.devops.io", license.ExtractHost("code.devops.io"))
	})

	t.Run("Wildcard and Omitted AllowedHosts", func(t *testing.T) {
		emptyClaims := &license.Claims{}
		assert.True(t, emptyClaims.IsHostAllowed("https://gitlab.com"))
		assert.True(t, emptyClaims.IsHostAllowed("https://gitlab.private.corp"))

		starClaims := &license.Claims{Scope: &license.Scope{Hosts: []string{"*"}}}
		assert.True(t, starClaims.IsHostAllowed("https://gitlab.com"))
		assert.True(t, starClaims.IsHostAllowed("https://gitlab.private.corp"))
	})

	t.Run("Exact Host Matching", func(t *testing.T) {
		claims := &license.Claims{
			Scope: &license.Scope{Hosts: []string{"gitlab.fintech.corp", "gitlab.com"}},
		}
		assert.True(t, claims.IsHostAllowed("gitlab.fintech.corp"))
		assert.True(t, claims.IsHostAllowed("gitlab.com"))
		assert.False(t, claims.IsHostAllowed("gitlab.other.corp"))
	})

	t.Run("Subdomain Wildcard Matching", func(t *testing.T) {
		claims := &license.Claims{
			Scope: &license.Scope{Hosts: []string{"*.internal.net"}},
		}
		assert.True(t, claims.IsHostAllowed("staging.internal.net"))
		assert.True(t, claims.IsHostAllowed("internal.net"))
		assert.True(t, claims.IsHostAllowed("https://staging.internal.net:8443/api/v4"))
		assert.False(t, claims.IsHostAllowed("gitlab.external.com"))
	})
}

func TestGroupValidation_Scoping(t *testing.T) {
	t.Run("Omitted or Wildcard AllowedGroups", func(t *testing.T) {
		emptyClaims := &license.Claims{}
		assert.True(t, emptyClaims.IsNamespaceAllowed("any-org/repo"))

		starClaims := &license.Claims{Scope: &license.Scope{Namespaces: []string{"*"}}}
		assert.True(t, starClaims.IsNamespaceAllowed("any-org/repo"))
	})

	t.Run("Hierarchy Matching", func(t *testing.T) {
		claims := &license.Claims{
			Scope: &license.Scope{Namespaces: []string{"acme-corp/*", "fintech-division/*"}},
		}

		assert.True(t, claims.IsNamespaceAllowed("acme-corp/billing"))
		assert.True(t, claims.IsNamespaceAllowed("acme-corp/billing/payments-service"))
		assert.True(t, claims.IsNamespaceAllowed("fintech-division/core-banking"))
		assert.False(t, claims.IsNamespaceAllowed("other-org/billing"))
	})

	t.Run("Plain Root Group Matching Without Wildcards", func(t *testing.T) {
		claims := &license.Claims{
			Scope: &license.Scope{Namespaces: []string{"devops"}},
		}

		assert.True(t, claims.IsNamespaceAllowed("devops"))
		assert.True(t, claims.IsNamespaceAllowed("devops/backend"))
		assert.True(t, claims.IsNamespaceAllowed("devops/backend/service"))
		assert.True(t, claims.IsNamespaceAllowed("https://gitlab.com/devops/backend/service.git"))

		// Security boundary: rejects prefix collisions
		assert.False(t, claims.IsNamespaceAllowed("devops-tools"))
		assert.False(t, claims.IsNamespaceAllowed("devops_infra"))
		assert.False(t, claims.IsNamespaceAllowed("other/devops"))
	})
}

func TestEnforce_HostAndGroupScoping(t *testing.T) {
	pub := testutil.GetTestPublicKey()

	t.Run("Permitted within valid host and group scope", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 50,
			IsDryRun:           false,
			GitLabBaseURL:      "https://gitlab.com/api/v4",
			TargetPaths:        []string{"enterprise-org/backend", "enterprise-org/frontend"},
			LicenseKey:         testutil.ScopedToken,
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
	})

	t.Run("Rejected when host does not match", func(t *testing.T) {
		_, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 50,
			IsDryRun:           false,
			GitLabBaseURL:      "https://gitlab.selfhosted.corp/api/v4",
			TargetPaths:        []string{"enterprise-org/backend"},
			LicenseKey:         testutil.ScopedToken,
			PublicKey:          pub,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "COMMERCIAL LICENSE HOST MISMATCH")
	})

	t.Run("Rejected when group does not match", func(t *testing.T) {
		_, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 50,
			IsDryRun:           false,
			GitLabBaseURL:      "https://gitlab.com/api/v4",
			TargetPaths:        []string{"other-org/backend"},
			LicenseKey:         testutil.ScopedToken,
			PublicKey:          pub,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "COMMERCIAL LICENSE GROUP MISMATCH")
	})
}

func TestEnforce_ChangeDate_AutomaticApacheConversion(t *testing.T) {
	origBuildDate := version.BuildDate
	origVersion := version.Version
	origSig := version.ReleaseSignature
	defer func() {
		version.BuildDate = origBuildDate
		version.Version = origVersion
		version.ReleaseSignature = origSig
	}()

	version.Version = "0.4.0"
	version.BuildDate = "2026-09-11T00:00:00Z"

	version.SetReleaseVerificationPublicKey(staticReleasePubKey)
	defer version.ResetReleaseVerificationPublicKey()

	token, err := version.SignRelease(&version.ReleaseClaims{
		Version:   "0.4.0",
		GitCommit: version.GitCommit,
		BuildDate: "2026-09-11T00:00:00Z",
		Authority: "DIVMORA Technologies",
	}, staticReleasePrivKey)
	require.NoError(t, err)
	version.ReleaseSignature = token

	t.Run("Before Change Date (2 years after release): strictly enforces >25 limits", func(t *testing.T) {
		twoYearsLater := time.Date(2028, 9, 11, 0, 0, 0, 0, time.UTC)
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 100,
			IsDryRun:           false,
			EvaluationTime:     twoYearsLater,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "COMMERCIAL LICENSE REQUIRED")
		assert.Nil(t, status)
	})

	t.Run("After Change Date (3 years and 1 day after release): automatically converts to Apache 2.0 with unlimited projects", func(t *testing.T) {
		threeYearsLater := time.Date(2029, 9, 12, 0, 0, 0, 0, time.UTC)
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 50000, // 50,000 projects!
			IsDryRun:           false,
			EvaluationTime:     threeYearsLater,
		})
		require.NoError(t, err)
		require.NotNil(t, status)
		assert.True(t, status.Valid)
		assert.Contains(t, status.Message, "Apache License 2.0")
		assert.Contains(t, status.Message, "2029-09-11")
	})

	t.Run("Layer 3: Unattested custom build does not convert to Apache 2.0 even after 4 years", func(t *testing.T) {
		version.ReleaseSignature = "none" // remove signature
		fourYearsLater := time.Date(2030, 9, 12, 0, 0, 0, 0, time.UTC)
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 100,
			IsDryRun:           false,
			EvaluationTime:     fourYearsLater,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "COMMERCIAL LICENSE REQUIRED")
		assert.Nil(t, status)
	})
}

func TestEnforce_Layer2_GitLabServerTimeAttestation(t *testing.T) {
	origBuildDate := version.BuildDate
	origVersion := version.Version
	origSig := version.ReleaseSignature
	defer func() {
		version.BuildDate = origBuildDate
		version.Version = origVersion
		version.ReleaseSignature = origSig
	}()

	version.Version = "0.4.0"
	version.BuildDate = "2026-09-11T00:00:00Z"

	version.SetReleaseVerificationPublicKey(staticReleasePubKey)
	defer version.ResetReleaseVerificationPublicKey()

	relToken, err := version.SignRelease(&version.ReleaseClaims{
		Version:   "0.4.0",
		GitCommit: version.GitCommit,
		BuildDate: "2026-09-11T00:00:00Z",
		Authority: "DIVMORA Technologies",
	}, staticReleasePrivKey)
	require.NoError(t, err)
	version.ReleaseSignature = relToken

	t.Run("Forward Clock Tampering Foiled: local clock set to 2030, but GitLab server Date is 2026", func(t *testing.T) {
		tamperedLocalClock := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		authoritativeServerTime := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

		// Over 25 projects without a commercial license
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 100,
			IsDryRun:           false,
			EvaluationTime:     tamperedLocalClock,
			GitLabServerTime:   authoritativeServerTime,
		})

		// Must fail because server time proves Change Date has not yet arrived!
		require.Error(t, err)
		assert.Contains(t, err.Error(), "COMMERCIAL LICENSE REQUIRED")
		assert.Nil(t, status)
	})

	t.Run("Legitimate Change Date: both local clock and GitLab server time have passed 3 years", func(t *testing.T) {
		genuineFutureTime := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 5000,
			IsDryRun:           false,
			EvaluationTime:     genuineFutureTime,
			GitLabServerTime:   genuineFutureTime,
		})

		require.NoError(t, err)
		require.NotNil(t, status)
		assert.True(t, status.Valid)
		assert.Contains(t, status.Message, "Apache License 2.0")
	})

	t.Run("Backward Clock Tampering Foiled: local clock rolled back to 2026, but GitLab server Date proves license expired in 2028", func(t *testing.T) {
		pub := testutil.GetTestPublicKey()
		tamperedLocalClock := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)      // claims unexpired
		authoritativeServerTime := time.Date(2028, 6, 1, 0, 0, 0, 0, time.UTC) // server proves 2028 (expired)

		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 100,
			IsDryRun:           false,
			LicenseKey:         testutil.Expire2027Token,
			PublicKey:          pub,
			EvaluationTime:     tamperedLocalClock,
			GitLabServerTime:   authoritativeServerTime,
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrExpired)
		require.NotNil(t, status)
		assert.False(t, status.Valid)
	})

	t.Run("Zero Server Time: air-gapped run gracefully falls back to local clock", func(t *testing.T) {
		validTime := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 20, // <= 25 community tier
			IsDryRun:           false,
			EvaluationTime:     validTime,
			GitLabServerTime:   time.Time{}, // Zero server time
		})

		require.NoError(t, err)
		require.NotNil(t, status)
		assert.True(t, status.Valid)
		assert.Contains(t, status.Message, "Free Community Tier")
	})
}

func TestVersionLocking_Enforcement(t *testing.T) {
	pubKey := testutil.GetTestPublicKey()
	license.SetVerificationPublicKey(pubKey)
	defer license.ResetVerificationPublicKey()

	origVer := version.Version
	defer func() { version.Version = origVer }()

	token := testutil.VersionLockedToken

	t.Run("Entitled version 0.3.0 passes", func(t *testing.T) {
		version.Version = "0.3.0"
		status, err := license.ParseAndVerify(token, pubKey)
		require.NoError(t, err)
		assert.True(t, status.Valid)
	})

	t.Run("Unentitled major upgrade v1.0.0 fails with ErrVersionNotEntitled", func(t *testing.T) {
		version.Version = "1.0.0"
		status, err := license.ParseAndVerify(token, pubKey)
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrVersionNotEntitled)
		assert.False(t, status.Valid)
	})

	t.Run("Development build 'dev' is exempt and passes", func(t *testing.T) {
		version.Version = "dev"
		status, err := license.ParseAndVerify(token, pubKey)
		require.NoError(t, err)
		assert.True(t, status.Valid)
	})
}

func TestPerpetualLicense_MaintenanceCutoff(t *testing.T) {
	pubKey := testutil.GetTestPublicKey()
	license.SetVerificationPublicKey(pubKey)
	defer license.ResetVerificationPublicKey()

	origDate := version.BuildDate
	defer func() { version.BuildDate = origDate }()

	token := testutil.PerpetualMaintenanceToken

	t.Run("Binary built during maintenance window passes", func(t *testing.T) {
		version.BuildDate = "2026-11-15T12:00:00Z"
		evalTime := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
		status, err := license.ParseAndVerifyAt(token, pubKey, evalTime)
		require.NoError(t, err)
		assert.True(t, status.Valid)
	})

	t.Run("Binary built after maintenance cutoff fails with ErrMaintenanceExpired", func(t *testing.T) {
		version.BuildDate = "2027-05-01T12:00:00Z"
		evalTime := time.Date(2027, 5, 5, 0, 0, 0, 0, time.UTC)
		status, err := license.ParseAndVerifyAt(token, pubKey, evalTime)
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrMaintenanceExpired)
		assert.False(t, status.Valid)
	})

	t.Run("Development build with 'unknown' build date is exempt and passes", func(t *testing.T) {
		version.BuildDate = "unknown"
		status, err := license.ParseAndVerify(token, pubKey)
		require.NoError(t, err)
		assert.True(t, status.Valid)
	})
}

func TestKeyRing_MultiKeyRotationAndRevocation(t *testing.T) {
	pub1 := testutil.GetTestPublicKey()
	pub2 := testutil.GetTestPublicKey2()

	bundlePEM, err := liblicense.EncodePublicKeysToPEM([]ed25519.PublicKey{pub1, pub2})
	require.NoError(t, err)
	t.Setenv("DIVMORA_PUBLIC_KEYS_PEM", string(bundlePEM))

	liblicense.SetAllowEnvKeyOverride(true)
	defer liblicense.ResetAllowEnvKeyOverride()

	token1 := testutil.KeyRingToken1
	token2 := testutil.KeyRingToken2

	// Both tokens must verify successfully against the KeyRing bundle without explicit public key passed
	status1, err := license.ParseAndVerify(token1, nil)
	require.NoError(t, err)
	assert.True(t, status1.Valid)

	status2, err := license.ParseAndVerify(token2, nil)
	require.NoError(t, err)
	assert.True(t, status2.Valid)

	// Test revocation in KeyRing
	ring, err := license.GetVerificationKeyRing()
	require.NoError(t, err)
	require.Equal(t, 2, ring.Count())

	// Revoke Key 2 by its fingerprint
	fp2 := liblicense.KeyFingerprint(pub2)
	err = ring.Revoke(fp2)
	require.NoError(t, err)

	val, err := liblicense.NewValidatorWithKeyRing(ring, liblicense.WithProduct("gitlab-fleet-governor"))
	require.NoError(t, err)

	// Token 1 remains valid
	_, err = val.Verify(token1)
	assert.NoError(t, err)

	// Token 2 is rejected because Key 2 is revoked
	_, err = val.Verify(token2)
	assert.ErrorIs(t, err, liblicense.ErrKeyRevoked)
}

func TestTierFeatures_CommunityZeroCheckAndCommercialEnforcement(t *testing.T) {
	// 1. IsCommunityFeature taxonomy verification
	communityFeatures := []string{
		"governance.push_rules",
		"governance.protected_branches",
		"governance.project_settings",
		"governance.members",
		"governance.variables",
		"report.table",
		"report.json",
		"report.csv",
		"report.markdown",
		"report.*",
	}
	for _, cf := range communityFeatures {
		assert.True(t, license.IsCommunityFeature(cf), "expected %s to be community feature", cf)
		assert.Equal(t, "community", license.RequiredTierForFeature(cf))
	}

	proFeatures := []string{
		"governance.approval_rules",
		"governance.runners",
		"governance.webhooks",
		"governance.pipeline_retention",
	}
	for _, pf := range proFeatures {
		assert.False(t, license.IsCommunityFeature(pf), "expected %s NOT to be community feature", pf)
		assert.Equal(t, "pro", license.RequiredTierForFeature(pf))
	}

	enterpriseFeatures := []string{
		"governance.compliance_frameworks",
		"audit.run",
		"audit.export.xlsx",
		"audit.smtp",
		"audit.*",
		"runtime.lambda",
	}
	for _, ef := range enterpriseFeatures {
		assert.False(t, license.IsCommunityFeature(ef), "expected %s NOT to be community feature", ef)
		assert.Equal(t, "enterprise", license.RequiredTierForFeature(ef))
	}

	// 2. AssertFeature: Community features bypass with ZERO checks (even on nil status)
	var nilStatus *license.ValidationStatus
	for _, cf := range communityFeatures {
		assert.NoError(t, nilStatus.AssertFeature(cf), "community feature %s must return nil on nil status", cf)
	}

	// Non-community features fail on nil status
	assert.Error(t, nilStatus.AssertFeature("governance.approval_rules"))
	assert.Error(t, nilStatus.AssertFeature("governance.compliance_frameworks"))

	// 3. Generate keypair and sign test tokens for Pro and Enterprise
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signTestToken := func(claims liblicense.Claims) string {
		payloadJSON, err := json.Marshal(claims)
		require.NoError(t, err)

		tempToken := liblicense.EncodeToken(payloadJSON, nil)
		dotIdx := len(tempToken) - 1
		signedData := []byte(tempToken[:dotIdx])

		sig := ed25519.Sign(priv, signedData)
		return liblicense.EncodeToken(payloadJSON, sig)
	}

	proClaims := liblicense.Claims{
		ID:        "lic_test_pro_123",
		Product:   "gitlab-fleet-governor",
		Plan:      "pro",
		Customer:  liblicense.Customer{Name: "Pro Team Corp"},
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(365 * 24 * time.Hour),
		Limits:    map[string]int64{"max_projects": 100},
	}
	proToken := signTestToken(proClaims)

	entClaims := liblicense.Claims{
		ID:        "lic_test_ent_456",
		Product:   "gitlab-fleet-governor",
		Plan:      "enterprise",
		Customer:  liblicense.Customer{Name: "Enterprise Global Corp"},
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(365 * 24 * time.Hour),
		Limits:    map[string]int64{"max_projects": 500},
	}
	entToken := signTestToken(entClaims)

	// 4. Enforce: Community Execution (no license, <= 25 projects, only community features)
	commStatus, err := license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: 10,
		IsDryRun:           false,
		RequiredFeatures:   []string{"governance.push_rules", "governance.protected_branches"},
	})
	require.NoError(t, err)
	require.NotNil(t, commStatus)
	assert.True(t, commStatus.Valid)
	assert.Contains(t, commStatus.Message, "Free Community Tier")

	// 5. Enforce: Unlicensed attempting Pro feature in live mode -> FAILS
	_, err = license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: 10,
		IsDryRun:           false,
		RequiredFeatures:   []string{"governance.approval_rules"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "COMMERCIAL LICENSE REQUIRED")
	assert.Contains(t, err.Error(), "governance.approval_rules")
	assert.Contains(t, err.Error(), "PRO")

	// 6. Enforce: Unlicensed attempting Pro feature in dry-run mode -> WARNS & SUCCEEDS
	dryRunStatus, err := license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: 10,
		IsDryRun:           true,
		RequiredFeatures:   []string{"governance.approval_rules"},
	})
	require.NoError(t, err)
	require.NotNil(t, dryRunStatus)
	assert.True(t, dryRunStatus.Valid)

	// 7. Enforce: Pro License token
	// Pro license authorizes approval rules, runners, webhooks, pipeline retention
	pStatus, err := license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: 50,
		IsDryRun:           false,
		LicenseKey:         proToken,
		PublicKey:          pub,
		RequiredFeatures: []string{
			"governance.approval_rules",
			"governance.runners",
			"governance.webhooks",
			"governance.pipeline_retention",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, pStatus)
	assert.True(t, pStatus.Valid)

	// Pro license attempting Enterprise feature (compliance_frameworks) -> FAILS in live mode
	_, err = license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: 50,
		IsDryRun:           false,
		LicenseKey:         proToken,
		PublicKey:          pub,
		RequiredFeatures:   []string{"governance.compliance_frameworks"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FEATURE NOT ENTITLED")
	assert.Contains(t, err.Error(), "governance.compliance_frameworks")
	assert.Contains(t, err.Error(), "ENTERPRISE")

	// Pro license attempting audit.run (Enterprise) -> FAILS in live mode
	_, err = license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: 50,
		IsDryRun:           false,
		LicenseKey:         proToken,
		PublicKey:          pub,
		RequiredFeatures:   []string{"audit.run"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FEATURE NOT ENTITLED")
	assert.Contains(t, err.Error(), "audit.run")
	assert.Contains(t, err.Error(), "ENTERPRISE")

	// Pro license attempting Enterprise feature in dry-run mode -> WARNS & SUCCEEDS
	dryProStatus, err := license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: 50,
		IsDryRun:           true,
		LicenseKey:         proToken,
		PublicKey:          pub,
		RequiredFeatures:   []string{"governance.compliance_frameworks", "audit.run"},
	})
	require.NoError(t, err)
	require.NotNil(t, dryProStatus)
	assert.True(t, dryProStatus.Valid)

	// 8. Enforce: Enterprise License token
	// Enterprise license authorizes ALL features via wildcard *
	entStatus, err := license.Enforce(license.EnforcementOptions{
		DiscoveredProjects: 200,
		IsDryRun:           false,
		LicenseKey:         entToken,
		PublicKey:          pub,
		RequiredFeatures: []string{
			"governance.approval_rules",
			"governance.runners",
			"governance.webhooks",
			"governance.pipeline_retention",
			"governance.compliance_frameworks",
			"audit.run",
			"audit.export.xlsx",
			"audit.smtp",
			"runtime.lambda",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, entStatus)
	assert.True(t, entStatus.Valid)
}

func TestCRL_RevocationAndResolution(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signTestToken := func(claims liblicense.Claims) string {
		payloadJSON, err := json.Marshal(claims)
		require.NoError(t, err)

		tempToken := liblicense.EncodeToken(payloadJSON, nil)
		dotIdx := len(tempToken) - 1
		signedData := []byte(tempToken[:dotIdx])

		sig := ed25519.Sign(priv, signedData)
		return liblicense.EncodeToken(payloadJSON, sig)
	}

	activeToken := signTestToken(liblicense.Claims{
		ID:        "lic_crl_active_001",
		Product:   "gitlab-fleet-governor",
		Plan:      "enterprise",
		Customer:  liblicense.Customer{Name: "Active Corp"},
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(365 * 24 * time.Hour),
		Limits:    map[string]int64{"max_projects": 500},
	})

	otherToken := signTestToken(liblicense.Claims{
		ID:        "lic_crl_other_002",
		Product:   "gitlab-fleet-governor",
		Plan:      "enterprise",
		Customer:  liblicense.Customer{Name: "Other Corp"},
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(365 * 24 * time.Hour),
		Limits:    map[string]int64{"max_projects": 500},
	})

	crlClaims := liblicense.RevocationListClaims{
		ID:       "crl_test_999",
		Issuer:   "divmora.com/crl",
		Product:  "gitlab-fleet-governor",
		IssuedAt: time.Now().UTC(),
		Entries: []liblicense.RevocationEntry{
			{
				ID:        "lic_crl_active_001",
				RevokedAt: time.Now().UTC().Add(-1 * time.Hour),
				Reason:    "customer requested cancellation",
			},
		},
	}
	crlArmored, err := liblicense.SignCRLArmored(crlClaims, priv)
	require.NoError(t, err)

	crlCompact, err := liblicense.SignCRL(crlClaims, priv)
	require.NoError(t, err)

	t.Run("Without CRL: active token passes verification", func(t *testing.T) {
		status, err := license.ParseAndVerify(activeToken, pub)
		require.NoError(t, err)
		assert.True(t, status.Valid)
	})

	t.Run("With armored CRL passed explicitly: active token is revoked", func(t *testing.T) {
		status, err := license.ParseAndVerify(activeToken, pub, crlArmored)
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrLicenseRevoked)
		var revokedErr *liblicense.LicenseRevokedError
		require.ErrorAs(t, err, &revokedErr)
		assert.Equal(t, "lic_crl_active_001", revokedErr.LicenseID)
		assert.Equal(t, "customer requested cancellation", revokedErr.Reason)
		assert.Equal(t, "crl_test_999", revokedErr.CRLID)
		assert.False(t, status.Valid)
	})

	t.Run("With compact CRL passed explicitly: active token is revoked", func(t *testing.T) {
		status, err := license.ParseAndVerify(activeToken, pub, crlCompact)
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrLicenseRevoked)
		assert.False(t, status.Valid)
	})

	t.Run("Other non-revoked token passes with same CRL", func(t *testing.T) {
		status, err := license.ParseAndVerify(otherToken, pub, crlArmored)
		require.NoError(t, err)
		assert.True(t, status.Valid)
	})

	t.Run("Enforce with revoked token in production mode fails with clean error", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 50,
			IsDryRun:           false,
			LicenseKey:         activeToken,
			PublicKey:          pub,
			CRL:                crlArmored,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrLicenseRevoked)
		assert.Contains(t, err.Error(), "COMMERCIAL LICENSE REVOKED")
		assert.False(t, status.Valid)
	})

	t.Run("Enforce with revoked token in dry-run mode falls back to simulation exemption", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 50,
			IsDryRun:           true,
			LicenseKey:         activeToken,
			PublicKey:          pub,
			CRL:                crlArmored,
		})
		require.NoError(t, err)
		require.NotNil(t, status)
		assert.True(t, status.Valid)
		assert.Contains(t, status.Message, "Simulation / dry-run mode exempted")
	})

	t.Run("CRL File resolution via CRLFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		crlFile := filepath.Join(tmpDir, "test.divcrl")
		err := os.WriteFile(crlFile, []byte(crlArmored), 0600)
		require.NoError(t, err)

		status, err := license.ParseAndVerify(activeToken, pub, crlFile)
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrLicenseRevoked)
		assert.False(t, status.Valid)

		statusEnforce, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 50,
			IsDryRun:           false,
			LicenseKey:         activeToken,
			PublicKey:          pub,
			CRLFile:            crlFile,
		})
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrLicenseRevoked)
		assert.Contains(t, err.Error(), "COMMERCIAL LICENSE REVOKED")
		assert.False(t, statusEnforce.Valid)
	})

	t.Run("CRL resolution via DIVMORA_CRL env var", func(t *testing.T) {
		t.Setenv("DIVMORA_CRL", crlArmored)
		status, err := license.ParseAndVerify(activeToken, pub)
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrLicenseRevoked)
		assert.False(t, status.Valid)
	})

	t.Run("CRL resolution via DIVMORA_CRL_FILE env var", func(t *testing.T) {
		tmpDir := t.TempDir()
		crlFile := filepath.Join(tmpDir, "env_test.divcrl")
		err := os.WriteFile(crlFile, []byte(crlArmored), 0600)
		require.NoError(t, err)

		t.Setenv("DIVMORA_CRL", "")
		t.Setenv("DIVMORA_CRL_FILE", crlFile)
		status, err := license.ParseAndVerify(activeToken, pub)
		require.Error(t, err)
		assert.ErrorIs(t, err, liblicense.ErrLicenseRevoked)
		assert.False(t, status.Valid)
	})

	t.Run("ResolveCRL returns empty without error when no CRL is configured", func(t *testing.T) {
		t.Setenv("DIVMORA_CRL", "")
		t.Setenv("DIVMORA_CRL_FILE", "")
		crl, err := license.ResolveCRL()
		require.NoError(t, err)
		assert.Empty(t, crl)
	})

	t.Run("ResolveCRL returns error when explicit non-existent file is specified", func(t *testing.T) {
		_, err := license.ResolveCRL("/nonexistent/path/to/revocations.divcrl")
		require.Error(t, err)
	})
}
