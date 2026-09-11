package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
	"time"
)

// BSLChangePeriodYears defines the duration in years before a BSL 1.1 licensed release converts to Apache 2.0.
const BSLChangePeriodYears = 3

var (
	// Version is the current semver release (e.g. "0.1.0") or "dev".
	// Overridden at build time via -ldflags "-X github.com/divmora/gitlab-fleet-governor/pkg/version.Version=...".
	Version = "dev"

	// GitCommit is the git commit SHA of the build.
	// Overridden at build time via -ldflags "-X github.com/divmora/gitlab-fleet-governor/pkg/version.GitCommit=...".
	GitCommit = "none"

	// BuildDate is the RFC3339 formatted build timestamp.
	// Overridden at build time via -ldflags "-X github.com/divmora/gitlab-fleet-governor/pkg/version.BuildDate=...".
	BuildDate = "unknown"

	// GoVersion is the Go compiler version used to build the binary.
	GoVersion = runtime.Version()
)

// Info encapsulates complete build and environment version metadata.
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Compiler  string `json:"compiler"`
	Platform  string `json:"platform"`
}

// Get returns populated Info metadata, attempting runtime/debug introspection if flags were omitted.
func Get() Info {
	v := Version
	commit := GitCommit
	date := BuildDate

	if info, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if commit == "none" {
					commit = setting.Value
				}
			case "vcs.time":
				if date == "unknown" {
					date = setting.Value
				}
			}
		}
	}

	return Info{
		Version:   v,
		GitCommit: commit,
		BuildDate: date,
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// String returns human-readable formatted version string.
func (i Info) String() string {
	return fmt.Sprintf("gitlab-fleet-governor %s (commit: %s, date: %s, go: %s, platform: %s)",
		i.Version, i.GitCommit, i.BuildDate, i.GoVersion, i.Platform)
}

// JSON returns formatted JSON string representation of build metadata.
func (i Info) JSON() (string, error) {
	bytes, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// ReleaseTime parses the BuildDate timestamp into a time.Time in UTC.
func (i Info) ReleaseTime() (time.Time, bool) {
	if i.BuildDate == "" || i.BuildDate == "unknown" {
		return time.Time{}, false
	}
	// Try standard RFC3339 (e.g. 2026-09-11T10:16:56Z)
	t, err := time.Parse(time.RFC3339, i.BuildDate)
	if err == nil {
		return t.UTC(), true
	}
	// Try ISO-8601 date-only format (e.g. 2026-09-11)
	t, err = time.Parse("2006-01-02", i.BuildDate)
	if err == nil {
		return t.UTC(), true
	}
	// Try RFC3339Nano
	t, err = time.Parse(time.RFC3339Nano, i.BuildDate)
	if err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

// ChangeDate returns the timestamp exactly 3 years from release when this version converts to Apache 2.0.
func (i Info) ChangeDate() (time.Time, bool) {
	t, ok := i.ReleaseTime()
	if !ok {
		return time.Time{}, false
	}
	return t.AddDate(BSLChangePeriodYears, 0, 0), true
}

// IsApacheConverted evaluates whether the version has converted to Apache 2.0 at the given evaluation time.
func (i Info) IsApacheConverted(now time.Time) bool {
	changeDate, ok := i.ChangeDate()
	if !ok {
		return false
	}
	return now.UTC().After(changeDate)
}

// IsApacheConvertedNow evaluates whether the version has converted to Apache 2.0 as of current UTC time.
func (i Info) IsApacheConvertedNow() bool {
	return i.IsApacheConverted(time.Now().UTC())
}

// License returns "Apache-2.0" if Change Date has been reached, otherwise "BSL-1.1".
func (i Info) License(now time.Time) string {
	if i.IsApacheConverted(now) {
		return "Apache-2.0"
	}
	return "BSL-1.1"
}
