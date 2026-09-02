package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
)

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
