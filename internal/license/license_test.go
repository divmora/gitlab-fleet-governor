package license_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/divmora/gitlab-fleet-governor/internal/license"
	"github.com/divmora/gitlab-fleet-governor/pkg/version"
	liblicense "github.com/divmora/license-go/pkg/license"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func TestSignAndVerify_Success(t *testing.T) {
	pub, priv := generateTestKeyPair(t)

	claims := &license.Claims{
		ID: "lic_test_12345",
		Customer: license.Customer{
			Name:  "Fintech Global Corp",
			Email: "lead@fintech.com",
			OrgID: "org_456",
		},
		Product:   "gitlab-fleet-governor",
		Plan:      "enterprise",
		Limits:    map[string]int64{"max_projects": 100},
		Features:  []string{"all"},
		IssuedAt:  time.Now().UTC().Add(-24 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(365 * 24 * time.Hour),
	}

	// Compact DIV1 token
	token, err := license.SignLicense(claims, priv)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.True(t, strings.HasPrefix(token, "DIV1."))

	status, err := license.ParseAndVerify(token, pub)
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
	armored, err := license.SignLicenseArmored(claims, priv)
	require.NoError(t, err)
	assert.Contains(t, armored, "-----BEGIN DIVMORA LICENSE KEY-----")

	statusArmored, err := license.ParseAndVerify(armored, pub)
	require.NoError(t, err)
	assert.True(t, statusArmored.Valid)
	assert.Equal(t, "lic_test_12345", statusArmored.Claims.ID)
}

func TestVerify_WithinGracePeriod(t *testing.T) {
	pub, priv := generateTestKeyPair(t)

	// Expired 5 days ago, grace period is 14 days
	claims := &license.Claims{
		ID: "lic_grace_123",
		Customer: license.Customer{
			Name: "Grace Corp",
		},
		Product:         "gitlab-fleet-governor",
		Plan:            "enterprise",
		Limits:          map[string]int64{"max_projects": 50},
		IssuedAt:        time.Now().UTC().Add(-400 * 24 * time.Hour),
		ExpiresAt:       time.Now().UTC().Add(-5 * 24 * time.Hour),
		GracePeriodDays: 14,
	}

	token, err := license.SignLicense(claims, priv)
	require.NoError(t, err)

	status, err := license.ParseAndVerify(token, pub)
	require.NoError(t, err)
	assert.True(t, status.Valid)
	assert.True(t, status.InGracePeriod)
	assert.Contains(t, status.Message, "operating within 14-day grace period")
}

func TestVerify_ExpiredBeyondGracePeriod(t *testing.T) {
	pub, priv := generateTestKeyPair(t)

	// Expired 20 days ago, grace period is 14 days
	claims := &license.Claims{
		ID: "lic_expired_123",
		Customer: license.Customer{
			Name: "Expired Corp",
		},
		Product:         "gitlab-fleet-governor",
		Plan:            "enterprise",
		Limits:          map[string]int64{"max_projects": 50},
		IssuedAt:        time.Now().UTC().Add(-400 * 24 * time.Hour),
		ExpiresAt:       time.Now().UTC().Add(-20 * 24 * time.Hour),
		GracePeriodDays: 14,
	}

	token, err := license.SignLicense(claims, priv)
	require.NoError(t, err)

	status, err := license.ParseAndVerify(token, pub)
	require.Error(t, err)
	assert.False(t, status.Valid)
}

func TestVerify_TamperedToken(t *testing.T) {
	pub, priv := generateTestKeyPair(t)

	claims := &license.Claims{
		ID: "lic_valid",
		Customer: license.Customer{
			Name: "Legit Corp",
		},
		Product:   "gitlab-fleet-governor",
		Plan:      "enterprise",
		Limits:    map[string]int64{"max_projects": 100},
		ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour),
	}

	token, err := license.SignLicense(claims, priv)
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	// Tamper payload (parts[1])
	tamperedToken := parts[0] + "." + parts[1] + "xyz." + parts[2]
	_, err = license.ParseAndVerify(tamperedToken, pub)
	require.Error(t, err)

	// Tamper signature (parts[2])
	tamperedSigToken := parts[0] + "." + parts[1] + ".AAAA" + parts[2][4:]
	_, err = license.ParseAndVerify(tamperedSigToken, pub)
	require.Error(t, err)
}

func TestEnforce_Scenarios(t *testing.T) {
	pub, priv := generateTestKeyPair(t)

	validClaims := &license.Claims{
		ID: "lic_enforce_test",
		Customer: license.Customer{
			Name: "Acme Fleet",
		},
		Product:   "gitlab-fleet-governor",
		Plan:      "enterprise",
		Limits:    map[string]int64{"max_projects": 100},
		ExpiresAt: time.Now().UTC().Add(90 * 24 * time.Hour),
	}
	validToken, err := license.SignLicense(validClaims, priv)
	require.NoError(t, err)

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
			LicenseKey:         validToken,
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
		assert.Equal(t, "lic_enforce_test", status.Claims.ID)
	})

	t.Run("Capacity Exceeded: 150 projects with 100-project license fails", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 150,
			IsDryRun:           false,
			LicenseKey:         validToken,
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
			LicenseKey:         validToken,
			PublicKey:          pub,
		})
		require.NoError(t, err)
		assert.True(t, status.Valid)
		require.NotNil(t, status.Claims)
		assert.Equal(t, "lic_enforce_test", status.Claims.ID)
		assert.Equal(t, "Acme Fleet", status.Claims.Customer.Name)
	})
}

func TestEnforce_PixelvideLicense(t *testing.T) {
	pub, priv := generateTestKeyPair(t)
	evalTime := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)

	claims := &license.Claims{
		ID: "lic_1f9326c6",
		Customer: license.Customer{
			Name:  "PIXELVIDE DESIGN SOLUTIONS LLP",
			Email: "ops@pixelvide.com",
			OrgID: "4147979000000071023",
		},
		Product:         "gitlab-fleet-governor",
		Plan:            "enterprise",
		Limits:          map[string]int64{"max_projects": 500},
		Scope:           &license.Scope{Hosts: []string{"gitlab.pixelvide.com"}},
		Features:        []string{"all"},
		IssuedAt:        time.Date(2026, 9, 11, 11, 36, 14, 0, time.UTC),
		ExpiresAt:       time.Date(2026, 10, 11, 11, 36, 14, 0, time.UTC),
		GracePeriodDays: 14,
	}

	pixelvideToken, err := license.SignLicense(claims, priv)
	require.NoError(t, err)

	t.Run("DryRun Mode with 437 projects", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 437,
			IsDryRun:           true,
			LicenseKey:         pixelvideToken,
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
			LicenseKey:         pixelvideToken,
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
	pub, priv := generateTestKeyPair(t)

	tests := []struct {
		name          string
		claimProduct  string
		expectAllowed bool
		expectErr     string
	}{
		{
			name:          "Wildcard product (*)",
			claimProduct:  "*",
			expectAllowed: true,
		},
		{
			name:          "DIVMORA suite license",
			claimProduct:  "divmora-suite",
			expectAllowed: true,
		},
		{
			name:          "Matching product gitlab-fleet-governor",
			claimProduct:  "gitlab-fleet-governor",
			expectAllowed: true,
		},
		{
			name:          "Unrecognized alias fleet-governor rejected",
			claimProduct:  "fleet-governor",
			expectAllowed: false,
			expectErr:     "license: product mismatch",
		},
		{
			name:          "Case-insensitive matching",
			claimProduct:  "GitLab-Fleet-Governor",
			expectAllowed: true,
		},
		{
			name:          "Mismatched product (github-fleet-governor)",
			claimProduct:  "github-fleet-governor",
			expectAllowed: false,
			expectErr:     "license: product mismatch",
		},
		{
			name:          "Mismatched product (cloud-compliance-engine)",
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
				Customer: license.Customer{
					Name: "Acme Corp",
				},
				Plan:      "enterprise",
				IssuedAt:  time.Now().UTC().Add(-1 * time.Hour),
				ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour),
			}

			// Direct helper check
			assert.Equal(t, tc.expectAllowed, claims.IsValidForProduct("gitlab-fleet-governor"))

			// Cryptographic parse & verify check
			tok, err := license.SignLicense(claims, priv)
			require.NoError(t, err)

			status, err := license.ParseAndVerify(tok, pub)
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
		// Apex domain matching (v0.5.0)
		assert.True(t, claims.IsHostAllowed("internal.net"))
		// URL input with port and path (v0.5.0)
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

	t.Run("Plain Root Group Matching Without Wildcards (v0.5.0)", func(t *testing.T) {
		claims := &license.Claims{
			Scope: &license.Scope{Namespaces: []string{"devops"}},
		}

		// Plain group authorizes root and deep child paths
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
	pub, priv := generateTestKeyPair(t)

	// Token restricted to gitlab.com and "enterprise-org"
	token, err := license.SignLicense(&license.Claims{
		ID: "lic_scoped_test",
		Customer: license.Customer{
			Name: "Enterprise Scoped Corp",
		},
		Product: "gitlab-fleet-governor",
		Plan:    "enterprise",
		Limits:  map[string]int64{"max_projects": 100},
		Scope: &license.Scope{
			Hosts:      []string{"gitlab.com"},
			Namespaces: []string{"enterprise-org", "enterprise-org/*"},
		},
		IssuedAt:  time.Now().UTC().Add(-1 * time.Hour),
		ExpiresAt: time.Now().UTC().Add(365 * 24 * time.Hour),
	}, priv)
	require.NoError(t, err)

	t.Run("Permitted within valid host and group scope", func(t *testing.T) {
		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 50,
			IsDryRun:           false,
			GitLabBaseURL:      "https://gitlab.com/api/v4",
			TargetPaths:        []string{"enterprise-org/backend", "enterprise-org/frontend"},
			LicenseKey:         token,
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
			LicenseKey:         token,
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
			LicenseKey:         token,
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

	pubRel, privRel := generateTestKeyPair(t)
	version.SetReleaseVerificationPublicKey(pubRel)
	defer version.ResetReleaseVerificationPublicKey()

	token, err := version.SignRelease(&version.ReleaseClaims{
		Version:   "0.4.0",
		GitCommit: version.GitCommit,
		BuildDate: "2026-09-11T00:00:00Z",
		Authority: "DIVMORA Technologies",
	}, privRel)
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

	pubKey, privKey := generateTestKeyPair(t)
	version.SetReleaseVerificationPublicKey(pubKey)
	defer version.ResetReleaseVerificationPublicKey()

	relToken, err := version.SignRelease(&version.ReleaseClaims{
		Version:   "0.4.0",
		GitCommit: version.GitCommit,
		BuildDate: "2026-09-11T00:00:00Z",
		Authority: "DIVMORA Technologies",
	}, privKey)
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
		claims := &license.Claims{
			ID: "lic_expire_test",
			Customer: license.Customer{
				Name: "Sneaky Corp",
			},
			Product:         "gitlab-fleet-governor",
			Plan:            "enterprise",
			Limits:          map[string]int64{"max_projects": 500},
			IssuedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			ExpiresAt:       time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			GracePeriodDays: 14,
			Scope:           &license.Scope{Hosts: []string{"*"}},
			Features:        []string{"all"},
		}

		token, err := license.SignLicense(claims, privKey)
		require.NoError(t, err)

		tamperedLocalClock := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)      // claims unexpired
		authoritativeServerTime := time.Date(2028, 6, 1, 0, 0, 0, 0, time.UTC) // server proves 2028 (expired)

		status, err := license.Enforce(license.EnforcementOptions{
			DiscoveredProjects: 100,
			IsDryRun:           false,
			LicenseKey:         token,
			PublicKey:          pubKey,
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
	pubKey, privKey := generateTestKeyPair(t)
	license.SetVerificationPublicKey(pubKey)
	defer license.ResetVerificationPublicKey()

	origVer := version.Version
	defer func() { version.Version = origVer }()

	claims := &license.Claims{
		ID:         "lic_ver_lock",
		Product:    "gitlab-fleet-governor",
		Customer:   license.Customer{Name: "Version Lock Corp"},
		Plan:       "enterprise",
		MaxVersion: "0.*",
		IssuedAt:   time.Now().UTC(),
		ExpiresAt:  time.Now().UTC().AddDate(1, 0, 0),
		Limits:     map[string]int64{"max_projects": 100},
	}

	token, err := license.SignLicense(claims, privKey)
	require.NoError(t, err)

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
	pubKey, privKey := generateTestKeyPair(t)
	license.SetVerificationPublicKey(pubKey)
	defer license.ResetVerificationPublicKey()

	origDate := version.BuildDate
	defer func() { version.BuildDate = origDate }()

	maintenanceCutoff := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	claims := &license.Claims{
		ID:                   "lic_perpetual_maint",
		Product:              "gitlab-fleet-governor",
		Customer:             license.Customer{Name: "Perpetual Corp"},
		Plan:                 "enterprise",
		IssuedAt:             time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC),
		MaintenanceExpiresAt: maintenanceCutoff,
		// ExpiresAt is zero (perpetual)
		Limits: map[string]int64{"max_projects": 500},
	}

	token, err := license.SignLicense(claims, privKey)
	require.NoError(t, err)

	t.Run("Binary built during maintenance window passes", func(t *testing.T) {
		version.BuildDate = "2026-11-15T12:00:00Z"
		status, err := license.ParseAndVerify(token, pubKey)
		require.NoError(t, err)
		assert.True(t, status.Valid)
	})

	t.Run("Binary built after maintenance cutoff fails with ErrMaintenanceExpired", func(t *testing.T) {
		version.BuildDate = "2027-05-01T12:00:00Z"
		status, err := license.ParseAndVerify(token, pubKey)
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
	pub1, priv1 := generateTestKeyPair(t)
	pub2, priv2 := generateTestKeyPair(t)

	bundlePEM, err := liblicense.EncodePublicKeysToPEM([]ed25519.PublicKey{pub1, pub2})
	require.NoError(t, err)
	t.Setenv("DIVMORA_PUBLIC_KEYS_PEM", string(bundlePEM))

	// Issue token with Key 1
	token1, err := license.SignLicense(&license.Claims{
		ID:        "lic_key1",
		Product:   "gitlab-fleet-governor",
		Customer:  license.Customer{Name: "Key 1 Customer"},
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().AddDate(1, 0, 0),
	}, priv1)
	require.NoError(t, err)

	// Issue token with Key 2
	token2, err := license.SignLicense(&license.Claims{
		ID:        "lic_key2",
		Product:   "gitlab-fleet-governor",
		Customer:  license.Customer{Name: "Key 2 Customer"},
		IssuedAt:  time.Now().UTC(),
		ExpiresAt: time.Now().UTC().AddDate(1, 0, 0),
	}, priv2)
	require.NoError(t, err)

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
