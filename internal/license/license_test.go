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
		Tier:        "enterprise",
		MaxProjects: 100,
		Features:    []string{"all"},
		IssuedAt:    time.Now().UTC().Add(-24 * time.Hour),
		ExpiresAt:   time.Now().UTC().Add(365 * 24 * time.Hour),
	}

	token, err := license.SignLicense(claims, priv)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Contains(t, token, ".")

	status, err := license.ParseAndVerify(token, pub)
	require.NoError(t, err)
	require.NotNil(t, status)
	assert.True(t, status.Valid)
	assert.False(t, status.InGracePeriod)
	assert.True(t, status.DaysRemaining >= 364)
	assert.Equal(t, "lic_test_12345", status.Claims.ID)
	assert.Equal(t, "Fintech Global Corp", status.Claims.Customer.Name)
	assert.Equal(t, "enterprise", status.Claims.Tier)
	assert.True(t, status.Claims.HasFeature("cloud_secrets"))
}

func TestVerify_WithinGracePeriod(t *testing.T) {
	pub, priv := generateTestKeyPair(t)

	// Expired 5 days ago, grace period is 14 days
	claims := &license.Claims{
		ID: "lic_grace_123",
		Customer: license.Customer{
			Name: "Grace Corp",
		},
		Tier:            "enterprise",
		MaxProjects:     50,
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
		Tier:            "enterprise",
		MaxProjects:     50,
		IssuedAt:        time.Now().UTC().Add(-400 * 24 * time.Hour),
		ExpiresAt:       time.Now().UTC().Add(-20 * 24 * time.Hour),
		GracePeriodDays: 14,
	}

	token, err := license.SignLicense(claims, priv)
	require.NoError(t, err)

	status, err := license.ParseAndVerify(token, pub)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "grace period of 14 days has elapsed")
	assert.False(t, status.Valid)
}

func TestVerify_TamperedToken(t *testing.T) {
	pub, priv := generateTestKeyPair(t)

	claims := &license.Claims{
		ID: "lic_valid",
		Customer: license.Customer{
			Name: "Legit Corp",
		},
		Tier:        "enterprise",
		MaxProjects: 100,
		ExpiresAt:   time.Now().UTC().Add(30 * 24 * time.Hour),
	}

	token, err := license.SignLicense(claims, priv)
	require.NoError(t, err)

	parts := strings.Split(token, ".")
	require.Len(t, parts, 2)

	// Tamper payload
	tamperedToken := parts[0] + "xyz." + parts[1]
	_, err = license.ParseAndVerify(tamperedToken, pub)
	require.Error(t, err)

	// Tamper signature
	tamperedSigToken := parts[0] + ".AAAA" + parts[1][4:]
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
		Tier:        "enterprise",
		MaxProjects: 100,
		ExpiresAt:   time.Now().UTC().Add(90 * 24 * time.Hour),
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
}

func TestResolveToken_FromFilesAndEnv(t *testing.T) {
	tempDir := t.TempDir()
	keyFile := filepath.Join(tempDir, "license.key")
	err := os.WriteFile(keyFile, []byte("  token-from-file  \n"), 0600)
	require.NoError(t, err)

	// Direct key
	k1, err := license.ResolveToken("direct-token", "")
	require.NoError(t, err)
	assert.Equal(t, "direct-token", k1)

	// File path
	k2, err := license.ResolveToken("", keyFile)
	require.NoError(t, err)
	assert.Equal(t, "token-from-file", k2)

	// Environment variable
	t.Setenv("FLEET_LICENSE_KEY", "token-from-env")
	k3, err := license.ResolveToken("", "")
	require.NoError(t, err)
	assert.Equal(t, "token-from-env", k3)

	// Environment variable file
	t.Setenv("FLEET_LICENSE_KEY", "")
	t.Setenv("FLEET_LICENSE_FILE", keyFile)
	k4, err := license.ResolveToken("", "")
	require.NoError(t, err)
	assert.Equal(t, "token-from-file", k4)
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

		starClaims := &license.Claims{AllowedHosts: []string{"*"}}
		assert.True(t, starClaims.IsHostAllowed("https://gitlab.com"))
		assert.True(t, starClaims.IsHostAllowed("https://gitlab.private.corp"))
	})

	t.Run("Exact Host Matching", func(t *testing.T) {
		claims := &license.Claims{
			AllowedHosts: []string{"gitlab.fintech.corp", "gitlab.com"},
		}
		assert.True(t, claims.IsHostAllowed("https://gitlab.fintech.corp/api/v4"))
		assert.True(t, claims.IsHostAllowed("https://gitlab.com/api/v4"))
		assert.False(t, claims.IsHostAllowed("https://gitlab.other.corp/api/v4"))
	})

	t.Run("Subdomain Wildcard Matching", func(t *testing.T) {
		claims := &license.Claims{
			AllowedHosts: []string{"*.internal.net"},
		}
		assert.True(t, claims.IsHostAllowed("https://gitlab.internal.net"))
		assert.True(t, claims.IsHostAllowed("https://staging.internal.net"))
		assert.False(t, claims.IsHostAllowed("https://gitlab.external.com"))
	})
}

func TestGroupValidation_Scoping(t *testing.T) {
	t.Run("Omitted or Wildcard AllowedGroups", func(t *testing.T) {
		emptyClaims := &license.Claims{}
		assert.True(t, emptyClaims.IsGroupAllowed("any-org/repo"))

		starClaims := &license.Claims{AllowedGroups: []string{"*"}}
		assert.True(t, starClaims.IsGroupAllowed("any-org/repo"))
	})

	t.Run("Hierarchy Prefix Matching", func(t *testing.T) {
		claims := &license.Claims{
			AllowedGroups: []string{"acme-corp", "fintech-division"},
		}

		// Exact group match
		assert.True(t, claims.IsGroupAllowed("acme-corp"))
		assert.True(t, claims.IsGroupAllowed("/acme-corp/"))

		// Subgroup and nested projects
		assert.True(t, claims.IsGroupAllowed("acme-corp/billing"))
		assert.True(t, claims.IsGroupAllowed("acme-corp/billing/payments-service"))
		assert.True(t, claims.IsGroupAllowed("fintech-division/core-banking"))

		// Unrelated or partial prefix attacks
		assert.False(t, claims.IsGroupAllowed("acme-corp-spoof/repo"))
		assert.False(t, claims.IsGroupAllowed("other-org/billing"))
	})
}

func TestValidateScope_HostAndGroup(t *testing.T) {
	claims := &license.Claims{
		AllowedHosts:  []string{"gitlab.com"},
		AllowedGroups: []string{"acme-corp"},
	}

	// 1. Success on matching SaaS host and group
	err := claims.ValidateScope("https://gitlab.com/api/v4", []string{
		"acme-corp/billing",
		"acme-corp/infra/terraform",
	})
	require.NoError(t, err)

	// 2. Failure on host mismatch
	err = claims.ValidateScope("https://gitlab.unauthorized.com/api/v4", []string{
		"acme-corp/billing",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "COMMERCIAL LICENSE HOST MISMATCH")
	assert.Contains(t, err.Error(), "gitlab.unauthorized.com")

	// 3. Failure on group mismatch
	err = claims.ValidateScope("https://gitlab.com/api/v4", []string{
		"acme-corp/billing",
		"competitor-org/secret-project",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "COMMERCIAL LICENSE GROUP MISMATCH")
	assert.Contains(t, err.Error(), "competitor-org/secret-project")
}

func TestEnforce_HostAndGroupScoping(t *testing.T) {
	pub, priv := generateTestKeyPair(t)

	// Token restricted to gitlab.com and "enterprise-org"
	token, err := license.SignLicense(&license.Claims{
		ID: "lic_scoped_test",
		Customer: license.Customer{
			Name: "Enterprise Scoped Corp",
		},
		Tier:          "enterprise",
		MaxProjects:   100,
		AllowedHosts:  []string{"gitlab.com"},
		AllowedGroups: []string{"enterprise-org"},
		IssuedAt:      time.Now().UTC().Add(-1 * time.Hour),
		ExpiresAt:     time.Now().UTC().Add(365 * 24 * time.Hour),
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
	defer func() {
		version.BuildDate = origBuildDate
		version.Version = origVersion
	}()

	version.Version = "0.4.0"
	version.BuildDate = "2026-09-11T00:00:00Z"

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
}

func TestEnforce_Layer2_GitLabServerTimeAttestation(t *testing.T) {
	origBuildDate := version.BuildDate
	origVersion := version.Version
	defer func() {
		version.BuildDate = origBuildDate
		version.Version = origVersion
	}()

	version.Version = "0.4.0"
	version.BuildDate = "2026-09-11T00:00:00Z"

	pubKey, privKey := generateTestKeyPair(t)

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
			Tier:            "enterprise",
			MaxProjects:     500,
			IssuedAt:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			ExpiresAt:       time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			GracePeriodDays: 14,
			AllowedHosts:    []string{"*"},
			AllowedGroups:   []string{"*"},
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
		assert.Contains(t, err.Error(), "license expired")
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
