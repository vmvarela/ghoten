// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package httpclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/vmvarela/ghoten/internal/logging"
)

// NewForRegistryRequests is a variant of [New] that deals with some additional
// policy concerns related to "registry requests".
//
// Exactly what constitutes a "registry request" is actually more about
// historical technical debt than intentional design, since these concerns
// were originally supposed to be handled internally within the module and
// provider registry clients but the implementation of that unfortunately caused
// the effects to "leak out" into other parts of the system, which we now
// preserve for backward compatibility here.
//
// Therefore "registry requests" includes the following:
//   - All requests from the client of our network service discovery protocol,
//     even though not all discoverable services are actually "registries".
//   - Requests to module registries during module installation.
//   - Requests to provider registries during provider installation.
//
// The retryCount argument specifies how many times requests from the resulting
// client should be automatically retried when certain transient errors occur.
//
// The timeout argument specifies a deadline for the completion of each
// request made using the client.
func NewForRegistryRequests(ctx context.Context, retryCount int, timeout time.Duration) *retryablehttp.Client {
	// We'll start with the result of New, so that what we return still
	// honors our general policy for HTTP client behavior.
	baseClient := New(ctx)
	baseClient.Timeout = timeout

	// Registry requests historically offered automatic retry on certain
	// transient errors implemented using the retryablehttp library, so
	// we'll now deal with that here.
	retryableClient := retryablehttp.NewClient()
	retryableClient.HTTPClient = baseClient
	retryableClient.RetryMax = retryCount
	retryableClient.RequestLogHook = registryRequestLogHook
	retryableClient.ErrorHandler = registryMaxRetryErrorHandler
	// The internal logger would print unsanitized URLs; keep it disabled
	// so that retry observability flows only through the sanitized
	// RequestLogHook below.
	retryableClient.Logger = nil

	return retryableClient
}

// hclogToRetryLogger adapts an hclog-style leveled logger to
// retryablehttp.Logger, whose Printf-style surface hclog.Logger doesn't
// implement directly.
type hclogToRetryLogger struct{ hclog.Logger }

func (l hclogToRetryLogger) Printf(s string, args ...interface{}) {
	l.Logger.Info(fmt.Sprintf(s, args...))
}

func registryRequestLogHook(logger retryablehttp.Logger, req *http.Request, i int) {
	// The download retryable client disables internal retryablehttp logging
	// (which prints unsanitized URLs), so its hook stays usable via the
	// sanitized fallback logger.
	if i <= 0 {
		return
	}
	if logger == nil {
		logger = hclogToRetryLogger{logging.HCLogger()}
	}
	// Drop userinfo, query string, and fragment: they can carry
	// credentials (e.g. presigned URLs).
	logger.Printf("[INFO] Failed request to %s; retrying", redactedRequestURL(req))
}

// redactedRequestURL renders a request's URL without its userinfo, query
// string, and fragment, which can carry credentials (e.g. for "presigned"
// URLs) that must not appear in logs or error messages.
func redactedRequestURL(req *http.Request) string {
	if req == nil || req.URL == nil {
		return ""
	}
	return RedactedURLString(req.URL)
}

// RedactedURLString renders u without its userinfo, query string, and
// fragment, any of which can carry credentials (e.g. for "presigned" URLs)
// that must not appear in user-facing labels, logs, traces, or error
// messages. It is the shared sanitizer for all URL renderings in logs,
// spans, and errors.
func RedactedURLString(u *url.URL) string {
	if u == nil {
		return ""
	}
	redacted := *u
	redacted.User = nil
	redacted.RawQuery = ""
	redacted.Fragment = ""
	redacted.RawFragment = ""
	return redacted.String()
}

func registryMaxRetryErrorHandler(resp *http.Response, err error, numTries int) (*http.Response, error) {
	// Close the body per library instructions
	if resp != nil {
		resp.Body.Close()
	}

	attemptMsg := "request failed"
	if numTries > 1 {
		// This function is always called with numTries=RetryMax+1. If we made
		// any retry attempts, include that in the error message.
		attemptMsg = fmt.Sprintf("request failed after %d attempts", numTries)
	}
	if resp != nil {
		// Additional error detail: the URL text can carry credentials
		// (e.g. presigned query strings), so render it without userinfo
		// and query string.
		return resp, fmt.Errorf("%s: %s returned from %s", attemptMsg, resp.Status, redactedRequestURL(resp.Request))
	}
	if err != nil {
		// Wrap (rather than flattening into text) so that callers can still
		// scrub credential-bearing URLs embedded in the underlying error.
		return resp, fmt.Errorf("%s: %w", attemptMsg, err)
	}
	return resp, fmt.Errorf("%s", attemptMsg)
}
