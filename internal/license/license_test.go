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
