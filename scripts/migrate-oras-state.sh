#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/ghoten.env"

ORAS_STATE_VERSION="v0.0.0-20261017074614-5bba9f03315c"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SRC="$WORK/opentofu"

git clone --quiet --filter=blob:none https://github.com/opentofu/opentofu.git "$SRC"
git -C "$SRC" checkout --quiet "$OPENTOFU_COMMIT"

PKG="$SRC/internal/backend/remote-state/oras"
mkdir -p "$PKG"

cat > "$PKG/backend.go" <<'EOF'
package oras

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	orasstate "github.com/vmvarela/oras-state"

	"github.com/opentofu/opentofu/internal/backend"
	"github.com/opentofu/opentofu/internal/command/cliconfig"
	"github.com/opentofu/opentofu/internal/command/cliconfig/ociauthconfig"
	"github.com/opentofu/opentofu/internal/encryption"
	"github.com/opentofu/opentofu/internal/httpclient"
	"github.com/opentofu/opentofu/internal/legacy/helper/schema"
	"github.com/opentofu/opentofu/internal/logging"
	"github.com/opentofu/opentofu/internal/states/remote"
	"github.com/opentofu/opentofu/internal/states/statemgr"
	"github.com/opentofu/opentofu/version"

	orasRegistry "oras.land/oras-go/v2/registry"
	orasRemote "oras.land/oras-go/v2/registry/remote"
	orasAuth "oras.land/oras-go/v2/registry/remote/auth"
)

const envVarRepository = "TF_BACKEND_ORAS_REPOSITORY"

const (
	envVarRetryMax     = "TF_BACKEND_ORAS_RETRY_MAX"
	envVarRetryWaitMin = "TF_BACKEND_ORAS_RETRY_WAIT_MIN"
	envVarRetryWaitMax = "TF_BACKEND_ORAS_RETRY_WAIT_MAX"
	envVarLockTTL      = "TF_BACKEND_ORAS_LOCK_TTL"
	envVarRateLimit    = "TF_BACKEND_ORAS_RATE_LIMIT"
	envVarRateBurst    = "TF_BACKEND_ORAS_RATE_LIMIT_BURST"
	envVarMaxStateSize = "TF_BACKEND_ORAS_MAX_STATE_SIZE"
)

type Backend struct {
	*schema.Backend
	encryption encryption.StateEncryption

	repository   string
	insecure     bool
	caFile       string
	compression  string
	lockTTL      time.Duration
	rateLimit    int
	rateBurst    int
	retryCfg     RetryConfig
	stateMaxSize int64

	versioningMaxVersions int

	orasCredsPolicy cliconfigORASCredentialsPolicy
	client          *orasstate.Client
}

func New(enc encryption.StateEncryption) backend.Backend {
	s := &schema.Backend{
		Schema: map[string]*schema.Schema{
			"repository": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "OCI repository in the form <registry>/<repository>, without tag or digest. Can also be set via TF_BACKEND_ORAS_REPOSITORY env var.",
				DefaultFunc: schema.EnvDefaultFunc(envVarRepository, ""),
			},
			"insecure": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Use an insecure OCI registry connection",
			},
			"ca_file": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Path to a PEM-encoded CA certificate bundle to trust when communicating with the OCI registry",
			},
			"retry_max": {
				Type:        schema.TypeInt,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(envVarRetryMax, 2),
				Description: "The number of retries for transient registry requests.",
			},
			"retry_wait_min": {
				Type:        schema.TypeInt,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(envVarRetryWaitMin, 1),
				Description: "The minimum time in seconds to wait between transient registry request attempts.",
			},
			"retry_wait_max": {
				Type:        schema.TypeInt,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(envVarRetryWaitMax, 30),
				Description: "The maximum time in seconds to wait between transient registry request attempts.",
			},
			"compression": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "none",
				Description: "State compression. Supported values: none, gzip.",
			},
			"lock_ttl": {
				Type:        schema.TypeInt,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(envVarLockTTL, 0),
				Description: "Lock TTL in seconds. 0 disables automatic stale lock clearing.",
			},
			"rate_limit": {
				Type:        schema.TypeInt,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(envVarRateLimit, 0),
				Description: "Maximum registry requests per second. 0 disables rate limiting.",
			},
			"rate_limit_burst": {
				Type:        schema.TypeInt,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(envVarRateBurst, 0),
				Description: "Maximum burst size for registry requests when rate limiting is enabled. 0 defaults to 1.",
			},
			"max_versions": {
				Type:        schema.TypeInt,
				Optional:    true,
				Default:     0,
				Description: "Maximum number of historical state versions to retain. 0 disables versioning.",
			},
			"max_state_size": {
				Type:        schema.TypeInt,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(envVarMaxStateSize, 0),
				Description: "Maximum state size in bytes. 0 uses the 256 MiB default.",
			},
		},
	}

	b := &Backend{Backend: s, encryption: enc}
	b.Backend.ConfigureFunc = b.configure
	return b
}

func (b *Backend) configure(ctx context.Context) error {
	data := schema.FromContextBackendConfig(ctx)

	repository := data.Get("repository").(string)
	if repository == "" {
		return fmt.Errorf("repository must not be empty (set via config or %s)", envVarRepository)
	}

	ref, err := orasRegistry.ParseReference(repository)
	if err != nil {
		return err
	}
	if ref.Reference != "" {
		return fmt.Errorf("repository must not include a tag or digest")
	}

	b.repository = repository
	b.insecure = data.Get("insecure").(bool)
	b.caFile = data.Get("ca_file").(string)

	b.compression = strings.ToLower(strings.TrimSpace(data.Get("compression").(string)))
	if b.compression == "" {
		b.compression = "none"
	}
	if b.compression != "none" && b.compression != "gzip" {
		return fmt.Errorf("unsupported compression %q (supported: none, gzip)", b.compression)
	}

	lockTTLSeconds := data.Get("lock_ttl").(int)
	if lockTTLSeconds < 0 {
		return fmt.Errorf("lock_ttl must be non-negative")
	}
	b.lockTTL = time.Duration(lockTTLSeconds) * time.Second

	b.rateLimit = data.Get("rate_limit").(int)
	b.rateBurst = data.Get("rate_limit_burst").(int)
	if b.rateLimit < 0 {
		return fmt.Errorf("rate_limit must be non-negative")
	}
	if b.rateBurst < 0 {
		return fmt.Errorf("rate_limit_burst must be non-negative")
	}

	retryMax := data.Get("retry_max").(int)
	retryWaitMin := time.Duration(data.Get("retry_wait_min").(int)) * time.Second
	retryWaitMax := time.Duration(data.Get("retry_wait_max").(int)) * time.Second
	b.retryCfg = RetryConfig{
		MaxAttempts:       max(retryMax+1, 1),
		InitialBackoff:    retryWaitMin,
		MaxBackoff:        retryWaitMax,
		BackoffMultiplier: 2,
	}
	if b.retryCfg.InitialBackoff <= 0 {
		b.retryCfg.InitialBackoff = time.Second
	}
	if b.retryCfg.MaxBackoff > 0 && b.retryCfg.MaxBackoff < b.retryCfg.InitialBackoff {
		b.retryCfg.MaxBackoff = b.retryCfg.InitialBackoff
	}

	b.versioningMaxVersions = max(data.Get("max_versions").(int), 0)

	maxStateSize := data.Get("max_state_size").(int)
	if maxStateSize < 0 {
		return fmt.Errorf("max_state_size must be non-negative")
	}
	b.stateMaxSize = int64(maxStateSize)

	cliCfg, diags := cliconfig.LoadConfig(ctx)
	if diags.HasErrors() {
		return diags.Err()
	}
	policy, err := cliCfg.OCICredentialsPolicy(ctx)
	if err != nil {
		return err
	}
	b.orasCredsPolicy = realORASCredentialsPolicy{policy: policy}

	credFunc, err := b.orasCredsPolicy.CredentialFunc(ctx, b.repository)
	if err != nil {
		return err
	}

	httpClient, err := newHTTPClient(b.insecure, b.caFile, b.rateLimit, b.rateBurst)
	if err != nil {
		return err
	}

	cfg := orasstate.Config{
		WorkspaceLayout: orasstate.WorkspaceLayoutLegacy,
		PlainHTTP:       b.insecure,
		TLSSkipVerify:   b.insecure,
		CAFile:          b.caFile,
		Compression:     b.compression == "gzip",
		LockTTL:         b.lockTTL,
		MaxVersions:     b.versioningMaxVersions,
		MaxStateSize:    b.stateMaxSize,
		HTTPClient:      httpClient,
		UserAgent:       httpclient.OpenTofuUserAgent(version.Version),
	}
	if credFunc != nil {
		cfg.CredentialFunc = func(ctx context.Context, hostport string) (orasstate.Credential, error) {
			cred, err := credFunc(ctx, hostport)
			if err != nil {
				return orasstate.Credential{}, err
			}
			return orasstate.Credential{
				Username:    cred.Username,
				Password:    cred.Password,
				AccessToken: cred.AccessToken,
			}, nil
		}
	}

	b.client, err = orasstate.NewClient(ref.Registry, ref.Repository, cfg)
	return err
}

func (b *Backend) StateMgr(_ context.Context, workspace string) (statemgr.Full, error) {
	if b.client == nil {
		return nil, fmt.Errorf("backend is not configured")
	}
	client := &RemoteClient{client: b.client, workspace: workspace, retryCfg: b.retryCfg}
	return remote.NewState(client, b.encryption), nil
}

func (b *Backend) Workspaces(ctx context.Context) ([]string, error) {
	if b.client == nil {
		return nil, fmt.Errorf("backend is not configured")
	}
	workspaces, err := withRetry(ctx, b.retryCfg, func(ctx context.Context) ([]string, error) {
		return b.client.List(ctx)
	})
	if err != nil {
		return nil, err
	}
	for _, workspace := range workspaces {
		if workspace == backend.DefaultStateName {
			return workspaces, nil
		}
	}
	workspaces = append(workspaces, backend.DefaultStateName)
	sort.Strings(workspaces)
	return workspaces, nil
}

func (b *Backend) DeleteWorkspace(ctx context.Context, name string, _ bool) error {
	if b.client == nil {
		return fmt.Errorf("backend is not configured")
	}
	if name == backend.DefaultStateName || name == "" {
		return fmt.Errorf("can't delete default state")
	}
	return withRetryNoResult(ctx, b.retryCfg, func(ctx context.Context) error {
		return b.client.DeleteWorkspace(ctx, name)
	})
}

type cliconfigORASCredentialsPolicy interface {
	CredentialFunc(ctx context.Context, repository string) (credentialFunc, error)
}

type credentialFunc func(ctx context.Context, hostport string) (orasAuth.Credential, error)

type realORASCredentialsPolicy struct {
	policy ociauthconfig.CredentialsConfigs
}

func (p realORASCredentialsPolicy) CredentialFunc(ctx context.Context, repository string) (credentialFunc, error) {
	repo, err := orasRemote.NewRepository(repository)
	if err != nil {
		return nil, err
	}
	registryDomain := repo.Reference.Registry
	repositoryPath := repo.Reference.Repository
	lookupEnv := dockerCredentialHelperEnv{}

	return func(ctx context.Context, _ string) (orasAuth.Credential, error) {
		source, err := p.policy.CredentialsSourceForRepository(ctx, registryDomain, repositoryPath)
		if err != nil {
			return orasAuth.EmptyCredential, err
		}
		creds, err := source.Credentials(ctx, lookupEnv)
		if err != nil {
			if ociauthconfig.IsCredentialsNotFoundError(err) {
				return orasAuth.EmptyCredential, nil
			}
			return orasAuth.EmptyCredential, err
		}
		return creds.ToORASCredential(), nil
	}, nil
}

type dockerCredentialHelperEnv struct{}

var _ ociauthconfig.CredentialsLookupEnvironment = dockerCredentialHelperEnv{}

const credentialHelperTimeout = 30 * time.Second

func (dockerCredentialHelperEnv) QueryDockerCredentialHelper(ctx context.Context, helperName string, serverURL string) (ociauthconfig.DockerCredentialHelperGetResult, error) {
	exe := "docker-credential-" + helperName

	tctx, cancel := context.WithTimeout(ctx, credentialHelperTimeout)
	defer cancel()

	cmd := exec.CommandContext(tctx, exe, "get")
	cmd.Stdin = strings.NewReader(serverURL)
	stdout, err := cmd.Output()
	if err != nil {
		if tctx.Err() == context.DeadlineExceeded {
			logging.HCLogger().Warn("credential helper timed out", "helper", exe, "timeout", credentialHelperTimeout)
			return ociauthconfig.DockerCredentialHelperGetResult{}, fmt.Errorf("credential helper %q timed out after %s: %w", exe, credentialHelperTimeout, context.DeadlineExceeded)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return ociauthconfig.DockerCredentialHelperGetResult{}, ociauthconfig.NewCredentialsNotFoundError(err)
		}
		return ociauthconfig.DockerCredentialHelperGetResult{}, err
	}

	var result ociauthconfig.DockerCredentialHelperGetResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		return ociauthconfig.DockerCredentialHelperGetResult{}, fmt.Errorf("parsing credential helper response: %w", err)
	}
	if result.ServerURL == "" {
		result.ServerURL = serverURL
	}
	return result, nil
}
EOF

cat > "$PKG/client.go" <<'EOF'
package oras

import (
	"context"
	"crypto/md5"
	"errors"

	orasstate "github.com/vmvarela/oras-state"

	"github.com/opentofu/opentofu/internal/states/remote"
	"github.com/opentofu/opentofu/internal/states/statemgr"
)

type RemoteClient struct {
	client    *orasstate.Client
	workspace string
	retryCfg  RetryConfig
}

var _ remote.Client = (*RemoteClient)(nil)
var _ remote.ClientLocker = (*RemoteClient)(nil)

func (c *RemoteClient) Get(ctx context.Context) (*remote.Payload, error) {
	data, err := withRetry(ctx, c.retryCfg, func(ctx context.Context) ([]byte, error) {
		return c.client.Get(ctx, c.workspace)
	})
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	sum := md5.Sum(data)
	return &remote.Payload{MD5: sum[:], Data: data}, nil
}

func (c *RemoteClient) Put(ctx context.Context, data []byte) error {
	if err := withRetryNoResult(ctx, c.retryCfg, func(ctx context.Context) error {
		return c.client.Put(ctx, c.workspace, data)
	}); err != nil {
		return err
	}
	c.client.WaitForRetention()
	return nil
}

func (c *RemoteClient) Delete(ctx context.Context) error {
	return withRetryNoResult(ctx, c.retryCfg, func(ctx context.Context) error {
		return c.client.Delete(ctx, c.workspace)
	})
}

func (c *RemoteClient) Lock(ctx context.Context, info *statemgr.LockInfo) (string, error) {
	shared := orasstate.LockInfo{}
	if info != nil {
		shared = orasstate.LockInfo{
			ID:        info.ID,
			Operation: info.Operation,
			Info:      info.Info,
			Who:       info.Who,
			Version:   info.Version,
			Created:   info.Created,
			Path:      info.Path,
		}
	}

	id, err := withRetry(ctx, c.retryCfg, func(ctx context.Context) (string, error) {
		return c.client.Lock(ctx, c.workspace, shared)
	})
	if err == nil {
		return id, nil
	}

	var lockErr *orasstate.LockError
	if errors.As(err, &lockErr) {
		var lockInfo *statemgr.LockInfo
		if lockErr.Info != nil {
			lockInfo = &statemgr.LockInfo{
				ID:        lockErr.Info.ID,
				Operation: lockErr.Info.Operation,
				Info:      lockErr.Info.Info,
				Who:       lockErr.Info.Who,
				Version:   lockErr.Info.Version,
				Created:   lockErr.Info.Created,
				Path:      lockErr.Info.Path,
			}
		}
		return "", &statemgr.LockError{Info: lockInfo, Err: lockErr.Err}
	}
	return "", err
}

func (c *RemoteClient) Unlock(ctx context.Context, id string) error {
	return withRetryNoResult(ctx, c.retryCfg, func(ctx context.Context) error {
		return c.client.Unlock(ctx, c.workspace, id)
	})
}
EOF

cat > "$PKG/retry.go" <<'EOF'
package oras

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	orasErrcode "oras.land/oras-go/v2/registry/remote/errcode"
)

type RetryConfig struct {
	MaxAttempts       int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
}

func withRetry[T any](ctx context.Context, cfg RetryConfig, operation func(context.Context) (T, error)) (T, error) {
	var zero T
	var lastErr error
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	backoff := cfg.InitialBackoff
	if backoff <= 0 {
		backoff = time.Second
	}

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		result, err := operation(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		if !isTransientError(err) {
			return zero, err
		}
		if attempt == cfg.MaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = time.Duration(float64(backoff) * cfg.BackoffMultiplier)
		if cfg.MaxBackoff > 0 && backoff > cfg.MaxBackoff {
			backoff = cfg.MaxBackoff
		}
	}
	return zero, lastErr
}

func withRetryNoResult(ctx context.Context, cfg RetryConfig, operation func(context.Context) error) error {
	_, err := withRetry(ctx, cfg, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, operation(ctx)
	})
	return err
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	var errResp *orasErrcode.ErrorResponse
	if errors.As(err, &errResp) {
		switch errResp.StatusCode {
		case http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "connection timeout") ||
		strings.Contains(msg, "tls handshake timeout") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "temporary failure in name resolution") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "unexpected eof") ||
		strings.Contains(msg, "read: eof") ||
		msg == "eof"
}
EOF

cat > "$PKG/transport.go" <<'EOF'
package oras

import (
	"context"
	"net/http"

	orasstate "github.com/vmvarela/oras-state"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/time/rate"
)

func newHTTPClient(insecure bool, caFile string, rateLimit, rateBurst int) (*http.Client, error) {
	client, err := orasstate.BuildHTTPClient(insecure, caFile)
	if err != nil {
		return nil, err
	}

	var rt http.RoundTripper = client.Transport
	if rateLimit > 0 {
		burst := rateBurst
		if burst <= 0 {
			burst = 1
		}
		rt = &rateLimitedRoundTripper{
			limiter: rate.NewLimiter(rate.Limit(rateLimit), burst),
			inner:   rt,
		}
	}
	client.Transport = otelhttp.NewTransport(rt)
	return client, nil
}

type requestLimiter interface {
	Wait(context.Context) error
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
EOF

python3 - "$SRC" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
path = root / "internal/backend/init/init.go"
text = path.read_text()

import_anchor = 'backendKubernetes "github.com/opentofu/opentofu/internal/backend/remote-state/kubernetes"\n'
if import_anchor not in text:
    raise SystemExit("kubernetes import anchor not found")
text = text.replace(
    import_anchor,
    import_anchor + 'backendORAS "github.com/opentofu/opentofu/internal/backend/remote-state/oras"\n',
    1,
)

map_anchor = '\t\t"kubernetes": func(enc encryption.StateEncryption) backend.Backend { return backendKubernetes.New(enc) },\n'
if map_anchor not in text:
    raise SystemExit("kubernetes backend anchor not found")
text = text.replace(
    map_anchor,
    map_anchor + '\t\t"oras":       func(enc encryption.StateEncryption) backend.Backend { return backendORAS.New(enc) },\n',
    1,
)
path.write_text(text)
PY

(
  cd "$SRC"
  GOWORK=off go mod edit -require="github.com/vmvarela/oras-state@$ORAS_STATE_VERSION"
  GOWORK=off go mod tidy
  gofmt -w internal/backend/remote-state/oras internal/backend/init/init.go
  GOWORK=off go test ./internal/backend/remote-state/oras ./internal/backend/init
  GOWORK=off go build -trimpath -o "$WORK/ghoten-oras" ./cmd/tofu
)

git -C "$SRC" add -A
git -C "$SRC" diff --cached --binary "$OPENTOFU_COMMIT" -- \
  internal/backend/remote-state/oras \
  internal/backend/init/init.go \
  go.mod go.sum > "$WORK/oras.patch"

test -s "$WORK/oras.patch"
cp "$WORK/oras.patch" "$ROOT/patches/oras-next.patch"
echo "Generated patches/oras-next.patch"
