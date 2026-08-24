package app

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func testCommand(out, errOut *bytes.Buffer) Command {
	return Command{Build: Build{Version: "test", Commit: "abc"}, Out: out, ErrOut: errOut}
}

func TestExecute_PrintsTheVersion(t *testing.T) {
	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"version"})

	require.NoError(t, err)
	require.Equal(t, "mqtt2prometheus test (abc)\n", out.String())
}

func TestExecute_ReportsAnUnknownSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"nonsense"})

	usage := &UsageError{}
	require.ErrorAs(t, err, &usage)
	require.ErrorContains(t, err, `unknown subcommand "nonsense"`)
	require.Contains(t, errOut.String(), "usage:")
}

func TestExecute_ReportsAMissingSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), nil)

	usage := &UsageError{}
	require.ErrorAs(t, err, &usage)
	require.Contains(t, errOut.String(), ConfigDirEnv)
}

func TestExecute_ReportsAFailedUsageWrite(t *testing.T) {
	var out bytes.Buffer

	err := Command{Out: &out, ErrOut: failingWriterOnly{}}.Execute(t.Context(), nil)

	require.ErrorContains(t, err, "disk full")
}

func TestExecute_ReportsAFailedVersionWrite(t *testing.T) {
	var errOut bytes.Buffer

	err := Command{Out: failingWriterOnly{}, ErrOut: &errOut}.Execute(t.Context(), []string{"version"})

	require.ErrorContains(t, err, "disk full")
}

func TestExecute_TreatsHelpAsSuccess(t *testing.T) {
	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"run", "--help"})

	require.NoError(t, err)
	require.Contains(t, errOut.String(), "log-level")
}

func TestExecute_RejectsAnUnknownFlag(t *testing.T) {
	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"verify", "--nope"})
	require.ErrorContains(t, err, "not defined")

	err = testCommand(&out, &errOut).Execute(t.Context(), []string{"version", "--nope"})
	require.ErrorContains(t, err, "not defined")
}

func TestExecute_RejectsAStrayArgument(t *testing.T) {
	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"verify", "extra"})

	usage := &UsageError{}
	require.ErrorAs(t, err, &usage)
	require.ErrorContains(t, err, `unexpected argument "extra"`)
}

func TestExecute_RequiresTheConfigDirEnvironmentVariable(t *testing.T) {
	t.Setenv(ConfigDirEnv, "")

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"verify"})
	require.ErrorContains(t, err, ConfigDirEnv+" is not set")

	err = testCommand(&out, &errOut).Execute(t.Context(), []string{"healthcheck"})
	require.ErrorContains(t, err, ConfigDirEnv+" is not set")
}

func TestExecute_VerifiesTheConfiguredDirectory(t *testing.T) {
	t.Setenv(ConfigDirEnv, writeConfigDir(t, "tcp://broker:1883", ":9000",
		map[string]string{"zwave.yaml": zwaveSource}))

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"verify"})

	require.NoError(t, err)
	require.Contains(t, out.String(), "1 sources, 1 rules")
}

func TestExecute_ProbesTheHealthEndpoint(t *testing.T) {
	t.Setenv(ConfigDirEnv, writeConfigDir(t, "tcp://broker:1883", "127.0.0.1:1",
		map[string]string{"zwave.yaml": zwaveSource}))

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"healthcheck"})

	require.ErrorContains(t, err, "probing")
}

func TestExecute_RunsUntilTheContextIsCancelled(t *testing.T) {
	t.Setenv(ConfigDirEnv, writeConfigDir(t, "tcp://127.0.0.1:1", freeAddress(t),
		map[string]string{"zwave.yaml": zwaveSource}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(ctx, []string{"run", "--log-level", "error"})

	require.NoError(t, err)
}

func TestExecute_ReportsAnUnreadableConfigDirectory(t *testing.T) {
	t.Setenv(ConfigDirEnv, filepath.Join(t.TempDir(), "absent"))

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"run"})

	require.ErrorContains(t, err, "reading config")
}

func TestEffectiveLevel_PrefersTheOverride(t *testing.T) {
	dir := writeConfigDir(t, "tcp://broker:1883", ":9000", map[string]string{"zwave.yaml": zwaveSource})

	require.Equal(t, "debug", effectiveLevel(dir, "debug"))
	require.Equal(t, "error", effectiveLevel(dir, ""))
	require.Empty(t, effectiveLevel(filepath.Join(t.TempDir(), "absent"), ""))
}
