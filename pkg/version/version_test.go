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
	version.BuildDate = "2026-08-25T19:00:00Z"

	info := version.Get()
	assert.Equal(t, "1.2.3", info.Version)
	assert.Equal(t, "abc12345", info.GitCommit)
	assert.Equal(t, "2026-08-25T19:00:00Z", info.BuildDate)

	str := info.String()
	assert.Contains(t, str, "1.2.3")
	assert.Contains(t, str, "abc12345")
	assert.Contains(t, str, "2026-08-25T19:00:00Z")
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
