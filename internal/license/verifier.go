package license

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	liblicense "github.com/divmora/license-go/pkg/license"

	"github.com/divmora/gitlab-fleet-governor/pkg/version"
)

// ParseAndVerify decodes, parses, and cryptographically verifies an Ed25519 signed license token
// against the current system time.
// If pubKey is nil or empty, DefaultPublicKeyBase64 is used as the immutable root of trust.
// Optional crlSources can be provided as inline CRL tokens or file paths. If omitted,
// standard environment variables (DIVMORA_CRL, DIVMORA_CRL_FILE) and system path (/etc/divmora/crl.divcrl)
// are checked.
func ParseAndVerify(token string, pubKey ed25519.PublicKey, crlSources ...string) (*ValidationStatus, error) {
	return ParseAndVerifyAt(token, pubKey, time.Now().UTC(), crlSources...)
}

// ParseAndVerifyAt decodes, parses, and cryptographically verifies an Ed25519 signed license token
// against a specified evaluation time.
// If pubKey is nil or empty, DefaultPublicKeyBase64 is used as the immutable root of trust.
// Optional crlSources can be provided as inline CRL tokens or file paths. If omitted,
// standard environment variables (DIVMORA_CRL, DIVMORA_CRL_FILE) and system path (/etc/divmora/crl.divcrl)
// are checked.
func ParseAndVerifyAt(token string, pubKey ed25519.PublicKey, evalTime time.Time, crlSources ...string) (*ValidationStatus, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("license token cannot be empty")
	}

	var validatorOpts []liblicense.ValidatorOption
	validatorOpts = append(validatorOpts, liblicense.WithProduct("gitlab-fleet-governor"))
	validatorOpts = append(validatorOpts, liblicense.WithTierFeatures(DefaultTierFeatures))
	if fp := strings.TrimSpace(os.Getenv("DIVMORA_FINGERPRINT")); fp != "" {
		validatorOpts = append(validatorOpts, liblicense.WithExpectedFingerprint(fp))
	}

	// 1. Software Version Enforcement:
	// Only enforce version constraints if the binary has an authoritative release version.
	// Local "dev" builds are exempt to prevent breaking local testing and developer workflows.
	vInfo := version.Get()
	if vInfo.Version != "" && vInfo.Version != "dev" {
		validatorOpts = append(validatorOpts, liblicense.WithCurrentVersion(vInfo.Version))
	}

	// 2. Maintenance / Support Update Cutoff Enforcement:
	// Only enforce maintenance cutoffs if the binary has an authoritative release timestamp.
	// If BuildDate is "unknown", ReleaseTime() returns false and the check is omitted.
	if releaseTime, ok := vInfo.ReleaseTime(); ok {
		validatorOpts = append(validatorOpts, liblicense.WithBuildDate(releaseTime))
	}

	// 3. Certificate Revocation List (CRL) Enforcement:
	// Resolve CRL from explicit sources (CLI flags / config file), environment variables,
	// or standard system file paths (/etc/divmora/crl.divcrl).
	crlData, err := ResolveCRL(crlSources...)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve certificate revocation list (CRL): %w", err)
	}
	if crlData != "" {
		validatorOpts = append(validatorOpts, liblicense.WithRevocationList(crlData))
	}

	var validator *liblicense.Validator
	if len(pubKey) > 0 {
		validator, err = liblicense.NewValidator(pubKey, validatorOpts...)
	} else {
		validator, err = liblicense.NewValidatorWithFallbackKey(DefaultPublicKeyBase64, validatorOpts...)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to initialize license validator: %w", err)
	}

	if evalTime.IsZero() {
		evalTime = time.Now().UTC()
	}

	res, err := validator.VerifyWithResultAt(token, evalTime)
	if err != nil {
		var scopeErr *liblicense.ScopeMismatchError
		if errors.As(err, &scopeErr) {
			// Token signature, product, and expiration are verified; scope is evaluated by Enforce against active targets.
			claims, inspectErr := liblicense.Inspect(token)
			if inspectErr == nil {
				claims.SetTierFeatures(DefaultTierFeatures)
				daysRemaining := claims.DaysRemaining()
				inGrace := claims.IsInGracePeriodAt(evalTime)
				if inGrace {
					daysRemaining = claims.GraceDaysRemainingAt(evalTime)
				}
				var msg string
				if inGrace {
					msg = fmt.Sprintf("License expired on %s; currently operating within %d-day grace period (%d days remaining)",
						claims.ExpiresAt.Format("2006-01-02"), claims.GracePeriodDays, daysRemaining)
				} else if claims.IsPerpetual() {
					msg = "Perpetual commercial license is valid and active"
				} else {
					msg = fmt.Sprintf("License is valid and active (%d days remaining)", daysRemaining)
				}
				return &ValidationStatus{
					Valid:         true,
					InGracePeriod: inGrace,
					DaysRemaining: daysRemaining,
					Message:       msg,
					Claims:        claims,
				}, nil
			}
		}

		claims, _ := liblicense.Inspect(token)
		if claims != nil {
			claims.SetTierFeatures(DefaultTierFeatures)
		}
		return &ValidationStatus{
			Valid:   false,
			Message: err.Error(),
			Claims:  claims,
		}, err
	}

	daysRemaining := res.Claims.DaysRemaining()
	if res.InGracePeriod {
		daysRemaining = res.GraceDaysRemaining
	}

	var msg string
	if res.InGracePeriod {
		msg = fmt.Sprintf("License expired on %s; currently operating within %d-day grace period (%d days remaining)",
			res.Claims.ExpiresAt.Format("2006-01-02"), res.Claims.GracePeriodDays, res.GraceDaysRemaining)
	} else if res.Claims.IsPerpetual() {
		msg = "Perpetual commercial license is valid and active"
	} else {
		msg = fmt.Sprintf("License is valid and active (%d days remaining)", daysRemaining)
	}

	return &ValidationStatus{
		Valid:         true,
		InGracePeriod: res.InGracePeriod,
		DaysRemaining: daysRemaining,
		Message:       msg,
		Claims:        res.Claims,
	}, nil
}

// ResolveCRL determines the active Certificate Revocation List (CRL) contents from explicit
// token strings, file paths, environment variables (DIVMORA_CRL, DIVMORA_CRL_FILE), or
// the standard system path (/etc/divmora/crl.divcrl).
// If no CRL is configured anywhere, it returns ("", nil) because CRL checking is optional.
func ResolveCRL(sources ...string) (string, error) {
	var explicit []string
	for _, s := range sources {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			explicit = append(explicit, trimmed)
		}
	}

	resolved, err := liblicense.ResolveCRL(explicit...)
	if err != nil {
		if err.Error() == "license: crl not found" {
			return "", nil
		}
		return "", err
	}
	return resolved.Content, nil
}
