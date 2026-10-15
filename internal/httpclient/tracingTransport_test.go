// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package httpclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// spanRecorder is a SpanExporter that records exported spans in memory.
type spanRecorder struct {
	spans []sdktrace.ReadOnlySpan
}

func (r *spanRecorder) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	r.spans = append(r.spans, spans...)
	return nil
}

func (r *spanRecorder) Shutdown(context.Context) error { return nil }

type stubRoundTripper func(*http.Request) (*http.Response, error)

func (f stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestTracingTransport_RedactsLocationHeader checks that the redirect
// Location header captured in tracing spans does not include the query
// string or userinfo of the redirect target, which can carry credentials
// (e.g. for "presigned" URLs).
func TestTracingTransport_RedactsLocationHeader(t *testing.T) {
	const locationSecret = "location-secret-token"

	recorder := &spanRecorder{}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()

	tracer := provider.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test")

	transport := &tracingTransport{inner: stubRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Status:     "302 Found",
			Header: http.Header{
				"Location": []string{"https://presignuser:" + locationSecret + "@files.example.com/package.zip?X-Amz-Signature=" + locationSecret},
			},
			Body:    io.NopCloser(strings.NewReader("")),
			Request: req,
		}, nil
	})}

	req, err := http.NewRequestWithContext(ctx, "GET", "https://registry.example.com/download", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// Ending the span flushes it to the synchronous exporter.
	span.End()

	found := false
	for _, s := range recorder.spans {
		for _, kv := range s.Attributes() {
			if kv.Key != attribute.Key("http.response.header.location") {
				continue
			}
			found = true
			val := kv.Value.AsString()
			if strings.Contains(val, locationSecret) {
				t.Errorf("Location span attribute leaked the redirect query credentials: %q", val)
			}
			if strings.Contains(val, "presignuser") {
				t.Errorf("Location span attribute leaked the redirect userinfo: %q", val)
			}
			if !strings.Contains(val, "files.example.com/package.zip") {
				t.Errorf("Location span attribute should keep the non-secret URL parts, got %q", val)
			}
		}
	}
	if !found {
		t.Fatal("expected a location span attribute to be recorded")
	}
}

// TestRedactedLocationText_GenericallyRedactsUnparseableValues checks that
// Location header values that don't parse as URLs are replaced with a
// generic placeholder (echoing the raw value could disclose credential
// material), and that fragments are dropped from values that do parse.
func TestRedactedLocationText_GenericallyRedactsUnparseableValues(t *testing.T) {
	const secret = "location-hop-secret-token"

	// url.Parse accepts invalid escapes in queries but not in the userinfo
	// or path, so put the sentinel in both places across the parse-failures.
	if got := redactedLocationText("https://example.com/pkg%zz?sig=" + secret); got != "redacted location" {
		t.Errorf("unparseable Location was echoed with secret material: %q", got)
	}
	if got := redactedLocationText("https://user:" + secret + "%zz@example.com/package.zip"); got != "redacted location" {
		t.Errorf("unparseable Location userinfo was echoed with secret material: %q", got)
	}
	if got := redactedLocationText("https://files.example.com/package.zip#sig=" + secret); strings.Contains(got, secret) {
		t.Errorf("Location fragment was echoed with secret material: %q", got)
	}
	if got := redactedLocationText("https://files.example.com/package.zip?sig=" + secret); strings.Contains(got, secret) {
		t.Errorf("Location query was echoed with secret material: %q", got)
	}
}

// TestNew_URLOmitsCredentialsFromSpanAttributes uses a real [New] client to
// check that the query string (and userinfo) of an outgoing request is not
// exported in any span attribute, while the actual HTTP request still
// carries the query string to the server.
//
// Like production ([tracing.Init] registers a global provider), the global
// OpenTelemetry provider provides the tracers used by otelhttp.
func TestNew_URLOmitsCredentialsFromSpanAttributes(t *testing.T) {
	const sentinelQuery = "X-Amz-Signature=span-url-secret-token"

	recorder := &spanRecorder{}
	globalProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(recorder))
	defer func() { _ = globalProvider.Shutdown(context.Background()) }()
	prevProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(globalProvider)
	defer otel.SetTracerProvider(prevProvider)

	tracer := globalProvider.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test")

	var serverQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverQuery = r.URL.RawQuery
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := New(ctx)
	resp, err := client.Get(server.URL + "/package.zip?" + sentinelQuery)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close() // closes the otelhttp span associated with the request
	if string(body) != "ok" {
		t.Fatalf("unexpected response body: %q", body)
	}
	// Ending the span flushes it to the synchronous exporter.
	span.End()

	if serverQuery != sentinelQuery {
		t.Errorf("expected the request to carry the full query string to the server, got %q", serverQuery)
	}

	urlFullSeen := false
	for _, s := range recorder.spans {
		for _, kv := range s.Attributes() {
			val := kv.Value.AsString()
			if strings.Contains(val, sentinelQuery) {
				t.Errorf("span attribute %q leaked the signed query: %q", kv.Key, val)
			}
			if kv.Key == attribute.Key("url.full") {
				urlFullSeen = true
				if !strings.Contains(val, "/package.zip") {
					t.Errorf("url.full should keep the non-secret URL parts, got %q", val)
				}
			}
		}
	}
	if !urlFullSeen {
		t.Fatal("expected a url.full span attribute to be recorded")
	}
}

// TestNew_RedirectFragmentOmittedFromSpanAttributes uses a real redirect
// whose Location is "/package.zip#access_token=..." to check that an
// attacker-sent fragment carrying credential material (as OAuth-style
// redirects do with access tokens) never appears in any exported span
// attribute: neither the Location-header attribute nor url.full of the
// redirect hop.
func TestNew_RedirectFragmentOmittedFromSpanAttributes(t *testing.T) {
	const sentinel = "access_token=span-fragment-secret-token"

	recorder := &spanRecorder{}
	globalProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(recorder))
	defer func() { _ = globalProvider.Shutdown(context.Background()) }()
	prevProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(globalProvider)
	defer otel.SetTracerProvider(prevProvider)

	// The client only wraps its transports with tracing when the context
	// carries an active span (see [New]).
	tracer := globalProvider.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test")
	defer span.End()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			http.Redirect(w, r, "/package.zip#"+sentinel, http.StatusFound)
		case "/package.zip":
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("server doesn't support hijacking")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack failed: %v", err)
				return
			}
			conn.Close()
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	client := New(ctx)
	if _, err := client.Get(server.URL + "/download"); err == nil {
		t.Fatal("expected transport error from the closed redirect target")
	}
	// The failing request's spans are ended by the transports when they
	// return errors; ending the parent span flushes it too.
	span.End()
	for _, s := range recorder.spans {
		for _, kv := range s.Attributes() {
			if val := kv.Value.AsString(); strings.Contains(val, sentinel) {
				t.Errorf("span attribute %q leaked the credential-bearing fragment: %q", kv.Key, val)
			}
		}
	}
	if len(recorder.spans) == 0 {
		t.Fatal("expected spans to be exported")
	}
}
