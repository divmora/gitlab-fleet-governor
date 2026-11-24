package notify

import (
	"context"
	"log/slog"

	"github.com/divmora/gitlab-fleet-governor/internal/config"
	"github.com/divmora/gitlab-fleet-governor/internal/license"
)

// Gate carries what the licence check needs.
type Gate struct {
	GitLabBaseURL string
	LicenseKey    string
	LicenseFile   string
	CRL           string
	CRLFile       string
	CRLURL        string
}

// Send validates n, checks that the licence covers the configured
// channels, and dispatches s. Problems are logged, never returned:
// a notification must not change the outcome of the run or audit.
func Send(ctx context.Context, d *Dispatcher, n *config.NotificationsConfig, s *Summary, g Gate) []Result {
	if n.IsEmpty() || s == nil {
		return nil
	}
	if err := config.ValidateNotificationsConfig(n); err != nil {
		slog.Warn("Notifications not sent: invalid notification settings", "error", err)
		return nil
	}
	// Notifications are production monitoring whatever the run mode, so
	// the licence is checked as for a live run, even after a dry-run plan.
	if _, err := license.Enforce(license.EnforcementOptions{
		IsDryRun:         false,
		GitLabBaseURL:    g.GitLabBaseURL,
		LicenseKey:       g.LicenseKey,
		LicenseFile:      g.LicenseFile,
		CRL:              g.CRL,
		CRLFile:          g.CRLFile,
		CRLURL:           g.CRLURL,
		Command:          "notify",
		RequiredFeatures: Features(n),
	}); err != nil {
		slog.Warn("Notifications not sent", "error", err)
		return nil
	}
	if d == nil {
		d = NewDispatcher()
	}
	results := d.Dispatch(ctx, n, s)
	Log(results)
	return results
}
