// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package e2etest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vmvarela/ghoten/internal/e2e"
)

var ghotenBin string

// canRunGoBuild is a short-term compromise to account for the fact that we
// have a small number of tests that work by building helper programs using
// "go build" at runtime, but we can't do that in our isolated test mode
// driven by the make-archive.sh script.
//
// FIXME: Rework this a bit so that we build the necessary helper programs
// (test plugins, etc) as part of the initial suite setup, and in the
// make-archive.sh script, so that we can run all of the tests in both
// situations with the tests just using the executable already built for
// them, as we do for ghotenBin.
var canRunGoBuild bool

func TestMain(m *testing.M) {
	teardown := setup()
	// run returns before os.Exit, so the deferred teardown always fires,
	// even if a test panics.
	run := func() int {
		defer teardown()
		return m.Run()
	}
	os.Exit(run())
}

func setup() (teardown func()) {
	cleanup := func() {}

	// Isolate e2e runs from the host CLI config: an empty TF_CLI_CONFIG_FILE
	// still falls back to the home-dir config file (e.g. ~/.terraformrc),
	// which may carry provider_installation dev_overrides or mirrors that
	// would leak into test runs. Point HOME and XDG_CONFIG_HOME at empty
	// temp dirs for the whole suite, including child processes run in tests.
	homeDir, err := os.MkdirTemp("", "e2etest-home")
	if err != nil {
		panic(fmt.Sprintf("failed to create isolated home directory: %s", err))
	}
	prevHome, hadHome := os.LookupEnv("HOME")
	prevXdgConfig, hadXdgConfig := os.LookupEnv("XDG_CONFIG_HOME")
	os.Setenv("HOME", homeDir)
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(homeDir, ".config"))
	cleanup = func() {
		// Restore originally-set variables; originally-unset ones must be
		// unset again rather than set to "".
		if hadHome {
			os.Setenv("HOME", prevHome)
		} else {
			os.Unsetenv("HOME")
		}
		if hadXdgConfig {
			os.Setenv("XDG_CONFIG_HOME", prevXdgConfig)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
		os.RemoveAll(homeDir)
	}

	// If any of the steps below panic (e.g. in GoBuild or filepath.Abs),
	// still run the cleanup on the way out of the panic.
	defer func() {
		if r := recover(); r != nil {
			cleanup()
			panic(r)
		}
	}()

	if ghotenBin != "" {
		// this is pre-set when we're running in a binary produced from
		// the make-archive.sh script, since that is for testing an
		// executable obtained from a real release package. However, we do
		// need to turn it into an absolute path so that we can find it
		// when we change the working directory during tests.
		var err error
		ghotenBin, err = filepath.Abs(ghotenBin)
		if err != nil {
			panic(fmt.Sprintf("failed to find absolute path of ghoten executable: %s", err))
		}
		return cleanup
	}

	tmpFilename := e2e.GoBuild("github.com/vmvarela/ghoten/cmd/ghoten", "ghoten")

	// Make the executable available for use in tests
	ghotenBin = tmpFilename

	// Tests running in the ad-hoc testing mode are allowed to use "go build"
	// and similar to produce other test executables.
	// (See the comment on this variable's declaration for more information.)
	canRunGoBuild = true

	return func() {
		os.Remove(tmpFilename)
		cleanup()
	}
}

func canAccessNetwork() bool {
	// We re-use the flag normally used for acceptance tests since that's
	// established as a way to opt-in to reaching out to real systems that
	// may suffer transient errors.
	return os.Getenv("TF_ACC") != ""
}

func skipIfCannotAccessNetwork(t *testing.T) {
	t.Helper()

	if !canAccessNetwork() {
		t.Skip("network access not allowed; use TF_ACC=1 to enable")
	}
}
