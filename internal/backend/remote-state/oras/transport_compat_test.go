package oras

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	cleanhttp "github.com/hashicorp/go-cleanhttp"
	"github.com/vmvarela/ghoten/internal/httpclient"
	"github.com/vmvarela/ghoten/internal/logging"
	"github.com/vmvarela/ghoten/version"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/time/rate"
)

type requestLimiter interface {
	Wait(ctx context.Context) error
}

type userAgentRoundTripper struct {
	userAgent string
	inner     http.RoundTripper
}

func (rt *userAgentRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		r2 := req.Clone(req.Context())
		r2.Header.Set("User-Agent", rt.userAgent)
		return rt.inner.RoundTrip(r2)
	}
	return rt.inner.RoundTrip(req)
}

type rateLimitedRoundTripper struct {
	limiter requestLimiter
	inner   http.RoundTripper
}

func (rt *rateLimitedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.limiter != nil {
		if err := rt.limiter.Wait(req.Context()); err != nil {
			return nil, err
		}
	}
	return rt.inner.RoundTrip(req)
}

func newORASHTTPClient(insecure bool, caFile string, rateLimit int, rateBurst int) (*http.Client, error) {
	client := cleanhttp.DefaultPooledClient()

	if t, ok := client.Transport.(*http.Transport); ok {
		t = t.Clone()
		if t.TLSClientConfig == nil {
			t.TLSClientConfig = &tls.Config{}
		}
		t.TLSClientConfig.InsecureSkipVerify = insecure
		if insecure {
			logging.HCLogger().Named("backend.oras").Warn("TLS certificate verification is disabled", "insecure", true)
		}
		if caFile != "" {
			pem, err := os.ReadFile(caFile)
			if err != nil {
				return nil, fmt.Errorf("reading ca_file %q: %w", caFile, err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("ca_file %q: no valid certificates", caFile)
			}
			t.TLSClientConfig.RootCAs = pool
		}
		client.Transport = t
	}

	var limiter requestLimiter
	if rateLimit > 0 {
		if rateBurst <= 0 {
			rateBurst = 1
		}
		limiter = rate.NewLimiter(rate.Limit(rateLimit), rateBurst)
	}

	var rt http.RoundTripper = &userAgentRoundTripper{
		userAgent: httpclient.GhotenUserAgent(version.Version),
		inner:     client.Transport,
	}
	if limiter != nil {
		rt = &rateLimitedRoundTripper{limiter: limiter, inner: rt}
	}
	rt = otelhttp.NewTransport(rt)
	client.Transport = rt

	return client, nil
}
