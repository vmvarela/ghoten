# internal/states

State management. In-memory state representation and serialization.

## SUBPACKAGES

| Package | Purpose |
|---------|---------|
| `statemgr/` | State manager interfaces — `Reader`, `Writer`, `Locker`, `Persister` |
| `statefile/` | State file serialization — versioned format (v0–v4), encryption-aware |

## KEY TYPES

| Type | File | Role |
|------|------|------|
| `State` | `instance_object_src.go` | Complete terraform state tree |
| `SyncState` | `sync.go` | Thread-safe state wrapper for concurrent graph walking |
| `ResourceInstanceObject` | `instance_object.go` | Single resource's data (attrs, status, deps) |
| `Module` | `module.go` | State for a single module instance |

## WHERE TO LOOK

| Task | Location | Notes |
|------|----------|-------|
| State serialization | `statefile/version4.go` | Current format, custom JSON marshal |
| State upgrades | `statefile/version*.go` | v1→v2→v3→v4 migration path |
| Locking | `statemgr/locker.go` | `Locker` interface |
| Encryption | `statefile/read.go`, `write.go` | `encryption.StateEncryption` integration |

## CONVENTIONS

- State is normalized before serialization: `sort.Stable` for deterministic output
- `ErrNoState` vs `ErrUnusableState` — distinguish empty from corrupt
- Custom `pathStep` type for `cty.Path` JSON serialization
- Testdata in `statefile/testdata/roundtrip/` — `.in.tfstate` → `.out.tfstate`
