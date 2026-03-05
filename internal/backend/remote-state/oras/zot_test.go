package oras

// Integration tests for the ORAS backend against a Zot OCI registry.
//
// These tests require Docker to be available and are guarded by the
// TF_ORAS_ZOT_TEST environment variable to avoid running during normal
// unit-test passes.
//
// Run with:
//
//	TF_ORAS_ZOT_TEST=1 go test ./internal/backend/remote-state/oras/... -run Zot -v
//
// Or via Make:
//
//	make test-zot

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vmvarela/ghoten/internal/backend"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/states/statemgr"
	"github.com/zclconf/go-cty/cty"
)

const (
	zotTestEnvVar    = "TF_ORAS_ZOT_TEST"
	zotImage         = "ghcr.io/project-zot/zot-linux-amd64:v2.1.0"
	zotContainerPort = "5000"
)

// zotMinimalConfig is a minimal Zot registry config with:
//   - Anonymous push/pull (no auth extension)
//   - HTTP only (no TLS) for local test use
//   - Error-level logging to keep test output clean
const zotMinimalConfig = `{
  "distSpecVersion": "1.1.0",
  "storage": {
    "rootDirectory": "/var/lib/registry"
  },
  "http": {
    "address": "0.0.0.0",
    "port": "5000"
  },
  "log": {
    "level": "error"
  }
}
`

// requireZotTest skips the test if TF_ORAS_ZOT_TEST is not set.
func requireZotTest(t *testing.T) {
	t.Helper()
	if os.Getenv(zotTestEnvVar) == "" {
		t.Skipf("skipping Zot integration test: set %s=1 to enable", zotTestEnvVar)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("skipping Zot integration test: docker not found in PATH")
	}
}

// freeLocalPort finds an available TCP port on localhost.
func freeLocalPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeLocalPort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startZot launches a Zot OCI registry container and blocks until it is
// accepting connections. It returns the host:port address of the registry and
// a cleanup function that stops the container.
func startZot(t *testing.T) (addr string, cleanup func()) {
	t.Helper()

	port := freeLocalPort(t)
	containerName := fmt.Sprintf("ghoten-zot-test-%d", port)
	addr = fmt.Sprintf("localhost:%d", port)

	// Write the Zot config to a temp directory that we can mount into the container.
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(zotMinimalConfig), 0600); err != nil {
		t.Fatalf("write zot config: %v", err)
	}

	args := []string{
		"run", "--rm", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("%d:%s", port, zotContainerPort),
		"-v", fmt.Sprintf("%s:/etc/zot:ro", tmpDir),
		zotImage,
		"serve", "/etc/zot/config.json",
	}

	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run zot: %v\n%s", err, out)
	}

	// Wait until the registry health endpoint responds.
	waitForZot(t, addr)

	return addr, func() {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
	}
}

// waitForZot polls GET /v2/ until it gets a 200 or times out.
func waitForZot(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://%s/v2/", addr)) //nolint:noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("Zot registry at %s did not become ready within 30s", addr)
}

// newZotBackend configures an ORAS backend pointing at the given Zot addr
// using the provided OCI repo path suffix (e.g. "ghoten-test/state").
func newZotBackend(t *testing.T, addr, repoPath string) *Backend {
	t.Helper()
	conf := map[string]cty.Value{
		"repository":   cty.StringVal(fmt.Sprintf("%s/%s", addr, repoPath)),
		"insecure":     cty.StringVal("true"),
		"retry_max":    cty.StringVal("1"),
		"max_versions": cty.StringVal("0"),
	}
	return backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)
}

// ─── State scenarios ──────────────────────────────────────────────────────────

// TestZotIntegration_StateGetPutDelete verifies that state can be written,
// read back, and deleted against a live Zot registry.
// It uses a named workspace ("ephemeral") because the default workspace
// cannot be deleted.
func TestZotIntegration_StateGetPutDelete(t *testing.T) {
	requireZotTest(t)
	addr, cleanup := startZot(t)
	defer cleanup()

	ctx := context.Background()
	b := newZotBackend(t, addr, "ghoten-test/state")

	const ws = "ephemeral"

	sm, err := b.StateMgr(ctx, ws)
	if err != nil {
		t.Fatalf("StateMgr: %v", err)
	}

	// Initial refresh on an empty registry must return nil state.
	if err := sm.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState (initial): %v", err)
	}
	if sm.State() != nil {
		t.Fatalf("expected nil state on empty registry, got non-nil")
	}

	// Write some state.
	if err := sm.WriteState(states.NewState()); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := sm.PersistState(ctx, nil); err != nil {
		t.Fatalf("PersistState: %v", err)
	}

	// A second reader must see the persisted state.
	sm2, err := b.StateMgr(ctx, ws)
	if err != nil {
		t.Fatalf("StateMgr (reader): %v", err)
	}
	if err := sm2.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState (reader): %v", err)
	}
	if sm2.State() == nil {
		t.Fatal("expected state after put, got nil")
	}

	// Delete the workspace.
	if err := b.DeleteWorkspace(ctx, ws, false); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	// Verify it's gone.
	sm3, err := b.StateMgr(ctx, ws)
	if err != nil {
		t.Fatalf("StateMgr (after delete): %v", err)
	}
	if err := sm3.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState (after delete): %v", err)
	}
	if sm3.State() != nil {
		t.Fatal("expected nil state after workspace delete")
	}
}

// ─── Lock scenarios ───────────────────────────────────────────────────────────

// TestZotIntegration_LockUnlock verifies that locking and unlocking state
// works correctly against a Zot registry, and that a second locker is rejected
// while the first lock is held.
func TestZotIntegration_LockUnlock(t *testing.T) {
	requireZotTest(t)
	addr, cleanup := startZot(t)
	defer cleanup()

	ctx := context.Background()
	b := newZotBackend(t, addr, "ghoten-test/lock")

	sm, err := b.StateMgr(ctx, "default")
	if err != nil {
		t.Fatalf("StateMgr: %v", err)
	}
	if err := sm.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}

	info1 := statemgr.NewLockInfo()
	info1.Operation = "zot-lock-test"
	id1, err := sm.Lock(ctx, info1)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if id1 == "" {
		t.Fatal("expected non-empty lock ID")
	}

	// A concurrent locker from a separate statemgr must fail.
	sm2, err := b.StateMgr(ctx, "default")
	if err != nil {
		t.Fatalf("StateMgr (concurrent): %v", err)
	}
	if err := sm2.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState (concurrent): %v", err)
	}
	info2 := statemgr.NewLockInfo()
	info2.Operation = "zot-lock-concurrent"
	if _, err := sm2.Lock(ctx, info2); err == nil {
		t.Fatal("expected concurrent lock to fail while first lock is held")
	}

	// Release the first lock.
	if err := sm.Unlock(ctx, id1); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	// Now a new lock must succeed.
	info3 := statemgr.NewLockInfo()
	info3.Operation = "zot-lock-after-unlock"
	id3, err := sm2.Lock(ctx, info3)
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	_ = sm2.Unlock(ctx, id3)
}

// TestZotIntegration_LockTTLStaleClearing verifies that an expired lock is
// automatically cleared when lock_ttl is configured.
func TestZotIntegration_LockTTLStaleClearing(t *testing.T) {
	requireZotTest(t)
	addr, cleanup := startZot(t)
	defer cleanup()

	ctx := context.Background()

	// 1-second TTL so the lock becomes stale quickly.
	conf := map[string]cty.Value{
		"repository": cty.StringVal(fmt.Sprintf("%s/ghoten-test/lock-ttl", addr)),
		"insecure":   cty.StringVal("true"),
		"retry_max":  cty.StringVal("1"),
		"lock_ttl":   cty.StringVal("1"),
	}
	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)

	sm, err := b.StateMgr(ctx, "default")
	if err != nil {
		t.Fatalf("StateMgr: %v", err)
	}
	if err := sm.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}

	info := statemgr.NewLockInfo()
	info.Operation = "stale-lock"
	if _, err := sm.Lock(ctx, info); err != nil {
		t.Fatalf("initial lock: %v", err)
	}

	// Intentionally do NOT unlock — wait for the TTL to expire.
	time.Sleep(2 * time.Second)

	// A new statemgr must auto-clear the stale lock on acquisition.
	sm2, err := b.StateMgr(ctx, "default")
	if err != nil {
		t.Fatalf("StateMgr (new): %v", err)
	}
	if err := sm2.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState (new): %v", err)
	}
	info2 := statemgr.NewLockInfo()
	info2.Operation = "after-stale"
	id2, err := sm2.Lock(ctx, info2)
	if err != nil {
		t.Fatalf("lock after stale TTL expired: %v", err)
	}
	_ = sm2.Unlock(ctx, id2)
}

// ─── Retention scenarios ──────────────────────────────────────────────────────

// TestZotIntegration_Retention verifies that the backend prunes historical state
// tags once max_versions is exceeded against a Zot registry.
func TestZotIntegration_Retention(t *testing.T) {
	requireZotTest(t)

	addr, cleanup := startZot(t)
	defer cleanup()

	const maxVersions = 3

	repo := fmt.Sprintf("%s/ghoten-test/retention", addr)
	conf := map[string]cty.Value{
		"repository":   cty.StringVal(repo),
		"insecure":     cty.StringVal("true"),
		"retry_max":    cty.StringVal("1"),
		"max_versions": cty.StringVal(fmt.Sprintf("%d", maxVersions)),
	}
	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)
	ctx := context.Background()

	sm, err := b.StateMgr(ctx, "default")
	if err != nil {
		t.Fatalf("StateMgr: %v", err)
	}
	if err := sm.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}

	// Write maxVersions+2 times so retention pruning must trigger.
	for i := range maxVersions + 2 {
		if err := sm.WriteState(states.NewState()); err != nil {
			t.Fatalf("WriteState %d: %v", i+1, err)
		}
		if err := sm.PersistState(ctx, nil); err != nil {
			t.Fatalf("PersistState %d: %v", i+1, err)
		}
	}

	// Allow async retention goroutines to finish.
	drainRetentionSem()
	time.Sleep(500 * time.Millisecond)

	// Count version tags on the live registry.
	versionTags := countZotVersionTags(t, addr, "ghoten-test/retention", stateTagPrefix+"default"+stateVersionTagSeparator)
	if versionTags > maxVersions {
		t.Errorf("expected at most %d historical version tags, found %d", maxVersions, versionTags)
	}
}

// ─── Multi-workspace scenarios ────────────────────────────────────────────────

// TestZotIntegration_Workspaces verifies that multiple named workspaces can
// coexist in a single Zot repository.
func TestZotIntegration_Workspaces(t *testing.T) {
	requireZotTest(t)
	addr, cleanup := startZot(t)
	defer cleanup()

	ctx := context.Background()
	b := newZotBackend(t, addr, "ghoten-test/workspaces")

	for _, ws := range []string{"prod", "staging", "dev"} {
		sm, err := b.StateMgr(ctx, ws)
		if err != nil {
			t.Fatalf("StateMgr(%s): %v", ws, err)
		}
		if err := sm.RefreshState(ctx); err != nil {
			t.Fatalf("RefreshState(%s): %v", ws, err)
		}
		if err := sm.WriteState(states.NewState()); err != nil {
			t.Fatalf("WriteState(%s): %v", ws, err)
		}
		if err := sm.PersistState(ctx, nil); err != nil {
			t.Fatalf("PersistState(%s): %v", ws, err)
		}
	}

	listed, err := b.Workspaces(ctx)
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}

	want := map[string]bool{"prod": false, "staging": false, "dev": false}
	for _, w := range listed {
		want[w] = true
	}
	for ws, found := range want {
		if !found {
			t.Errorf("workspace %q not returned by Workspaces()", ws)
		}
	}
}

// ─── Compression scenarios ────────────────────────────────────────────────────

// TestZotIntegration_Compression verifies that gzip-compressed state
// round-trips correctly through Zot.
func TestZotIntegration_Compression(t *testing.T) {
	requireZotTest(t)
	addr, cleanup := startZot(t)
	defer cleanup()

	ctx := context.Background()
	conf := map[string]cty.Value{
		"repository":  cty.StringVal(fmt.Sprintf("%s/ghoten-test/compression", addr)),
		"insecure":    cty.StringVal("true"),
		"compression": cty.StringVal("gzip"),
		"retry_max":   cty.StringVal("1"),
	}
	b := backend.TestBackendConfig(t, New(encryption.StateEncryptionDisabled()), configs.SynthBody("synth", conf)).(*Backend)

	sm, err := b.StateMgr(ctx, "default")
	if err != nil {
		t.Fatalf("StateMgr: %v", err)
	}
	if err := sm.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState: %v", err)
	}
	if err := sm.WriteState(states.NewState()); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := sm.PersistState(ctx, nil); err != nil {
		t.Fatalf("PersistState: %v", err)
	}

	sm2, err := b.StateMgr(ctx, "default")
	if err != nil {
		t.Fatalf("StateMgr (reader): %v", err)
	}
	if err := sm2.RefreshState(ctx); err != nil {
		t.Fatalf("RefreshState (reader): %v", err)
	}
	if sm2.State() == nil {
		t.Fatal("expected non-nil state after gzip round-trip")
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// countZotVersionTags queries the OCI Distribution Spec tag-list endpoint
// on the Zot registry and counts tags matching the given prefix.
func countZotVersionTags(t *testing.T, addr, repo, prefix string) int {
	t.Helper()

	resp, err := http.Get(fmt.Sprintf("http://%s/v2/%s/tags/list", addr, repo)) //nolint:noctx
	if err != nil {
		// Registry or repo does not exist yet — treat as 0 tags.
		return 0
	}
	defer resp.Body.Close()

	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0
	}

	count := 0
	for _, tag := range body.Tags {
		if strings.HasPrefix(tag, prefix) {
			count++
		}
	}
	return count
}
