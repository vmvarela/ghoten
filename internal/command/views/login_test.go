// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/vmvarela/ghoten/internal/command/arguments"
	"github.com/vmvarela/ghoten/internal/tfdiags"
)

func TestLoginViews(t *testing.T) {
	tests := map[string]struct {
		viewCall   func(login Login)
		wantJson   []map[string]any
		wantStdout string
		wantStderr string
	}{
		"uiSeparator": {
			viewCall: func(login Login) {
				login.UiSeparator()
			},
			wantJson:   []map[string]any{{}},
			wantStdout: withNewline("\n---------------------------------------------------------------------------------\n"),
		},
		"motdMessage": {
			viewCall: func(login Login) {
				login.MOTDMessage("Welcome to the service!")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Welcome to the service!",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: withNewline("Welcome to the service!"),
		},
		"defaultTFCLoginSuccess": {
			viewCall: func(login Login) {
				login.DefaultTFCLoginSuccess()
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Success! Logged in to cloud backend",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "\nSuccess! Logged in to cloud backend\n\n",
		},
		"defaultTFELoginSuccess": {
			viewCall: func(login Login) {
				login.DefaultTFELoginSuccess("app.example.com")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Success! Logged in to the cloud backend (app.example.com)",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Success! Logged in to the cloud backend (app.example.com)\n\n",
		},
		"tokenObtainedConfirmation": {
			viewCall: func(login Login) {
				login.TokenObtainedConfirmation("app.example.com")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Success! Ghoten has obtained and saved an API token. The new API token will be used for any future Ghoten command that must make authenticated requests to app.example.com",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Success! Ghoten has obtained and saved an API token.\n\nThe new API token will be used for any future Ghoten command that must make\nauthenticated requests to app.example.com.\n\n",
		},
		"openingBrowserForOAuth": {
			viewCall: func(login Login) {
				login.OpeningBrowserForOAuth("app.example.com", "https://app.example.com/oauth")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Opening web browser to the login page for app.example.com: https://app.example.com/oauth. If a browser does not open this automatically, open the following URL to proceed: https://app.example.com/oauth",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Ghoten must now open a web browser to the login page for app.example.com.\n\nIf a browser does not open this automatically, open the following URL to proceed:\n    https://app.example.com/oauth\n\n",
		},
		"manualBrowserLaunch": {
			viewCall: func(login Login) {
				login.ManualBrowserLaunch("app.example.com", "https://app.example.com/login")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Please open the following URL to access the login page for app.example.com: https://app.example.com/login",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Open the following URL to access the login page for app.example.com:\n    https://app.example.com/login\n\n",
		},
		"waitingForHostSignal": {
			viewCall: func(login Login) {
				login.WaitingForHostSignal()
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Waiting for the host to signal that login was successful",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Ghoten will now wait for the host to signal that login was successful.\n\n",
		},
		"passwordRequestHeader": {
			viewCall: func(login Login) {
				login.PasswordRequestHeader()
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Ghoten must temporarily use your password to request an API token. This password will NOT be saved locally.",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Ghoten must temporarily use your password to request an API token.\nThis password will NOT be saved locally.\n\n",
		},
		"openingBrowserForTokens": {
			viewCall: func(login Login) {
				login.OpeningBrowserForTokens("app.example.com", "https://app.example.com/tokens")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Ghoten must now open a web browser to the login page for app.example.com: https://app.example.com/tokens. If a browser does not open this automatically, open the following URL to proceed: https://app.example.com/tokens",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Ghoten must now open a web browser to the tokens page for app.example.com.\n\nIf a browser does not open this automatically, open the following URL to proceed:\n    https://app.example.com/tokens\n\n",
		},
		"manualBrowserLaunchForTokens": {
			viewCall: func(login Login) {
				login.ManualBrowserLaunchForTokens("app.example.com", "https://app.example.com/tokens")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Open the following URL to access the tokens page for app.example.com: https://app.example.com/tokens",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Open the following URL to access the tokens page for app.example.com:\n    https://app.example.com/tokens\n\n",
		},
		"generateTokenInstruction": {
			viewCall: func(login Login) {
				login.GenerateTokenInstruction()
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Generate a token using your browser, and copy-paste it into the prompt",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Generate a token using your browser, and copy-paste it into this prompt.\n\n",
		},
		"tokenStorageInFile": {
			viewCall: func(login Login) {
				login.TokenStorageInFile("/home/user/.terraform.d/credentials.tfrc.json")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Ghoten will store the token in plain text in the following file for use by subsequent commands: /home/user/.terraform.d/credentials.tfrc.json",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Ghoten will store the token in plain text in the following file\nfor use by subsequent commands:\n    /home/user/.terraform.d/credentials.tfrc.json\n\n",
		},
		"tokenStorageInHelper": {
			viewCall: func(login Login) {
				login.TokenStorageInHelper("osxkeychain")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Ghoten will store the token in the configured \"osxkeychain\" credentials helper for use by subsequent commands",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Ghoten will store the token in the configured \"osxkeychain\" credentials helper\nfor use by subsequent commands.\n\n",
		},
		"retrievedTokenForUser": {
			viewCall: func(login Login) {
				login.RetrievedTokenForUser("john.doe")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Retrieved token for user john.doe",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "\nRetrieved token for user john.doe\n\n",
		},
		"requestAPITokenMessage": {
			viewCall: func(login Login) {
				login.RequestAPITokenMessage("app.example.com", "OAuth")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "Ghoten will request an API token for app.example.com using OAuth",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "Ghoten will request an API token for app.example.com using OAuth.\n\n",
		},
		"browserBasedLoginInstruction": {
			viewCall: func(login Login) {
				login.BrowserBasedLoginInstruction()
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "This will work only if you are able to use a web browser on this computer to complete a login process. If not, you must obtain an API token by another means and configure it in the CLI configuration manually.",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "This will work only if you are able to use a web browser on this computer to\ncomplete a login process. If not, you must obtain an API token by another\nmeans and configure it in the CLI configuration manually.\n\n",
		},
		"storageLocationConsentInHelper": {
			viewCall: func(login Login) {
				login.StorageLocationConsentInHelper("osxkeychain")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "If login is successful, Ghoten will store the token in the configured \"osxkeychain\" credentials helper for use by subsequent commands",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "If login is successful, Ghoten will store the token in the configured\n\"osxkeychain\" credentials helper for use by subsequent commands.\n\n",
		},
		"storageLocationConsentInFile": {
			viewCall: func(login Login) {
				login.StorageLocationConsentInFile("/home/user/.terraform.d/credentials.tfrc.json")
			},
			wantJson: []map[string]any{
				{
					"@level":   "info",
					"@message": "If login is successful, Ghoten will store the token in plain text in the following file for use by subsequent commands: /home/user/.terraform.d/credentials.tfrc.json",
					"@module":  "ghoten.ui",
				},
			},
			wantStdout: "If login is successful, Ghoten will store the token in plain text in\nthe following file for use by subsequent commands:\n    /home/user/.terraform.d/credentials.tfrc.json\n\n",
		},
		"helpPrompt": {
			viewCall: func(login Login) {
				login.HelpPrompt("/home/user/.terraform.d/credentials.tfrc.json")
			},
			wantJson:   []map[string]any{{}},
			wantStdout: "",
			wantStderr: "\nUsage: ghoten [global options] login [hostname]\n\n  Retrieves an authentication token for the given hostname, if it supports\n  automatic login, and saves it in a credentials file in your home directory.\n\n  If not overridden by credentials helper settings in the CLI configuration,\n  the credentials will be written to the following local file:\n      /home/user/.terraform.d/credentials.tfrc.json\n\n",
		},
		// Diagnostics
		"warning": {
			viewCall: func(login Login) {
				diags := tfdiags.Diagnostics{
					tfdiags.Sourceless(tfdiags.Warning, "A warning occurred", "foo bar"),
				}
				login.Diagnostics(diags)
			},
			wantStdout: withNewline("\nWarning: A warning occurred\n\nfoo bar"),
			wantStderr: "",
			wantJson: []map[string]any{
				{
					"@level":   "warn",
					"@message": "Warning: A warning occurred",
					"@module":  "ghoten.ui",
					"diagnostic": map[string]any{
						"detail":   "foo bar",
						"severity": "warning",
						"summary":  "A warning occurred",
					},
					"type": "diagnostic",
				},
			},
		},
		"error": {
			viewCall: func(login Login) {
				diags := tfdiags.Diagnostics{
					tfdiags.Sourceless(tfdiags.Error, "An error occurred", "foo bar"),
				}
				login.Diagnostics(diags)
			},
			wantStdout: "",
			wantStderr: withNewline("\nError: An error occurred\n\nfoo bar"),
			wantJson: []map[string]any{
				{
					"@level":   "error",
					"@message": "Error: An error occurred",
					"@module":  "ghoten.ui",
					"diagnostic": map[string]any{
						"detail":   "foo bar",
						"severity": "error",
						"summary":  "An error occurred",
					},
					"type": "diagnostic",
				},
			},
		},
		"multiple_diagnostics": {
			viewCall: func(login Login) {
				diags := tfdiags.Diagnostics{
					tfdiags.Sourceless(tfdiags.Warning, "A warning", "foo bar warning"),
					tfdiags.Sourceless(tfdiags.Error, "An error", "foo bar error"),
				}
				login.Diagnostics(diags)
			},
			wantStdout: withNewline("\nWarning: A warning\n\nfoo bar warning"),
			wantStderr: withNewline("\nError: An error\n\nfoo bar error"),
			wantJson: []map[string]any{
				{
					"@level":   "warn",
					"@message": "Warning: A warning",
					"@module":  "ghoten.ui",
					"diagnostic": map[string]any{
						"detail":   "foo bar warning",
						"severity": "warning",
						"summary":  "A warning",
					},
					"type": "diagnostic",
				},
				{
					"@level":   "error",
					"@message": "Error: An error",
					"@module":  "ghoten.ui",
					"diagnostic": map[string]any{
						"detail":   "foo bar error",
						"severity": "error",
						"summary":  "An error",
					},
					"type": "diagnostic",
				},
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			testLoginHuman(t, tc.viewCall, tc.wantStdout, tc.wantStderr)
			testLoginJson(t, tc.viewCall, tc.wantJson)
			testLoginMulti(t, tc.viewCall, tc.wantStdout, tc.wantStderr, tc.wantJson)
		})
	}
}

func testLoginHuman(t *testing.T, call func(login Login), wantStdout, wantStderr string) {
	view, done := testView(t)
	loginView := NewLogin(arguments.ViewOptions{ViewType: arguments.ViewHuman}, view)
	call(loginView)
	output := done(t)
	if diff := cmp.Diff(wantStderr, output.Stderr()); diff != "" {
		t.Errorf("invalid stderr (-want, +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantStdout, output.Stdout()); diff != "" {
		t.Errorf("invalid stdout (-want, +got):\n%s", diff)
	}
}

func testLoginJson(t *testing.T, call func(login Login), want []map[string]interface{}) {
	view, done := testView(t)
	loginView := NewLogin(arguments.ViewOptions{ViewType: arguments.ViewJSON}, view)
	call(loginView)
	output := done(t)
	if output.Stderr() != "" {
		t.Errorf("expected no stderr but got:\n%s", output.Stderr())
	}

	testJSONViewOutputEquals(t, output.Stdout(), want)
}

func testLoginMulti(t *testing.T, call func(login Login), wantStdout string, wantStderr string, want []map[string]interface{}) {
	jsonInto, err := os.CreateTemp(t.TempDir(), "json-into-*")
	if err != nil {
		t.Fatalf("failed to create the file to write json content into: %s", err)
	}
	view, done := testView(t)
	loginView := NewLogin(arguments.ViewOptions{ViewType: arguments.ViewHuman, JSONInto: jsonInto}, view)
	call(loginView)
	{
		if err := jsonInto.Close(); err != nil {
			t.Fatalf("failed to close the jsonInto file: %s", err)
		}
		// check the fileInto content
		fileContent, err := os.ReadFile(jsonInto.Name())
		if err != nil {
			t.Fatalf("failed to read the file content with the json output: %s", err)
		}
		testJSONViewOutputEquals(t, string(fileContent), want)
	}
	{
		// check the human output
		output := done(t)
		if diff := cmp.Diff(wantStderr, output.Stderr()); diff != "" {
			t.Errorf("invalid stderr (-want, +got):\n%s", diff)
		}
		if diff := cmp.Diff(wantStdout, output.Stdout()); diff != "" {
			t.Errorf("invalid stdout (-want, +got):\n%s", diff)
		}
	}
}
