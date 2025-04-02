package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCmd_Execute(t *testing.T) {
	t.Parallel()

	// Create a buffer to capture output
	b := bytes.NewBufferString("")
	rootCmd := NewRootCmd()
	rootCmd.cmd.SetOut(b)
	rootCmd.cmd.SetArgs([]string{})

	// Test with no arguments (should show help)
	err := rootCmd.Execute()
	require.NoError(t, err)

	// Read the output
	out, err := io.ReadAll(b)
	require.NoError(t, err)

	// Verify help output contains expected content
	assert.Contains(t, string(out), "Freighter Backend Server")
	assert.Contains(t, string(out), "serve")
}

func TestRootCmd_ExecuteWithServeCommand(t *testing.T) {
	t.Parallel()

	// Create a buffer to capture output
	b := bytes.NewBufferString("")
	rootCmd := NewRootCmd()
	rootCmd.cmd.SetOut(b)

	// Test with serve command
	rootCmd.cmd.SetArgs([]string{"serve", "--help"})
	err := rootCmd.Execute()
	require.NoError(t, err)

	// Read the output
	out, err := io.ReadAll(b)
	require.NoError(t, err)

	// Verify serve command help output
	assert.Contains(t, string(out), "Start the server")
}
