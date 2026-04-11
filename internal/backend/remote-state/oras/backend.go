package oras

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	oraslib "github.com/vmvarela/ghoten-oras-backend/backend/oras"
	"github.com/vmvarela/ghoten/internal/backend"
	"github.com/vmvarela/ghoten/internal/command/cliconfig"
	"github.com/vmvarela/ghoten/internal/command/cliconfig/ociauthconfig"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/httpclient"
	"github.com/vmvarela/ghoten/internal/legacy/helper/schema"
	"github.com/vmvarela/ghoten/internal/logging"
	"github.com/vmvarela/ghoten/internal/states/remote"
	"github.com/vmvarela/ghoten/internal/states/statemgr"
	"github.com/vmvarela/ghoten/version"
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

// Backend implements the Openghoten backend interface for OCI registries
// using the ORAS (OCI Registry As Storage) protocol. It stores state as
// OCI artifacts and supports workspace management via manifest tags.
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
	retryCfg     oraslib.RetryConfig
	stateMaxSize int64

	versioningMaxVersions int

	orasCredsPolicy cliconfigORASCredentialsPolicy
	lib             oraslib.StateBackend
}

// New creates a new ORAS backend instance with the given state encryption.
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
				Description: "Skip TLS certificate verification when communicating with the OCI registry",
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
				Description: "Lock TTL in seconds. When greater than 0, stale locks older than this are automatically cleared during lock acquisition. Set to 0 (default) to disable automatic stale lock clearing.",
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
				Description: "Maximum number of historical state versions to retain. 0 disables versioning, >0 enables versioning with that retention limit.",
			},
			"max_state_size": {
				Type:        schema.TypeInt,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(envVarMaxStateSize, 0),
				Description: "Maximum state size in bytes that may be read from the registry. Defaults to 268435456 (256 MiB). Set to 0 to use the default. Can also be set via TF_BACKEND_ORAS_MAX_STATE_SIZE env var.",
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
	switch b.compression {
	case "none", "gzip":
		// ok
	default:
		return fmt.Errorf("unsupported compression %q (supported: none, gzip)", b.compression)
	}

	lockTTLSeconds := data.Get("lock_ttl").(int)
	if lockTTLSeconds < 0 {
		return fmt.Errorf("lock_ttl must be non-negative")
	}
	b.lockTTL = time.Duration(lockTTLSeconds) * time.Second

	rateLimit := data.Get("rate_limit").(int)
	rateBurst := data.Get("rate_limit_burst").(int)
	if rateLimit < 0 {
		return fmt.Errorf("rate_limit must be non-negative")
	}
	if rateBurst < 0 {
		return fmt.Errorf("rate_limit_burst must be non-negative")
	}
	b.rateLimit = rateLimit
	b.rateBurst = rateBurst

	// Retry behavior (match HTTP backend semantics: retry_max is number of retries).
	retryMax := data.Get("retry_max").(int)
	retryWaitMin := time.Duration(data.Get("retry_wait_min").(int)) * time.Second
	retryWaitMax := time.Duration(data.Get("retry_wait_max").(int)) * time.Second

	retryCfg := oraslib.RetryConfig{
		MaxAttempts:       retryMax + 1,
		InitialBackoff:    retryWaitMin,
		MaxBackoff:        retryWaitMax,
		BackoffMultiplier: 2.0,
	}
	if retryCfg.MaxAttempts < 1 {
		retryCfg.MaxAttempts = 1
	}
	if retryCfg.InitialBackoff <= 0 {
		retryCfg.InitialBackoff = time.Second
	}
	if retryCfg.MaxBackoff > 0 && retryCfg.MaxBackoff < retryCfg.InitialBackoff {
		retryCfg.MaxBackoff = retryCfg.InitialBackoff
	}
	b.retryCfg = retryCfg

	// State versioning: max_versions > 0 enables versioning
	b.versioningMaxVersions = max(data.Get("max_versions").(int), 0)

	// State read size limit: 0 means use the built-in default (256 MiB).
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

	libCfg := oraslib.Config{
		Repository:   b.repository,
		Insecure:     b.insecure,
		CAFile:       b.caFile,
		Compression:  b.compression,
		LockTTL:      b.lockTTL,
		RateLimit:    b.rateLimit,
		RateBurst:    b.rateBurst,
		Retry:        b.retryCfg,
		MaxStateSize: b.stateMaxSize,
		MaxVersions:  b.versioningMaxVersions,
		UserAgent:    httpclient.GhotenUserAgent(version.Version),
	}
	if credFunc != nil {
		libCfg.CredentialFunc = func(ctx context.Context, hostport string) (oraslib.Credential, error) {
			cred, err := credFunc(ctx, hostport)
			if err != nil {
				return oraslib.Credential{}, err
			}
			return oraslib.Credential{
				Username:    cred.Username,
				Password:    cred.Password,
				AccessToken: cred.AccessToken,
			}, nil
		}
	}

	b.lib, err = oraslib.New(ctx, libCfg)
	return err
}

func (b *Backend) StateMgr(ctx context.Context, workspace string) (statemgr.Full, error) {
	if b.lib == nil {
		return nil, fmt.Errorf("backend is not configured")
	}
	mgr, err := b.lib.StateMgr(ctx, workspace)
	if err != nil {
		return nil, err
	}
	client := &RemoteClient{mgr: mgr}
	return remote.NewState(client, b.encryption), nil
}

func (b *Backend) Workspaces(ctx context.Context) ([]string, error) {
	if b.lib == nil {
		return nil, fmt.Errorf("backend is not configured")
	}
	return b.lib.Workspaces(ctx)
}

func (b *Backend) DeleteWorkspace(ctx context.Context, name string, force bool) error {
	if b.lib == nil {
		return fmt.Errorf("backend is not configured")
	}
	return b.lib.DeleteWorkspace(ctx, name, force)
}

type cliconfigORASCredentialsPolicy interface {
	CredentialFunc(ctx context.Context, repository string) (credentialFunc, error)
}

type credentialFunc func(ctx context.Context, hostport string) (orasAuth.Credential, error)

// Credentials

const (
	// defaultDockerCredentialHelperCacheTTL is an alias for the shared constant,
	// kept for readability in this package.
	defaultDockerCredentialHelperCacheTTL = ociauthconfig.DefaultCredentialHelperCacheTTL
)

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

	lookupEnv := ociauthconfig.NewCachedCredentialsLookupEnv(dockerCredentialHelperEnv{}, defaultDockerCredentialHelperCacheTTL)

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

// credentialHelperTimeout is the maximum time allowed for a credential helper
// process to respond. A hung helper would otherwise block ghoten indefinitely.
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
