package version

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	liblicense "github.com/divmora/license-go/pkg/license"
)

// DefaultReleasePublicKeyBase64 is the embedded production Ed25519 public verification key for DIVMORA Technologies.
const DefaultReleasePublicKeyBase64 = "K8GS3G93kHK5kfav+jxrLwZMIh710EWVyL0tvHLfE5A="

// ReleaseSignature holds the cryptographic release token injected at compile time via -ldflags:
// -X github.com/divmora/gitlab-fleet-governor/pkg/version.ReleaseSignature=<token>
var ReleaseSignature = "none"

var (
	releaseKeyLock     sync.RWMutex
	releaseKeyOverride ed25519.PublicKey
)

// ProvenanceStatus indicates the cryptographic supply-chain verification state of the binary.
type ProvenanceStatus string

const (
	// ProvenanceVerifiedOfficial indicates the binary was officially compiled, attested, and signed by DIVMORA Technologies.
	ProvenanceVerifiedOfficial ProvenanceStatus = "VERIFIED_OFFICIAL_RELEASE"

	// ProvenanceUnattestedCustom indicates a community or custom compilation lacking an official DIVMORA release signature.
	ProvenanceUnattestedCustom ProvenanceStatus = "UNATTESTED_CUSTOM_BUILD"

	// ProvenanceTamperedMetadata indicates an official signature was provided, but the compiled metadata (Version, Commit, or BuildDate) differs from the signed payload.
	ProvenanceTamperedMetadata ProvenanceStatus = "TAMPERED_METADATA"

	// ProvenanceTamperedSignature indicates the signature is corrupted or was signed with an unauthorized key.
	ProvenanceTamperedSignature ProvenanceStatus = "TAMPERED_SIGNATURE"
)

// ReleaseClaims represents the canonical metadata envelope signed by DIVMORA Technologies.
type ReleaseClaims struct {
	Product      string `json:"product,omitempty"`
	Version      string `json:"version"`
	GitCommit    string `json:"git_commit"`
	BuildDate    string `json:"build_date"`
	ReleaseDate  string `json:"release_date,omitempty"`
	BinaryDigest string `json:"binary_digest,omitempty"`
	Authority    string `json:"authority"`
	AuthorityID  string `json:"authority_id,omitempty"`
}

// ReleaseProvenance holds the verified release attestation state and claims.
type ReleaseProvenance struct {
	Status       ProvenanceStatus `json:"status"`
	Verified     bool             `json:"verified"`
	Authority    string           `json:"authority,omitempty"`
	AuthorityID  string           `json:"authority_id,omitempty"`
	Source       string           `json:"source,omitempty"`
	Error        string           `json:"error,omitempty"`
	SignedClaims *ReleaseClaims   `json:"signed_claims,omitempty"`
}

// SetReleaseVerificationPublicKey sets a programmatic override for the release public verification key (primarily used in tests).
func SetReleaseVerificationPublicKey(key ed25519.PublicKey) {
	releaseKeyLock.Lock()
	defer releaseKeyLock.Unlock()
	releaseKeyOverride = key
}

// ResetReleaseVerificationPublicKey resets the release public verification key override.
func ResetReleaseVerificationPublicKey() {
	releaseKeyLock.Lock()
	defer releaseKeyLock.Unlock()
	releaseKeyOverride = nil
}

// GetReleaseVerificationPublicKey resolves the public key used to verify release tokens.
// Resolution order:
// 1. In-memory programmatic override (via SetReleaseVerificationPublicKey).
// 2. DIVMORA_PUBLIC_KEY environment variable (base64-encoded).
// 3. Embedded DefaultReleasePublicKeyBase64.
func GetReleaseVerificationPublicKey() (ed25519.PublicKey, error) {
	releaseKeyLock.RLock()
	if releaseKeyOverride != nil {
		defer releaseKeyLock.RUnlock()
		return releaseKeyOverride, nil
	}
	releaseKeyLock.RUnlock()

	keyStr := os.Getenv("DIVMORA_PUBLIC_KEY")
	if keyStr == "" {
		keyStr = DefaultReleasePublicKeyBase64
	}

	keyBytes, err := base64.StdEncoding.DecodeString(keyStr)
	if err != nil {
		keyBytes, err = base64.RawURLEncoding.DecodeString(keyStr)
		if err != nil {
			return nil, fmt.Errorf("failed to decode release verification public key: %w", err)
		}
	}

	if len(keyBytes) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid release verification public key size: expected %d bytes, got %d", ed25519.PublicKeySize, len(keyBytes))
	}

	return ed25519.PublicKey(keyBytes), nil
}

// SignRelease serializes and cryptographically signs a set of ReleaseClaims using an Ed25519 private key,
// returning a canonical DIVREL1 compact token string ("DIVREL1.<payload>.<signature>").
func SignRelease(claims *ReleaseClaims, privKey ed25519.PrivateKey) (string, error) {
	if claims == nil {
		return "", errors.New("cannot sign nil release claims")
	}
	if len(privKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid Ed25519 private key size: expected %d bytes, got %d", ed25519.PrivateKeySize, len(privKey))
	}
	product := claims.Product
	if product == "" {
		product = "gitlab-fleet-governor"
	}
	authority := claims.Authority
	if authority == "" {
		authority = "DIVMORA Technologies Release Authority"
	}

	bDate, _ := parseAnyDate(claims.BuildDate)
	if bDate.IsZero() {
		bDate = time.Now().UTC()
	}
	rDate, _ := parseAnyDate(claims.ReleaseDate)
	if rDate.IsZero() {
		rDate = bDate
	}

	libClaims := liblicense.ReleaseClaims{
		Product:      product,
		Version:      claims.Version,
		GitCommit:    claims.GitCommit,
		BuildDate:    bDate,
		ReleaseDate:  rDate,
		BinaryDigest: claims.BinaryDigest,
		Authority:    authority,
		KeyID:        claims.AuthorityID,
	}

	payloadJSON, err := json.Marshal(libClaims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal release claims: %w", err)
	}

	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	canonicalData := []byte(fmt.Sprintf("%s.%s", liblicense.ProtocolPrefixRelease, payloadB64))

	sig := ed25519.Sign(privKey, canonicalData)
	return liblicense.EncodeReleaseToken(payloadJSON, sig), nil
}

// SignReleaseArmored signs release claims and formats as an armored PEM block.
func SignReleaseArmored(claims *ReleaseClaims, privKey ed25519.PrivateKey) (string, error) {
	token, err := SignRelease(claims, privKey)
	if err != nil {
		return "", err
	}
	block := &pem.Block{
		Type:  liblicense.PEMTypeReleaseAttestation,
		Bytes: []byte(token),
	}
	return string(pem.EncodeToMemory(block)), nil
}

// ParseAndVerifyReleaseToken decodes, parses, and cryptographically verifies an Ed25519 signed release token.
// It supports canonical DIVREL1 compact tokens, armored PEM blocks, and legacy 2-part tokens.
// If pubKey is nil or empty, GetReleaseVerificationPublicKey() is used.
func ParseAndVerifyReleaseToken(token string, pubKey ed25519.PublicKey) (*ReleaseClaims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("release token cannot be empty")
	}

	if len(pubKey) == 0 {
		var err error
		pubKey, err = GetReleaseVerificationPublicKey()
		if err != nil {
			return nil, err
		}
	}

	// 1. Canonical DIVREL1 or armored PEM block
	if strings.HasPrefix(token, liblicense.ProtocolPrefixRelease+".") || strings.HasPrefix(token, "-----BEGIN") {
		ring := liblicense.NewKeyRing(pubKey)
		libClaims, _, err := liblicense.VerifyRelease(token, ring)
		if err != nil {
			return nil, err
		}
		buildDateStr := ""
		if !libClaims.BuildDate.IsZero() {
			buildDateStr = libClaims.BuildDate.UTC().Format(time.RFC3339)
		}
		releaseDateStr := ""
		if !libClaims.ReleaseDate.IsZero() {
			releaseDateStr = libClaims.ReleaseDate.UTC().Format(time.RFC3339)
		}
		return &ReleaseClaims{
			Product:      libClaims.Product,
			Version:      libClaims.Version,
			GitCommit:    libClaims.GitCommit,
			BuildDate:    buildDateStr,
			ReleaseDate:  releaseDateStr,
			BinaryDigest: libClaims.BinaryDigest,
			Authority:    libClaims.Authority,
			AuthorityID:  libClaims.KeyID,
		}, nil
	}

	// 2. Legacy 2-part token (<payload>.<signature>)
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, errors.New("malformed release token: expected format 'DIVREL1.<payload>.<sig>' or '<payload>.<signature>'")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		payloadBytes, err = base64.StdEncoding.DecodeString(parts[0])
		if err != nil {
			return nil, fmt.Errorf("failed to decode release payload: %w", err)
		}
	}

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		sigBytes, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, fmt.Errorf("failed to decode release signature: %w", err)
		}
	}

	if len(sigBytes) != ed25519.SignatureSize {
		return nil, fmt.Errorf("invalid release signature size: expected %d bytes, got %d", ed25519.SignatureSize, len(sigBytes))
	}

	if !ed25519.Verify(pubKey, payloadBytes, sigBytes) {
		return nil, errors.New("cryptographic signature verification failed: signature does not match public key")
	}

	var claims ReleaseClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, fmt.Errorf("failed to deserialize release claims: %w", err)
	}

	return &claims, nil
}

// ResolveReleaseSignature resolves the cryptographic release token from:
// 1. Compile-time variable ReleaseSignature (if not empty and not placeholder)
// 2. FLEET_RELEASE_SIGNATURE environment variable
// 3. Sidecar file release.sig or gitlab-fleet-governor.sig
func ResolveReleaseSignature() (string, string) {
	// 1. Embedded ldflags
	sig := strings.TrimSpace(ReleaseSignature)
	if isAuthenticSignatureCandidate(sig) {
		return sig, "embedded (ldflags)"
	}

	// 2. Environment variable
	envSig := strings.TrimSpace(os.Getenv("FLEET_RELEASE_SIGNATURE"))
	if isAuthenticSignatureCandidate(envSig) {
		return envSig, "environment (FLEET_RELEASE_SIGNATURE)"
	}

	// 3. Sidecar file candidates
	candidates := []string{
		"release.sig",
		"gitlab-fleet-governor.sig",
	}

	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates = append(candidates,
			filepath.Join(execDir, "release.sig"),
			filepath.Join(execDir, "gitlab-fleet-governor.sig"),
		)
	}

	for _, path := range candidates {
		if content, err := os.ReadFile(path); err == nil {
			s := strings.TrimSpace(string(content))
			if isAuthenticSignatureCandidate(s) {
				return s, fmt.Sprintf("sidecar file (%s)", path)
			}
		}
	}

	return "", "none"
}

func isAuthenticSignatureCandidate(s string) bool {
	return !liblicense.IsPlaceholderAttestation(s)
}

// EvaluateProvenance inspects the binary's release signature and verifies that the compiled
// Version, GitCommit, and BuildDate match the signed claims.
func (i Info) EvaluateProvenance(pubKey ed25519.PublicKey) ReleaseProvenance {
	sig, source := ResolveReleaseSignature()
	if sig == "" {
		return ReleaseProvenance{
			Status:   ProvenanceUnattestedCustom,
			Verified: false,
			Source:   source,
			Error:    "No cryptographic release signature present; running as custom/unattested build",
		}
	}

	var keyRing *liblicense.KeyRing
	if len(pubKey) > 0 {
		keyRing = liblicense.NewKeyRing(pubKey)
	} else {
		resolvedKey, err := GetReleaseVerificationPublicKey()
		if err != nil {
			return ReleaseProvenance{
				Status:   ProvenanceTamperedSignature,
				Verified: false,
				Source:   source,
				Error:    fmt.Sprintf("Failed to resolve release verification public key: %v", err),
			}
		}
		keyRing = liblicense.NewKeyRing(resolvedKey)
	}

	// 1. Canonical DIVREL1 or Armored PEM Block -> Delegate to liblicense.EvaluateProvenance
	if strings.HasPrefix(sig, liblicense.ProtocolPrefixRelease+".") || strings.HasPrefix(sig, "-----BEGIN") {
		var execPath string
		if p, err := os.Executable(); err == nil && p != "" {
			execPath = p
		}
		buildTime, _ := parseAnyDate(i.BuildDate)

		params := liblicense.ProvenanceParams{
			ExpectedProduct:  "gitlab-fleet-governor",
			CurrentVersion:   i.Version,
			CurrentCommit:    i.GitCommit,
			CurrentBuildDate: buildTime,
			BinaryPath:       execPath,
		}

		prov, err := liblicense.EvaluateProvenance(sig, keyRing, params)
		if err != nil {
			status := ProvenanceTamperedSignature
			errStr := err.Error()

			if errors.Is(err, liblicense.ErrReleaseProductMismatch) {
				status = ProvenanceTamperedMetadata
				errStr = fmt.Sprintf("Product mismatch: %v", err)
			} else if errors.Is(err, liblicense.ErrReleaseDigestMismatch) {
				status = ProvenanceTamperedMetadata
				errStr = fmt.Sprintf("Binary digest mismatch: %v", err)
			} else {
				var tampErr *liblicense.ReleaseTamperingError
				if errors.As(err, &tampErr) {
					status = ProvenanceTamperedMetadata
					switch tampErr.Field {
					case "version":
						errStr = fmt.Sprintf("Version mismatch: %v", err)
					case "git_commit":
						errStr = fmt.Sprintf("Commit mismatch: %v", err)
					case "build_date":
						errStr = fmt.Sprintf("Build date mismatch: %v", err)
					}
				} else if prov != nil && prov.Tampered {
					status = ProvenanceTamperedMetadata
				}
			}

			var signedClaims *ReleaseClaims
			if prov != nil && prov.Claims != nil {
				signedClaims = toVersionClaims(prov.Claims)
			} else if inspected, inspectErr := liblicense.InspectRelease(sig); inspectErr == nil {
				signedClaims = toVersionClaims(inspected)
			}

			authority := ""
			authorityID := ""
			if signedClaims != nil {
				authority = signedClaims.Authority
				authorityID = signedClaims.AuthorityID
			}

			return ReleaseProvenance{
				Status:       status,
				Verified:     false,
				Authority:    authority,
				AuthorityID:  authorityID,
				Source:       source,
				Error:        errStr,
				SignedClaims: signedClaims,
			}
		}

		// Defense against upstream Issue #19 (one-way 24h drift in liblicense):
		// Assert that compiled build date matches attested build date to within 2 seconds.
		if prov.Claims != nil && !prov.Claims.BuildDate.IsZero() {
			if !datesMatch(i.BuildDate, prov.Claims.BuildDate.UTC().Format(time.RFC3339)) {
				return ReleaseProvenance{
					Status:       ProvenanceTamperedMetadata,
					Verified:     false,
					Authority:    prov.Authority,
					AuthorityID:  prov.VerifiedByKeyID,
					Source:       source,
					Error:        fmt.Sprintf("Build date mismatch: binary compiled with date '%s', but signed claims specify '%s'", i.BuildDate, prov.Claims.BuildDate.UTC().Format(time.RFC3339)),
					SignedClaims: toVersionClaims(prov.Claims),
				}
			}
		}

		return ReleaseProvenance{
			Status:       ProvenanceVerifiedOfficial,
			Verified:     true,
			Source:       source,
			Authority:    prov.Authority,
			AuthorityID:  prov.VerifiedByKeyID,
			SignedClaims: toVersionClaims(prov.Claims),
		}
	}

	// 2. Legacy 2-part token (<payload>.<signature>) fallback
	claims, err := ParseAndVerifyReleaseToken(sig, pubKey)
	if err != nil {
		return ReleaseProvenance{
			Status:   ProvenanceTamperedSignature,
			Verified: false,
			Source:   source,
			Error:    fmt.Sprintf("Release signature verification failed (%s): %v", source, err),
		}
	}

	// Verify Product match (if specified)
	if claims.Product != "" && claims.Product != "*" && claims.Product != "all" {
		if !strings.EqualFold(claims.Product, "gitlab-fleet-governor") {
			return ReleaseProvenance{
				Status:       ProvenanceTamperedMetadata,
				Verified:     false,
				Source:       source,
				Authority:    claims.Authority,
				AuthorityID:  claims.AuthorityID,
				Error:        fmt.Sprintf("Product mismatch: binary expects 'gitlab-fleet-governor', but signed claims specify '%s'", claims.Product),
				SignedClaims: claims,
			}
		}
	}

	// Verify Version match (normalize leading "v")
	expectedVer := strings.TrimPrefix(strings.TrimSpace(claims.Version), "v")
	actualVer := strings.TrimPrefix(strings.TrimSpace(i.Version), "v")
	if actualVer != "" && actualVer != "dev" && actualVer != expectedVer {
		return ReleaseProvenance{
			Status:       ProvenanceTamperedMetadata,
			Verified:     false,
			Source:       source,
			Authority:    claims.Authority,
			AuthorityID:  claims.AuthorityID,
			Error:        fmt.Sprintf("Version mismatch: binary compiled as '%s', but signed claims specify '%s'", i.Version, claims.Version),
			SignedClaims: claims,
		}
	}

	// Verify GitCommit match (prefix match allowed for short commit SHAs)
	expectedCommit := strings.TrimSpace(claims.GitCommit)
	actualCommit := strings.TrimSpace(i.GitCommit)
	if actualCommit != "" && actualCommit != "none" && expectedCommit != "" {
		if !strings.HasPrefix(expectedCommit, actualCommit) && !strings.HasPrefix(actualCommit, expectedCommit) {
			return ReleaseProvenance{
				Status:       ProvenanceTamperedMetadata,
				Verified:     false,
				Source:       source,
				Authority:    claims.Authority,
				AuthorityID:  claims.AuthorityID,
				Error:        fmt.Sprintf("Commit mismatch: binary compiled from '%s', but signed claims specify '%s'", i.GitCommit, claims.GitCommit),
				SignedClaims: claims,
			}
		}
	}

	// Verify BuildDate match (parsed timestamps match to within 2 seconds or exact string match)
	if !datesMatch(i.BuildDate, claims.BuildDate) {
		return ReleaseProvenance{
			Status:       ProvenanceTamperedMetadata,
			Verified:     false,
			Source:       source,
			Authority:    claims.Authority,
			AuthorityID:  claims.AuthorityID,
			Error:        fmt.Sprintf("Build date mismatch: binary compiled with date '%s', but signed claims specify '%s'", i.BuildDate, claims.BuildDate),
			SignedClaims: claims,
		}
	}

	// Verify Binary Digest (if specified in claims)
	if claims.BinaryDigest != "" {
		if execPath, err := os.Executable(); err == nil && execPath != "" {
			computedDigest, err := liblicense.ComputeFileDigest(execPath)
			if err == nil {
				d1 := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(claims.BinaryDigest)), "sha256:")
				d2 := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(computedDigest)), "sha256:")
				if d1 != d2 {
					return ReleaseProvenance{
						Status:       ProvenanceTamperedMetadata,
						Verified:     false,
						Source:       source,
						Authority:    claims.Authority,
						AuthorityID:  claims.AuthorityID,
						Error:        fmt.Sprintf("Binary digest mismatch: computed %s, but signed claims specify %s", computedDigest, claims.BinaryDigest),
						SignedClaims: claims,
					}
				}
			}
		}
	}

	return ReleaseProvenance{
		Status:       ProvenanceVerifiedOfficial,
		Verified:     true,
		Source:       source,
		Authority:    claims.Authority,
		AuthorityID:  claims.AuthorityID,
		SignedClaims: claims,
	}
}

func toVersionClaims(c *liblicense.ReleaseClaims) *ReleaseClaims {
	if c == nil {
		return nil
	}
	buildDateStr := ""
	if !c.BuildDate.IsZero() {
		buildDateStr = c.BuildDate.UTC().Format(time.RFC3339)
	}
	releaseDateStr := ""
	if !c.ReleaseDate.IsZero() {
		releaseDateStr = c.ReleaseDate.UTC().Format(time.RFC3339)
	}
	return &ReleaseClaims{
		Product:      c.Product,
		Version:      c.Version,
		GitCommit:    c.GitCommit,
		BuildDate:    buildDateStr,
		ReleaseDate:  releaseDateStr,
		BinaryDigest: c.BinaryDigest,
		Authority:    c.Authority,
		AuthorityID:  c.KeyID,
	}
}

func datesMatch(date1, date2 string) bool {
	if strings.TrimSpace(date1) == strings.TrimSpace(date2) {
		return true
	}
	t1, err1 := parseAnyDate(date1)
	t2, err2 := parseAnyDate(date2)
	if err1 != nil || err2 != nil {
		return false
	}
	diff := t1.Sub(t2)
	return diff >= -2*time.Second && diff <= 2*time.Second
}

func parseAnyDate(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("unknown date format")
}
