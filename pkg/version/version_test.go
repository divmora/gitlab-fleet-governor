package version_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divmora/gitlab-fleet-governor/pkg/version"
)

func TestGet(t *testing.T) {
	info := version.Get()

	assert.NotEmpty(t, info.Version)
	assert.NotEmpty(t, info.GitCommit)
	assert.NotEmpty(t, info.BuildDate)
	assert.NotEmpty(t, info.GoVersion)
	assert.NotEmpty(t, info.Compiler)
	assert.NotEmpty(t, info.Platform)
	assert.Contains(t, info.Platform, "/")
}

func TestGet_CustomValues(t *testing.T) {
	origVersion := version.Version
	origCommit := version.GitCommit
	origDate := version.BuildDate
	defer func() {
		version.Version = origVersion
		version.GitCommit = origCommit
		version.BuildDate = origDate
	}()

	version.Version = "1.2.3"
	version.GitCommit = "abc12345"
	version.BuildDate = "2026-09-05T19:00:00Z"

	info := version.Get()
	assert.Equal(t, "1.2.3", info.Version)
	assert.Equal(t, "abc12345", info.GitCommit)
	assert.Equal(t, "2026-09-05T19:00:00Z", info.BuildDate)

	str := info.String()
	assert.Contains(t, str, "1.2.3")
	assert.Contains(t, str, "abc12345")
	assert.Contains(t, str, "2026-09-05T19:00:00Z")
}

func TestInfo_String(t *testing.T) {
	info := version.Get()
	str := info.String()

	assert.True(t, strings.HasPrefix(str, "gitlab-fleet-governor "))
	assert.Contains(t, str, "commit: ")
	assert.Contains(t, str, "date: ")
	assert.Contains(t, str, "go: ")
	assert.Contains(t, str, "platform: ")
}

func TestInfo_JSON(t *testing.T) {
	info := version.Get()
	jsonStr, err := info.JSON()
	require.NoError(t, err)
	assert.NotEmpty(t, jsonStr)

	var unmarshaled version.Info
	err = json.Unmarshal([]byte(jsonStr), &unmarshaled)
	require.NoError(t, err)
	assert.Equal(t, info, unmarshaled)
}

func TestChangeDate_Calculation(t *testing.T) {
	info := version.Info{
		Version:   "0.4.0",
		BuildDate: "2026-09-11T12:00:00Z",
	}

	relTime, ok := info.ReleaseTime()
	require.True(t, ok)
	assert.Equal(t, 2026, relTime.Year())
	assert.Equal(t, 9, int(relTime.Month()))
	assert.Equal(t, 11, relTime.Day())

	changeDate, ok := info.ChangeDate()
	require.True(t, ok)
	assert.Equal(t, 2029, changeDate.Year())
	assert.Equal(t, 9, int(changeDate.Month()))
	assert.Equal(t, 11, changeDate.Day())

	// Exactly 2 years after release -> Not converted (BSL 1.1 active)
	twoYearsLater := relTime.AddDate(2, 0, 0)
	assert.False(t, info.IsApacheConverted(twoYearsLater))
	assert.Equal(t, "BSL-1.1", info.License(twoYearsLater))

	// 3 years and 1 day after release -> Converted (Apache 2.0 active)
	threeYearsOneDayLater := changeDate.AddDate(0, 0, 1)
	assert.True(t, info.IsApacheConverted(threeYearsOneDayLater))
	assert.Equal(t, "Apache-2.0", info.License(threeYearsOneDayLater))
}

func TestChangeDate_EdgeCases(t *testing.T) {
	unknownInfo := version.Info{BuildDate: "unknown"}
	_, ok := unknownInfo.ReleaseTime()
	assert.False(t, ok)
	_, ok = unknownInfo.ChangeDate()
	assert.False(t, ok)
	assert.False(t, unknownInfo.IsApacheConvertedNow())
}

func TestReleaseTime_GenesisEpoch(t *testing.T) {
	tests := []struct {
		name        string
		buildDate   string
		expectValid bool
	}{
		{
			name:        "Unix epoch 1970 (forgery attempt)",
			buildDate:   "1970-01-01T00:00:00Z",
			expectValid: false,
		},
		{
			name:        "Year 2020 (forgery attempt)",
			buildDate:   "2020-05-15T10:00:00Z",
			expectValid: false,
		},
		{
			name:        "Day before genesis epoch (2026-08-31 23:59:59)",
			buildDate:   "2026-08-31T23:59:59Z",
			expectValid: false,
		},
		{
			name:        "Exact genesis epoch (2026-09-01 00:00:00)",
			buildDate:   "2026-09-01T00:00:00Z",
			expectValid: true,
		},
		{
			name:        "Post-genesis valid release date (2026-09-11)",
			buildDate:   "2026-09-11T12:00:00Z",
			expectValid: true,
		},
		{
			name:        "Date-only format post-genesis (2026-09-15)",
			buildDate:   "2026-09-15",
			expectValid: true,
		},
		{
			name:        "Date-only format pre-genesis (2025-12-31)",
			buildDate:   "2025-12-31",
			expectValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := version.Info{BuildDate: tt.buildDate}
			relTime, ok := info.ReleaseTime()
			if tt.expectValid {
				assert.True(t, ok, "expected valid release time for %s", tt.buildDate)
				assert.False(t, relTime.IsZero())
				assert.True(t, relTime.Equal(version.ProductGenesisEpoch) || relTime.After(version.ProductGenesisEpoch))

				changeDate, chOk := info.ChangeDate()
				assert.True(t, chOk)
				assert.False(t, changeDate.IsZero())
			} else {
				assert.False(t, ok, "expected invalid/rejected release time for pre-genesis %s", tt.buildDate)
				assert.True(t, relTime.IsZero())

				_, chOk := info.ChangeDate()
				assert.False(t, chOk)
				assert.False(t, info.IsApacheConvertedNow())
			}
		})
	}
}
