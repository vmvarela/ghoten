// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statemgr

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/go-cmp/cmp"
	version "github.com/hashicorp/go-version"
	"github.com/zclconf/go-cty/cty"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/lang/marks"
	"github.com/vmvarela/ghoten/internal/states"
	"github.com/vmvarela/ghoten/internal/states/statefile"
	tfversion "github.com/vmvarela/ghoten/version"
)

func TestFilesystem(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	ls := testFilesystem(t)
	defer os.Remove(ls.readPath)
	TestFull(t, ls)
}

func TestFilesystemRace(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	ls := testFilesystem(t)
	defer os.Remove(ls.readPath)

	current := TestFullInitialState()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ls.WriteState(current); err != nil {
				panic(err)
			}
		}()
	}
	wg.Wait()
}

func TestFilesystemLocks(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	s := testFilesystem(t)
	defer os.Remove(s.readPath)

	// lock first
	info := NewLockInfo()
	info.Operation = "test"
	lockID, err := s.Lock(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command("go", "run", "testdata/lockstate.go", s.path).CombinedOutput()
	if err != nil {
		t.Fatal("unexpected lock failure", err, string(out))
	}

	if !strings.Contains(string(out), "lock failed") {
		t.Fatal("expected 'locked failed', got", string(out))
	}

	// check our lock info
	lockInfo, err := s.lockInfo()
	if err != nil {
		t.Fatal(err)
	}

	if lockInfo.Operation != "test" {
		t.Fatalf("invalid lock info %#v\n", lockInfo)
	}

	// a noop, since we unlock on exit
	if err := s.Unlock(t.Context(), lockID); err != nil {
		t.Fatal(err)
	}

	// local locks can re-lock
	lockID, err = s.Lock(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Unlock(t.Context(), lockID); err != nil {
		t.Fatal(err)
	}

	// we should not be able to unlock the same lock twice
	if err := s.Unlock(t.Context(), lockID); err == nil {
		t.Fatal("unlocking an unlocked state should fail")
	}

	// make sure lock info is gone
	lockInfoPath := s.lockInfoPath()
	if _, err := os.Stat(lockInfoPath); !os.IsNotExist(err) {
		t.Fatal("lock info not removed")
	}
}

// Verify that we can write to the state file, as Windows' mandatory locking
// will prevent writing to a handle different than the one that hold the lock.
func TestFilesystem_writeWhileLocked(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	s := testFilesystem(t)
	defer os.Remove(s.readPath)

	// lock first
	info := NewLockInfo()
	info.Operation = "test"
	lockID, err := s.Lock(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Unlock(t.Context(), lockID); err != nil {
			t.Fatal(err)
		}
	}()

	if err := s.WriteState(TestFullInitialState()); err != nil {
		t.Fatal(err)
	}
}

func TestFilesystem_pathOut(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	f, err := os.CreateTemp("", "tf")
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	f.Close()
	defer os.Remove(f.Name())

	ls := testFilesystem(t)
	ls.path = f.Name()
	defer os.Remove(ls.path)

	TestFull(t, ls)
}

func TestFilesystem_backup(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	f, err := os.CreateTemp("", "tf")
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	f.Close()
	defer os.Remove(f.Name())

	ls := testFilesystem(t)
	backupPath := f.Name()
	ls.SetBackupPath(backupPath)

	TestFull(t, ls)

	// The backup functionality should've saved a copy of the original state
	// prior to all of the modifications that TestFull does.
	bfh, err := os.Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	bf, err := statefile.Read(bfh, encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatal(err)
	}
	origState := TestFullInitialState()
	if !bf.State.Equal(origState) {
		if diff := cmp.Diff(bf.State, origState); diff != "" {
			t.Error("wrong backup state:\n" + diff)
		}
	}
}

// This test verifies a particularly tricky behavior where the input file
// is overridden and backups are enabled at the same time. This combination
// requires special care because we must ensure that when we create a backup
// it is of the original contents of the output file (which we're overwriting),
// not the contents of the input file (which is left unchanged).
func TestFilesystem_backupAndReadPath(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	info := NewLockInfo()
	info.Operation = "test"

	workDir := t.TempDir()
	markerOutput := addrs.OutputValue{Name: "foo"}.Absolute(addrs.RootModuleInstance)

	outState := states.BuildState(func(ss *states.SyncState) {
		ss.SetOutputValue(
			markerOutput,
			cty.StringVal("from-output-state"),
			false, // not sensitive
			"",
		)
	})
	outFile, err := os.Create(filepath.Join(workDir, "output.tfstate"))
	if err != nil {
		t.Fatalf("failed to create temporary outFile %s", err)
	}
	defer outFile.Close()
	err = statefile.Write(&statefile.File{
		Lineage:          "-",
		Serial:           0,
		TerraformVersion: version.Must(version.NewVersion("1.2.3")),
		State:            outState,
	}, outFile, encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatalf("failed to write initial outfile state to %s: %s", outFile.Name(), err)
	}

	inState := states.BuildState(func(ss *states.SyncState) {
		ss.SetOutputValue(
			markerOutput,
			cty.StringVal("from-input-state"),
			false, // not sensitive
			"",
		)
	})
	inFile, err := os.Create(filepath.Join(workDir, "input.tfstate"))
	if err != nil {
		t.Fatalf("failed to create temporary inFile %s", err)
	}
	defer inFile.Close()
	err = statefile.Write(&statefile.File{
		Lineage:          "-",
		Serial:           0,
		TerraformVersion: version.Must(version.NewVersion("1.2.3")),
		State:            inState,
	}, inFile, encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatalf("failed to write initial infile state to %s: %s", inFile.Name(), err)
	}

	backupPath := outFile.Name() + ".backup"

	ls := NewFilesystemBetweenPaths(inFile.Name(), outFile.Name(), encryption.StateEncryptionDisabled())
	ls.SetBackupPath(backupPath)

	newState := states.BuildState(func(ss *states.SyncState) {
		ss.SetOutputValue(
			markerOutput,
			cty.StringVal("from-new-state"),
			false, // not sensitive
			"",
		)
	})
	err = WriteAndPersist(t.Context(), ls, newState, nil)
	if err != nil {
		t.Fatalf("failed to write new state: %s", err)
	}

	lockID, err := ls.Lock(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}

	if err := ls.Unlock(t.Context(), lockID); err != nil {
		t.Fatal(err)
	}

	// The backup functionality should've saved a copy of the original contents
	// of the _output_ file, even though the first snapshot was read from
	// the _input_ file.
	t.Run("backup file", func(t *testing.T) {
		bfh, err := os.Open(backupPath)
		if err != nil {
			t.Fatal(err)
		}
		defer bfh.Close()
		bf, err := statefile.Read(bfh, encryption.StateEncryptionDisabled())
		if err != nil {
			t.Fatal(err)
		}
		os := bf.State.OutputValue(markerOutput)
		if got, want := os.Value, cty.StringVal("from-output-state"); !want.RawEquals(got) {
			t.Errorf("wrong marker value in backup state file\ngot:  %#v\nwant: %#v", got, want)
		}
	})
	t.Run("output file", func(t *testing.T) {
		ofh, err := os.Open(outFile.Name())
		if err != nil {
			t.Fatal(err)
		}
		defer ofh.Close()
		of, err := statefile.Read(ofh, encryption.StateEncryptionDisabled())
		if err != nil {
			t.Fatal(err)
		}
		os := of.State.OutputValue(markerOutput)
		if got, want := os.Value, cty.StringVal("from-new-state"); !want.RawEquals(got) {
			t.Errorf("wrong marker value in backup state file\ngot:  %#v\nwant: %#v", got, want)
		}
	})
}

func TestFilesystem_nonExist(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	ls := NewFilesystem("ishouldntexist", encryption.StateEncryptionDisabled())
	if err := ls.RefreshState(t.Context()); err != nil {
		t.Fatalf("err: %s", err)
	}

	if state := ls.State(); state != nil {
		t.Fatalf("bad: %#v", state)
	}
}

func TestFilesystem_lockUnlockWithoutWrite(t *testing.T) {
	info := NewLockInfo()
	info.Operation = "test"

	ls := testFilesystem(t)

	// Delete the just-created tempfile so that Lock recreates it
	os.Remove(ls.path)

	// Lock the state, and in doing so recreate the tempfile
	lockID, err := ls.Lock(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}

	if !ls.created {
		t.Fatal("should have marked state as created")
	}

	if err := ls.Unlock(t.Context(), lockID); err != nil {
		t.Fatal(err)
	}

	_, err = os.Stat(ls.path)
	if os.IsNotExist(err) {
		// Success! Unlocking the state successfully deleted the tempfile
		return
	} else if err != nil {
		t.Fatalf("unexpected error from os.Stat: %s", err)
	} else {
		os.Remove(ls.readPath)
		t.Fatal("should have removed path, but exists")
	}
}

func TestFilesystem_impl(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	var _ Reader = new(Filesystem)
	var _ Writer = new(Filesystem)
	var _ Persister = new(Filesystem)
	var _ Refresher = new(Filesystem)
	var _ OutputReader = new(Filesystem)
	var _ Locker = new(Filesystem)
}

func newStateWithOutput(t *testing.T, name, value string) *states.State {
	t.Helper()
	return states.BuildState(func(ss *states.SyncState) {
		ss.SetOutputValue(
			addrs.OutputValue{Name: name}.Absolute(addrs.RootModuleInstance),
			cty.StringVal(value),
			false,
			"",
		)
	})
}

func assertStatePreserved(t *testing.T, ls *Filesystem, beforeBytes []byte, beforeMeta SnapshotMeta, beforeReadPath string) {
	t.Helper()
	raw, err := os.ReadFile(ls.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, beforeBytes) {
		t.Errorf("live file bytes changed:\ngot:  %q\nwant: %q", raw, beforeBytes)
	}
	if diff := cmp.Diff(beforeMeta, ls.StateSnapshotMeta()); diff != "" {
		t.Errorf("metadata should be unchanged\n%s", diff)
	}
	if ls.readPath != beforeReadPath {
		t.Errorf("readPath changed from %q to %q", beforeReadPath, ls.readPath)
	}
}

func TestFilesystem_persistState_failuresPreservePreviousSnapshot(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()

	newState := newStateWithOutput(t, "changed", "new value")
	unserializableState := states.BuildState(func(ss *states.SyncState) {
		ss.SetOutputValue(
			addrs.OutputValue{Name: "unserializable"}.Absolute(addrs.RootModuleInstance),
			cty.DynamicVal,
			false,
			"",
		)
	})

	// failingEncryptionSelector fails serialization/encryption of selected
	// payloads (matched by plaintext marker; an empty marker matches every
	// payload) at the encryption boundary, before any filesystem write.
	failOnMarker := func(marker string) encryption.StateEncryption {
		return &failingEncryptionSelector{
			err: fmt.Errorf("synthetic encryption failure"),
			failOn: func(payload []byte) bool {
				return bytes.Contains(payload, []byte(marker))
			},
		}
	}

	t.Run("main-encryption-failure-preserves-live-bytes-and-meta", func(t *testing.T) {
		ls := testFilesystem(t)
		defer os.Remove(ls.readPath)

		beforeBytes, err := os.ReadFile(ls.path)
		if err != nil {
			t.Fatal(err)
		}
		beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath
		beforeVersion := ls.file.TerraformVersion.String()

		if err := ls.WriteState(newState); err != nil {
			t.Fatal(err)
		}
		ls.encryption = &failureEncryptionAll{}

		if err := ls.PersistState(t.Context(), nil); err == nil {
			t.Fatal("expected an error from a failing encryption")
		}
		if ls.writtenBackup {
			t.Error("writtenBackup must not be set by a failed persist")
		}

		assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)
		if got := ls.file.TerraformVersion.String(); got != beforeVersion {
			t.Errorf("failed persist changed in-memory TerraformVersion: got %q, want %q", got, beforeVersion)
		}

		// Retry with working encryption: the previous snapshot bytes are
		// intact and the retry must commit exactly one serial increment.
		ls.encryption = encryption.StateEncryptionDisabled()
		if err := ls.PersistState(t.Context(), nil); err != nil {
			t.Fatalf("unexpected error on retry: %s", err)
		}
		got := readStatefileForTest(t, ls.path)
		if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
			t.Error("retried persist should have written the new state")
		}
		if got := ls.readPath; got != ls.path {
			t.Errorf("readPath should be the output path after success, got %q", got)
		}
		if meta := ls.StateSnapshotMeta(); meta.Serial != beforeMeta.Serial+1 {
			t.Errorf("serial after retry: got %d, want %d", meta.Serial, beforeMeta.Serial+1)
		}
	})

	t.Run("backup-encryption-failure-preserves-live-and-sentinel", func(t *testing.T) {
		f, err := os.CreateTemp("", "tf")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		defer os.Remove(f.Name())
		// A pre-existing backup file (a sentinel, here) must not be replaced
		// by a backup whose serialization fails.
		sentinel := []byte("sentinel-backup-bytes")
		if err := os.WriteFile(f.Name(), sentinel, 0600); err != nil {
			t.Fatal(err)
		}

		ls := testFilesystem(t)
		defer os.Remove(ls.readPath)
		ls.SetBackupPath(f.Name())

		beforeBytes, err := os.ReadFile(ls.path)
		if err != nil {
			t.Fatal(err)
		}
		beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

		if err := ls.WriteState(newState); err != nil {
			t.Fatal(err)
		}
		// Fail only the backup payload, which carries the old snapshot.
		ls.encryption = failOnMarker("it's a secret")

		if err := ls.PersistState(t.Context(), nil); err == nil {
			t.Fatal("expected an error from a failing backup serialization")
		}

		rawBackup, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(rawBackup, sentinel) {
			t.Errorf("existing backup file was touched:\ngot:  %q\nwant: %q", rawBackup, sentinel)
		}
		if ls.writtenBackup {
			t.Error("writtenBackup must not be set by a failed backup write")
		}
		assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)

		// Retry with working encryption: the first successful persist is the
		// only backup, taken from the old output file.
		ls.encryption = encryption.StateEncryptionDisabled()
		if err := ls.PersistState(t.Context(), nil); err != nil {
			t.Fatalf("unexpected error on retry: %s", err)
		}
		if !ls.writtenBackup {
			t.Error("writtenBackup should be set after a successful backup write")
		}
		bf := readStatefileForTest(t, f.Name())
		if bf.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) != nil {
			t.Error("backup must contain the previous snapshot, not the new state")
		}
		if !bf.State.Equal(TestFullInitialState()) {
			t.Errorf("backup does not contain the initial snapshot:\n%s", spew.Sdump(bf.State))
		}

		// A second successful persist must not overwrite the backup again.
		firstBackup, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := ls.PersistState(t.Context(), nil); err != nil {
			t.Fatalf("unexpected error on repeated persist: %s", err)
		}
		secondBackup, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstBackup, secondBackup) {
			t.Error("backup must only ever be written for the first snapshot")
		}
	})

	t.Run("main-encryption-failure-after-backup-staging-preserves-all", func(t *testing.T) {
		f, err := os.CreateTemp("", "tf")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		defer os.Remove(f.Name())
		// The backup payload serialization succeeds here, but the backup
		// file must still not be written: both stages must pass first.
		sentinel := []byte("sentinel-backup-bytes")
		if err := os.WriteFile(f.Name(), sentinel, 0600); err != nil {
			t.Fatal(err)
		}

		ls := testFilesystem(t)
		defer os.Remove(ls.readPath)
		ls.SetBackupPath(f.Name())

		beforeBytes, err := os.ReadFile(ls.path)
		if err != nil {
			t.Fatal(err)
		}
		beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

		if err := ls.WriteState(newState); err != nil {
			t.Fatal(err)
		}
		// Fail only the main payload; the backup payload passes.
		ls.encryption = failOnMarker("new value")

		if err := ls.PersistState(t.Context(), nil); err == nil {
			t.Fatal("expected an error from a failing main serialization")
		}

		rawBackup, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(rawBackup, sentinel) {
			t.Errorf("backup sentinel was touched although the write never happened:\ngot:  %q\nwant: %q", rawBackup, sentinel)
		}
		if ls.writtenBackup {
			t.Error("backup must not be created when the main serialization fails")
		}
		assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)

		// Retry with working encryption: backup then comes from the old
		// output file and the live file gets the new state.
		ls.encryption = encryption.StateEncryptionDisabled()
		if err := ls.PersistState(t.Context(), nil); err != nil {
			t.Fatalf("unexpected error on retry: %s", err)
		}
		bf := readStatefileForTest(t, f.Name())
		if bf.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) != nil {
			t.Error("backup must not contain the new state")
		}
		got := readStatefileForTest(t, ls.path)
		if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
			t.Error("retried persist should have written the new state")
		}
	})

	t.Run("serialization-failure-preserves-live", func(t *testing.T) {
		ls := testFilesystem(t)
		defer os.Remove(ls.readPath)

		beforeBytes, err := os.ReadFile(ls.path)
		if err != nil {
			t.Fatal(err)
		}
		beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

		if err := ls.WriteState(unserializableState); err != nil {
			t.Fatal(err)
		}

		// A state with an unserializable value must fail staging before any
		// destructive I/O, leaving the prior snapshot untouched.
		if err := ls.PersistState(t.Context(), nil); err == nil {
			t.Fatal("expected an error from an unserializable state")
		}

		assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)
		got := readStatefileForTest(t, ls.path)
		if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("unserializable")) != nil {
			t.Error("unserializable output must not have been written")
		}

		// Retry with a serializable accepted state: one serial increment.
		if err := ls.WriteState(newState); err != nil {
			t.Fatal(err)
		}
		if err := ls.PersistState(t.Context(), nil); err != nil {
			t.Fatalf("unexpected error on retry: %s", err)
		}
		got = readStatefileForTest(t, ls.path)
		if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
			t.Error("retried persist should have written the new state")
		}
		if meta := ls.StateSnapshotMeta(); meta.Serial != beforeMeta.Serial+1 {
			t.Errorf("serial after retry: got %d, want %d", meta.Serial, beforeMeta.Serial+1)
		}
	})

	t.Run("locked-persist-failure-preserves-live-and-handle", func(t *testing.T) {
		ls := testFilesystem(t)
		defer os.Remove(ls.readPath)

		info := NewLockInfo()
		info.Operation = "test"
		lockID, err := ls.Lock(t.Context(), info)
		if err != nil {
			t.Fatal(err)
		}

		beforeBytes, err := os.ReadFile(ls.path)
		if err != nil {
			t.Fatal(err)
		}
		beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

		if err := ls.WriteState(newState); err != nil {
			t.Fatal(err)
		}
		ls.encryption = &failureEncryptionAll{}

		if err := ls.PersistState(t.Context(), nil); err == nil {
			t.Fatal("expected an error from a failing encryption")
		}

		// A successfully locked handle must stay in place and usable.
		if ls.stateFileOut == nil {
			t.Error("locked handle should not have been replaced on persist failure")
		}
		if ls.lockID != lockID {
			t.Errorf("lockID changed from %q to %q", lockID, ls.lockID)
		}
		assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)

		// Retry while still locked with working encryption.
		ls.encryption = encryption.StateEncryptionDisabled()
		if err := ls.PersistState(t.Context(), nil); err != nil {
			t.Fatalf("unexpected error on retry: %s", err)
		}
		got := readStatefileForTest(t, ls.path)
		if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
			t.Error("retried persist should have written the new state")
		}
		if got := ls.readPath; got != ls.path {
			t.Errorf("readPath should be the output path after success, got %q", got)
		}

		if err := ls.Unlock(t.Context(), lockID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("backup-create-failure-preserves-live-and-sentinel", func(t *testing.T) {
		ls := testFilesystem(t)
		defer os.Remove(ls.readPath)

		// The backup file exists but is not writable, so creating it anew
		// fails even though its serialization succeeded.
		bfPath := ls.readPath + ".backup"
		defer os.Remove(bfPath)
		sentinel := []byte("sentinel-backup-bytes")
		if err := os.WriteFile(bfPath, sentinel, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(bfPath, 0444); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Chmod(bfPath, 0644); err != nil {
				t.Fatal(err)
			}
		}()
		ls.SetBackupPath(bfPath)

		beforeBytes, err := os.ReadFile(ls.path)
		if err != nil {
			t.Fatal(err)
		}
		beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

		if err := ls.WriteState(newState); err != nil {
			t.Fatal(err)
		}
		if err := ls.PersistState(t.Context(), nil); err == nil {
			t.Fatal("expected an error from a failing backup file creation")
		}

		rawBackup, err := os.ReadFile(bfPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(rawBackup, sentinel) {
			t.Errorf("unwritable backup sentinel was touched:\ngot:  %q\nwant: %q", rawBackup, sentinel)
		}
		if ls.writtenBackup {
			t.Error("writtenBackup must not be set when the backup file cannot be written")
		}
		assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)

		// Repair the permissions and retry: backup and main write succeed.
		if err := os.Chmod(bfPath, 0644); err != nil {
			t.Fatal(err)
		}
		if err := ls.PersistState(t.Context(), nil); err != nil {
			t.Fatalf("unexpected error on retry: %s", err)
		}
		if !ls.writtenBackup {
			t.Error("writtenBackup should be set after a successful retry")
		}
		got := readStatefileForTest(t, ls.path)
		if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
			t.Error("retried persist should have written the new state")
		}
		bf := readStatefileForTest(t, bfPath)
		if !bf.State.Equal(TestFullInitialState()) {
			t.Errorf("backup does not contain the initial snapshot:\n%s", spew.Sdump(bf.State))
		}
	})

	t.Run("preparation-failure-is-not-swallowed-and-preserves-file", func(t *testing.T) {
		ls := testFilesystem(t)

		beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

		// Corrupt the output file so preparation (reading the existing
		// snapshot for the backup) fails.
		garbage := []byte("%%% not a state file %%%")
		if err := os.WriteFile(ls.path, garbage, 0666); err != nil {
			t.Fatal(err)
		}

		if err := ls.WriteState(newState); err != nil {
			t.Fatal(err)
		}
		if err := ls.PersistState(t.Context(), nil); err == nil {
			t.Fatal("expected the preparation error to be returned, not swallowed")
		}

		if ls.stateFileOut != nil {
			t.Error("the opened handle should have been closed and reset after failed unlocked preparation")
		}
		raw, err := os.ReadFile(ls.path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(raw, garbage) {
			t.Errorf("file bytes changed:\ngot:  %q\nwant: %q", raw, garbage)
		}
		if diff := cmp.Diff(beforeMeta, ls.StateSnapshotMeta()); diff != "" {
			t.Errorf("metadata should be unchanged\n%s", diff)
		}
		if ls.readPath != beforeReadPath {
			t.Errorf("readPath changed from %q to %q", beforeReadPath, ls.readPath)
		}

		// Retry against the still-corrupted file: still not swallowed.
		if err := ls.PersistState(t.Context(), nil); err == nil {
			t.Fatal("retry with corrupt file should fail too")
		}

		// Fix the file and retry: the persist must now succeed.
		writeStatefileForTest(t, ls.path, &statefile.File{
			Lineage:          "test-lineage",
			Serial:           0,
			TerraformVersion: version.Must(version.NewVersion("1.2.3")),
			State:            TestFullInitialState(),
		})

		if err := ls.PersistState(t.Context(), nil); err != nil {
			t.Fatalf("retry after fixing the file should succeed: %s", err)
		}
		got := readStatefileForTest(t, ls.path)
		if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
			t.Error("retried persist should have written the new state")
		}
		if meta := ls.StateSnapshotMeta(); meta.Serial != beforeMeta.Serial+1 {
			t.Errorf("serial after retry: got %d, want %d", meta.Serial, beforeMeta.Serial+1)
		}
		os.Remove(ls.readPath)
	})
}

func TestFilesystem_lockPreparationFailure(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	ls := testFilesystem(t)
	defer os.Remove(ls.readPath)

	beforeMeta := ls.StateSnapshotMeta()

	// Corrupt the output file so the lock-time preparation (reading the
	// existing snapshot for the backup) fails.
	garbage := []byte("%%% not a state file %%%")
	if err := os.WriteFile(ls.path, garbage, 0666); err != nil {
		t.Fatal(err)
	}

	info := NewLockInfo()
	info.Operation = "test"
	if _, err := ls.Lock(t.Context(), info); err == nil {
		t.Fatal("expected the lock-time preparation error to be returned")
	}

	// The failed unlocked handle must be closed and reset, releasing the OS
	// lock, so a retry cannot bypass the failed preparation. No lock info
	// file may linger from the aborted lock attempt.
	if ls.stateFileOut != nil {
		t.Error("handle should have been closed and reset after failed lock-time preparation")
	}
	if ls.lockID != "" {
		t.Errorf("lockID should be empty, got %q", ls.lockID)
	}
	if _, err := os.Stat(ls.lockInfoPath()); !os.IsNotExist(err) {
		t.Errorf("lock info file should not exist, got %s", err)
	}
	raw, err := os.ReadFile(ls.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, garbage) {
		t.Errorf("file bytes changed:\ngot:  %q\nwant: %q", raw, garbage)
	}
	if diff := cmp.Diff(beforeMeta, ls.StateSnapshotMeta()); diff != "" {
		t.Errorf("metadata should be unchanged\n%s", diff)
	}

	// An unrepaired retry of PersistState must fail again without writing.
	if err := ls.WriteState(newStateWithOutput(t, "changed", "new value")); err != nil {
		t.Fatal(err)
	}
	if err := ls.PersistState(t.Context(), nil); err == nil {
		t.Fatal("expected the unrepaired retry to fail rather than overwrite")
	}
	raw, err = os.ReadFile(ls.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, garbage) {
		t.Errorf("failed retry overwrote the file:\ngot:  %q\nwant: %q", raw, garbage)
	}

	// Repair the file: persistence then succeeds.
	writeStatefileForTest(t, ls.path, &statefile.File{
		Lineage:          "test-lineage",
		Serial:           0,
		TerraformVersion: version.Must(version.NewVersion("1.2.3")),
		State:            TestFullInitialState(),
	})
	if err := ls.PersistState(t.Context(), nil); err != nil {
		t.Fatalf("retry after repairing the file should succeed: %s", err)
	}
	got := readStatefileForTest(t, ls.path)
	if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
		t.Error("repaired retry should have written the new state")
	}
	if meta := ls.StateSnapshotMeta(); meta.Serial != beforeMeta.Serial+1 {
		t.Errorf("serial after repaired retry: got %d, want %d", meta.Serial, beforeMeta.Serial+1)
	}

	// Locking works again once the file is usable again.
	newInfo := NewLockInfo()
	newInfo.Operation = "test"
	lockID, err := ls.Lock(t.Context(), newInfo)
	if err != nil {
		t.Fatalf("lock after repair should succeed: %s", err)
	}
	if err := ls.Unlock(t.Context(), lockID); err != nil {
		t.Fatal(err)
	}
	os.Remove(ls.readPath)
}

// This test verifies that a successful nil-state persist commits the output
// path as the new authoritative read source, so a later RefreshState does not
// resurrect the state still present on the original input path when input and
// output paths differ.
func TestFilesystem_nilStateClearDoesNotResurrectReadPathState(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	workDir := t.TempDir()

	inPath := filepath.Join(workDir, "input.tfstate")
	outPath := filepath.Join(workDir, "output.tfstate")
	writeStatefileForTest(t, inPath, &statefile.File{
		Lineage:          "test-lineage",
		Serial:           0,
		TerraformVersion: version.Must(version.NewVersion("1.2.3")),
		State:            TestFullInitialState(),
	})

	ls := NewFilesystemBetweenPaths(inPath, outPath, encryption.StateEncryptionDisabled())
	if err := ls.RefreshState(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := ls.State(); state == nil {
		t.Fatal("expected the input snapshot to be loaded")
	}

	if err := ls.WriteState(nil); err != nil {
		t.Fatal(err)
	}
	if err := ls.PersistState(t.Context(), nil); err != nil {
		t.Fatalf("nil-state persist failed: %s", err)
	}

	// A refresh after clearing must not bring the input state back.
	if err := ls.RefreshState(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := ls.State(); state != nil {
		t.Fatalf("refresh after nil-state persist resurrected state:\n%s", spew.Sdump(state))
	}

	stat, err := os.Stat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() != 0 {
		t.Fatalf("output file should be empty after clearing, has size %d", stat.Size())
	}

	// The input file must never be touched by this manager.
	got := readStatefileForTest(t, inPath)
	if !got.State.Equal(TestFullInitialState()) {
		t.Errorf("input snapshot changed:\n%s", spew.Sdump(got.State))
	}
}

func readStatefileForTest(t *testing.T, path string) *statefile.File {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	f, err := statefile.Read(fh, encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatalf("failed to read statefile at %s: %s", path, err)
	}
	return f
}

func writeStatefileForTest(t *testing.T, path string, f *statefile.File) {
	t.Helper()
	fh, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	// WriteForTest honors f.TerraformVersion instead of stamping the current
	// test version, so callers control the stamped version explicitly.
	if err := statefile.WriteForTest(f, fh); err != nil {
		t.Fatal(err)
	}
}

// stateWithUnserializableValues returns two broken states: one with an
// instance object of a status that cannot be serialized, and one with an
// unknown (dynamic) output value. Both fail writeStateV4 conversion only, so
// no encryption failure is needed to make their persistence fail.
func brokenStatesForTest(t *testing.T) map[string]*states.State {
	t.Helper()
	invalidStatus := states.NewState()
	invalidStatus.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "test_thing",
			Name: "oops",
		}.Instance(addrs.IntKey(0)),
		&states.ResourceInstanceObjectSrc{
			Status:        states.ObjectPlanned,
			SchemaVersion: 1,
			AttrsJSON:     []byte(`{"woozles":"confuzles"}`),
		},
		addrs.AbsProviderConfig{
			Provider: addrs.NewDefaultProvider("test"),
			Module:   addrs.RootModule,
		},
		addrs.NoKey,
	)
	unknownOutput := states.BuildState(func(ss *states.SyncState) {
		ss.SetOutputValue(
			addrs.OutputValue{Name: "odd"}.Absolute(addrs.RootModuleInstance),
			cty.DynamicVal,
			false,
			"",
		)
	})
	return map[string]*states.State{
		"invalid-instance-status": invalidStatus,
		"unknown-output-value":    unknownOutput,
	}
}

// This test proves that conversion/serialization failures (with a working
// encryption) preserve exact prior live and backup bytes, metadata, readPath
// and writtenBackup, and that a repaired retry succeeds.
func TestFilesystem_persistState_conversionFailurePreservesSnapshotWithBackup(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()

	repaired := newStateWithOutput(t, "changed", "new value")

	for name, broken := range brokenStatesForTest(t) {
		t.Run(name, func(t *testing.T) {
			ls := testFilesystem(t)
			defer os.Remove(ls.readPath)

			// Existing backup with sentinel content that only a successful
			// backup write may replace.
			bfPath := ls.readPath + ".backup"
			defer os.Remove(bfPath)
			sentinel := []byte("sentinel-backup-bytes")
			if err := os.WriteFile(bfPath, sentinel, 0600); err != nil {
				t.Fatal(err)
			}
			ls.SetBackupPath(bfPath)

			beforeBytes, err := os.ReadFile(ls.path)
			if err != nil {
				t.Fatal(err)
			}
			beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

			if err := ls.WriteState(broken); err != nil {
				t.Fatal(err)
			}
			// Encryption stays enabled and healthy: conversion must fail on
			// its own, before any destructive I/O.
			if err := ls.PersistState(t.Context(), nil); err == nil {
				t.Fatalf("expected an error from a non-serializable state (%s)", name)
			}

			rawBackup, err := os.ReadFile(bfPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(rawBackup, sentinel) {
				t.Errorf("existing backup sentinel changed:\ngot:  %q\nwant: %q", rawBackup, sentinel)
			}
			if ls.writtenBackup {
				t.Error("writtenBackup must remain false after a failed persist")
			}
			assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)
			if got := readStatefileForTest(t, ls.path); got.State.OutputValue(addrs.RootModuleInstance.OutputValue("odd")) != nil {
				t.Errorf("broken output leaked into the live file:\n%s", spew.Sdump(got.State))
			}

			// Repaired retry: persists the new state and finally writes the
			// backup taken from the old output file.
			if err := ls.WriteState(repaired); err != nil {
				t.Fatal(err)
			}
			if err := ls.PersistState(t.Context(), nil); err != nil {
				t.Fatalf("unexpected error on repaired retry: %s", err)
			}
			got := readStatefileForTest(t, ls.path)
			if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
				t.Error("repaired retry should have written the new state")
			}
			if ls.writtenBackup {
				bf := readStatefileForTest(t, bfPath)
				if bf.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) != nil {
					t.Error("backup must contain the previous snapshot, not the new state")
				}
				if !bf.State.Equal(TestFullInitialState()) {
					t.Errorf("backup does not contain the initial snapshot:\n%s", spew.Sdump(bf.State))
				}
			} else {
				t.Error("writtenBackup should be true after the repaired retry")
			}
			if got := ls.readPath; got != ls.path {
				t.Errorf("readPath should be the output path after success, got %q", got)
			}
			if meta := ls.StateSnapshotMeta(); meta.Serial != beforeMeta.Serial+1 {
				t.Errorf("serial after repaired retry: got %d, want %d", meta.Serial, beforeMeta.Serial+1)
			}
		})
	}
}

// This test proves that the version stamped by a failed candidate is isolated
// to the discarded candidate: an old-versioned live snapshot keeps its old
// in-memory TerraformVersion after a failed persist, and the repaired retry
// commits the new current version only on success.
func TestFilesystem_persistState_terraformVersionIsolatedFromFailedCandidate(t *testing.T) {
	restoreCurrent := testOverrideVersion(t, "4.3.0")
	defer restoreCurrent()

	f, err := os.CreateTemp("", "tf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()

	// Preload a snapshot stamped with an older version than the one the
	// current persist would stamp.
	writeStatefileForTest(t, f.Name(), &statefile.File{
		Lineage:          "test-lineage",
		Serial:           0,
		TerraformVersion: version.Must(version.NewVersion("1.0.0")),
		State:            newStateWithOutput(t, "original", "old value"),
	})

	ls := NewFilesystem(f.Name(), encryption.StateEncryptionDisabled())
	if err := ls.RefreshState(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := ls.StateSnapshotMeta().TerraformVersion; got == nil || got.String() != "1.0.0" {
		t.Fatalf("preloaded snapshot should be stamped 1.0.0, got %v", got)
	}

	beforeBytes, err := os.ReadFile(ls.path)
	if err != nil {
		t.Fatal(err)
	}
	beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

	if err := ls.WriteState(newStateWithOutput(t, "changed", "new value")); err != nil {
		t.Fatal(err)
	}
	ls.encryption = &failureEncryptionAll{}
	if err := ls.PersistState(t.Context(), nil); err == nil {
		t.Fatal("expected an error from a failing encryption")
	}

	// The failed candidate must leave the old version in memory.
	if got := ls.StateSnapshotMeta().TerraformVersion; got == nil || got.String() != "1.0.0" {
		t.Errorf("in-memory TerraformVersion changed after failed persist: got %v, want 1.0.0", got)
	}
	assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)
	if ls.writtenBackup {
		t.Error("writtenBackup must not be set by a failed persist")
	}
	live := readStatefileForTest(t, ls.path)
	if at := live.TerraformVersion; at == nil || at.String() != "1.0.0" {
		t.Errorf("live file should still be stamped 1.0.0, got %v", at)
	}

	// The repaired retry commits the new current version.
	ls.encryption = encryption.StateEncryptionDisabled()
	if err := ls.PersistState(t.Context(), nil); err != nil {
		t.Fatalf("unexpected error on repaired retry: %s", err)
	}
	live = readStatefileForTest(t, ls.path)
	if at := live.TerraformVersion; at == nil || at.String() != "4.3.0" {
		t.Errorf("repaired retry should stamp the current version 4.3.0, got %v", at)
	}
	if got := ls.StateSnapshotMeta().TerraformVersion; got == nil || got.String() != "4.3.0" {
		t.Errorf("metadata TerraformVersion should be 4.3.0 after the retry, got %v", got)
	}
	if got := ls.readPath; got != ls.path {
		t.Errorf("readPath should be the output path after success, got %q", got)
	}
	if meta := ls.StateSnapshotMeta(); meta.Serial != beforeMeta.Serial+1 {
		t.Errorf("serial after repaired retry: got %d, want %d", meta.Serial, beforeMeta.Serial+1)
	}
}

// This test proves the failure/retry readPath behavior with distinct input and
// output paths: after a failed persist the older input snapshot stays
// authoritative (a refresh loads it and the empty output file is not promoted),
// and after a successful retry the output path is the authoritative read
// source (a refresh loads the new snapshot).
func TestFilesystem_persistState_distinctPathsFailureAndRetry(t *testing.T) {
	restoreCurrent := testOverrideVersion(t, "4.3.0")
	defer restoreCurrent()

	workDir := t.TempDir()
	inPath := filepath.Join(workDir, "input.tfstate")
	outPath := filepath.Join(workDir, "output.tfstate")

	// Preload the older input snapshot, stamped with an older version.
	writeStatefileForTest(t, inPath, &statefile.File{
		Lineage:          "test-lineage",
		Serial:           0,
		TerraformVersion: version.Must(version.NewVersion("1.0.0")),
		State:            newStateWithOutput(t, "original", "old value"),
	})

	ls := NewFilesystemBetweenPaths(inPath, outPath, encryption.StateEncryptionDisabled())
	if err := ls.RefreshState(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := ls.State(); state.OutputValue(addrs.RootModuleInstance.OutputValue("original")) == nil {
		t.Fatalf("input snapshot was not pre-loaded:\n%s", spew.Sdump(state))
	}
	beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

	if err := ls.WriteState(newStateWithOutput(t, "changed", "new value")); err != nil {
		t.Fatal(err)
	}
	ls.encryption = &failureEncryptionAll{}
	if err := ls.PersistState(t.Context(), nil); err == nil {
		t.Fatal("expected an error from a failing encryption")
	}

	// After the failure the input file remains untouched and authoritative.
	got := readStatefileForTest(t, inPath)
	if at := got.TerraformVersion; at == nil || at.String() != "1.0.0" {
		t.Errorf("input file should still be stamped 1.0.0, got %v", at)
	}
	if diff := cmp.Diff(beforeMeta, ls.StateSnapshotMeta()); diff != "" {
		t.Errorf("metadata should be unchanged\n%s", diff)
	}
	if got := ls.readPath; got != beforeReadPath || beforeReadPath != inPath {
		t.Errorf("readPath should still be the input path after failure, got %q", got)
	}
	if ls.writtenBackup {
		t.Error("writtenBackup must not be set by a failed persist")
	}
	if stat, err := os.Stat(outPath); err != nil {
		t.Fatalf("output file should exist after the opened-handle failure, got %s", err)
	} else if stat.Size() != 0 {
		t.Errorf("failed persist should not have written the output file, size %d", stat.Size())
	}
	if err := ls.RefreshState(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := ls.State(); state == nil || state.OutputValue(addrs.RootModuleInstance.OutputValue("original")) == nil {
		t.Fatalf("refresh after failure should load the old input snapshot, got:\n%s", spew.Sdump(state))
	}

	// Repaired retry: re-establish the accepted transient state (the earlier
	// failure-phase refresh legitimately returned the manager to the state
	// on disk), persist it, and confirm the readPath switches to the output
	// path so a subsequent refresh loads the new snapshot.
	ls.encryption = encryption.StateEncryptionDisabled()
	if err := ls.WriteState(newStateWithOutput(t, "changed", "new value")); err != nil {
		t.Fatal(err)
	}
	if err := ls.PersistState(t.Context(), nil); err != nil {
		t.Fatalf("unexpected error on repaired retry: %s", err)
	}
	if got := ls.readPath; got != outPath {
		t.Errorf("readPath should be the output path after success, got %q", got)
	}
	outBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(outBytes) == 0 {
		t.Error("output file should contain the new snapshot")
	} else {
		out := readStatefileForTest(t, outPath)
		if out.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
			t.Error("output file should contain the new state")
		}
		if at := out.TerraformVersion; at == nil || at.String() != "4.3.0" {
			t.Errorf("output snapshot should be stamped 4.3.0, got %v", at)
		}
	}
	if err := ls.RefreshState(t.Context()); err != nil {
		t.Fatal(err)
	}
	if state := ls.State(); state == nil || state.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
		t.Fatalf("refresh after retry should load the new output snapshot, got:\n%s", spew.Sdump(state))
	}
	if meta := ls.StateSnapshotMeta(); meta.Serial != beforeMeta.Serial+1 {
		t.Errorf("serial after repaired retry: got %d, want %d", meta.Serial, beforeMeta.Serial+1)
	}
}

// This test proves that resource instance objects carrying Ephemeral marks in
// their TransientPathValueMarks (both current and deposed generations) are
// rejected by the statefile serialization guard at the filesystem boundary:
// the persist returns an error naming the ephemeral leak, no panic occurs,
// and the exact prior live bytes and existing backup sentinel are preserved,
// with a successful corrected-state retry and old-output backup semantics.
func TestFilesystem_persistState_ephemeralMarksPreserveSnapshotWithBackup(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()

	repaired := newStateWithOutput(t, "changed", "new value")

	providerConfig := addrs.AbsProviderConfig{
		Provider: addrs.NewDefaultProvider("test"),
		Module:   addrs.RootModule,
	}
	markedObj := func() *states.ResourceInstanceObjectSrc {
		return &states.ResourceInstanceObjectSrc{
			AttrsJSON:     []byte(`{"id":"x"}`),
			Status:        states.ObjectReady,
			SchemaVersion: 0,
			TransientPathValueMarks: []cty.PathValueMarks{{
				Path:  cty.Path{cty.GetAttrStep{Name: "id"}},
				Marks: cty.NewValueMarks(marks.Ephemeral),
			}},
		}
	}
	validObj := func() *states.ResourceInstanceObjectSrc {
		return &states.ResourceInstanceObjectSrc{
			AttrsJSON:     []byte(`{"id":"x"}`),
			Status:        states.ObjectReady,
			SchemaVersion: 0,
		}
	}
	resourceAddr := func(prefix string) addrs.ResourceInstance {
		return addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "test_thing",
			Name: prefix,
		}.Instance(addrs.IntKey(0))
	}

	currentState := states.NewState()
	currentState.RootModule().SetResourceInstanceCurrent(
		resourceAddr("current"), markedObj(), providerConfig, addrs.NoKey,
	)

	deposedState := states.NewState()
	deposedState.RootModule().SetResourceInstanceCurrent(
		resourceAddr("deposed"), validObj(), providerConfig, addrs.NoKey,
	)
	// The retained rejected-destroy generation carries the ephemeral leak.
	deposedState.RootModule().SetResourceInstanceDeposed(
		resourceAddr("deposed"), states.NewDeposedKey(), markedObj(), providerConfig, addrs.NoKey,
	)

	tests := map[string]*states.State{
		"current-instance": currentState,
		"deposed-instance": deposedState,
	}
	for name, broken := range tests {
		t.Run(name, func(t *testing.T) {
			ls := testFilesystem(t)
			defer os.Remove(ls.readPath)

			bfPath := ls.readPath + ".backup"
			defer os.Remove(bfPath)
			sentinel := []byte("sentinel-backup-bytes")
			if err := os.WriteFile(bfPath, sentinel, 0600); err != nil {
				t.Fatal(err)
			}
			ls.SetBackupPath(bfPath)

			beforeBytes, err := os.ReadFile(ls.path)
			if err != nil {
				t.Fatal(err)
			}
			beforeMeta, beforeReadPath := ls.StateSnapshotMeta(), ls.readPath

			if err := ls.WriteState(broken); err != nil {
				t.Fatal(err)
			}
			// Encryption stays enabled and healthy: only the Stage B
			// statefile guard must fail the candidate.
			err = ls.PersistState(t.Context(), nil)
			if err == nil {
				t.Fatalf("expected an error for an ephemeral-marked snapshot (%s)", name)
			}
			if !strings.Contains(err.Error(), "contains ephemeral values") {
				t.Fatalf("error should come from the ephemeral-marks guard, got: %s", err)
			}

			rawBackup, err := os.ReadFile(bfPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(rawBackup, sentinel) {
				t.Errorf("existing backup sentinel changed:\ngot:  %q\nwant: %q", rawBackup, sentinel)
			}
			if ls.writtenBackup {
				t.Error("writtenBackup must remain false after a failed persist")
			}
			assertStatePreserved(t, ls, beforeBytes, beforeMeta, beforeReadPath)

			// Corrected-state retry: the accepted temporary state no longer
			// carries ephemeral marks, so serialization succeeds and the
			// backup is finally written from the old output file.
			if err := ls.WriteState(repaired); err != nil {
				t.Fatal(err)
			}
			if err := ls.PersistState(t.Context(), nil); err != nil {
				t.Fatalf("unexpected error on corrected retry: %s", err)
			}
			got := readStatefileForTest(t, ls.path)
			if got.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) == nil {
				t.Error("corrected retry should have written the new state")
			}
			if !ls.writtenBackup {
				t.Error("writtenBackup should be true after the corrected retry")
			}
			bf := readStatefileForTest(t, bfPath)
			if bf.State.OutputValue(addrs.RootModuleInstance.OutputValue("changed")) != nil {
				t.Error("backup must contain the previous snapshot, not the new state")
			}
			if !bf.State.Equal(TestFullInitialState()) {
				t.Errorf("backup does not contain the initial snapshot:\n%s", spew.Sdump(bf.State))
			}
			if got := ls.readPath; got != ls.path {
				t.Errorf("readPath should be the output path after success, got %q", got)
			}
			if meta := ls.StateSnapshotMeta(); meta.Serial != beforeMeta.Serial+1 {
				t.Errorf("serial after corrected retry: got %d, want %d", meta.Serial, beforeMeta.Serial+1)
			}
		})
	}
}

func TestFilesystem_persistState_nilStateClearsFile(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	ls := testFilesystem(t)
	defer os.Remove(ls.readPath)

	if err := ls.WriteState(nil); err != nil {
		t.Fatal(err)
	}
	if err := ls.PersistState(t.Context(), nil); err != nil {
		t.Fatalf("err: %s", err)
	}

	stat, err := os.Stat(ls.path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() != 0 {
		t.Fatalf("state file should be empty after clearing, has size %d", stat.Size())
	}
	if state := ls.State(); state != nil {
		t.Fatalf("state should be nil, got:\n%s", spew.Sdump(state))
	}
}

func TestFilesystem_persistState_failureRetainsAcceptedTransientState(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()

	newState := newStateWithOutput(t, "changed", "new value")

	ls := testFilesystem(t)
	defer os.Remove(ls.readPath)

	if err := ls.WriteState(newState); err != nil {
		t.Fatal(err)
	}
	ls.encryption = &failureEncryptionAll{}
	if err := ls.PersistState(t.Context(), nil); err == nil {
		t.Fatal("expected an error")
	}

	// Ordinary persistence keeps the accepted transient state after failure.
	if got := ls.State(); !got.Equal(newState) {
		t.Errorf("transient state should be retained\n%s", cmp.Diff(got, newState))
	}
}

func TestFilesystem_WriteStateForMigration_forcedFailureRestoresFile(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	ls := testFilesystem(t)
	defer os.Remove(ls.readPath)

	before := ls.StateForMigration().DeepCopy()

	migrant := &statefile.File{
		Lineage: "other-lineage",
		Serial:  7,
		State:   newStateWithOutput(t, "migrated", "migrated value"),
	}

	ls.encryption = &failureEncryptionAll{}
	err := ls.WriteStateForMigration(migrant, true)
	if err == nil {
		t.Fatal("expected an error from a failing forced migration")
	}

	after := ls.StateForMigration()
	if diff := cmp.Diff(after, before); diff != "" {
		t.Errorf("failed forced migration should restore the pre-attempt snapshot\n%s", diff)
	}
}

// failureEncryptionAll fails every EncryptState call but passes decryption
// through, so managers that already read their snapshot keep working.
type failureEncryptionAll struct{}

func (e *failureEncryptionAll) DecryptState(b []byte) ([]byte, encryption.EncryptionStatus, error) {
	return b, encryption.StatusSatisfied, nil
}
func (e *failureEncryptionAll) EncryptState([]byte) ([]byte, error) {
	return nil, fmt.Errorf("synthetic encryption failure")
}

// failingEncryptionSelector fails EncryptState for payloads selected by
// failOn, and otherwise passes them through unchanged.
type failingEncryptionSelector struct {
	err    error
	failOn func(payload []byte) bool
}

func (e *failingEncryptionSelector) DecryptState(b []byte) ([]byte, encryption.EncryptionStatus, error) {
	return b, encryption.StatusSatisfied, nil
}
func (e *failingEncryptionSelector) EncryptState(b []byte) ([]byte, error) {
	if e.failOn(b) {
		return nil, e.err
	}
	return b, nil
}

func testFilesystem(t *testing.T) *Filesystem {
	f, err := os.CreateTemp("", "tf")
	if err != nil {
		t.Fatalf("failed to create temporary file %s", err)
	}
	t.Logf("temporary state file at %s", f.Name())

	err = statefile.Write(&statefile.File{
		Lineage:          "test-lineage",
		Serial:           0,
		TerraformVersion: version.Must(version.NewVersion("1.2.3")),
		State:            TestFullInitialState(),
	}, f, encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatalf("failed to write initial state to %s: %s", f.Name(), err)
	}
	f.Close()

	ls := NewFilesystem(f.Name(), encryption.StateEncryptionDisabled())
	if err := ls.RefreshState(t.Context()); err != nil {
		t.Fatalf("initial refresh failed: %s", err)
	}

	return ls
}

// Make sure we can refresh while the state is locked
func TestFilesystem_refreshWhileLocked(t *testing.T) {
	defer testOverrideVersion(t, "1.2.3")()
	f, err := os.CreateTemp("", "tf")
	if err != nil {
		t.Fatalf("err: %s", err)
	}

	err = statefile.Write(&statefile.File{
		Lineage:          "test-lineage",
		Serial:           0,
		TerraformVersion: version.Must(version.NewVersion("1.2.3")),
		State:            TestFullInitialState(),
	}, f, encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatalf("err: %s", err)
	}
	f.Close()

	s := NewFilesystem(f.Name(), encryption.StateEncryptionDisabled())
	defer os.Remove(s.path)

	// lock first
	info := NewLockInfo()
	info.Operation = "test"
	lockID, err := s.Lock(t.Context(), info)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s.Unlock(t.Context(), lockID); err != nil {
			t.Fatal(err)
		}
	}()

	if err := s.RefreshState(t.Context()); err != nil {
		t.Fatal(err)
	}

	readState := s.State()
	if readState == nil {
		t.Fatal("missing state")
	}
}

func TestFilesystem_GetRootOutputValues(t *testing.T) {
	fs := testFilesystem(t)

	outputs, err := fs.GetRootOutputValues(t.Context())
	if err != nil {
		t.Errorf("Expected GetRootOutputValues to not return an error, but it returned %v", err)
	}

	if len(outputs) != 2 {
		t.Errorf("Expected %d outputs, but received %d", 2, len(outputs))
	}
}

func testOverrideVersion(t *testing.T, v string) func() {
	oldVersionStr := tfversion.Version
	oldPrereleaseStr := tfversion.Prerelease
	oldSemVer := tfversion.SemVer

	var newPrereleaseStr string
	if dash := strings.Index(v, "-"); dash != -1 {
		newPrereleaseStr = v[dash+1:]
		v = v[:dash]
	}

	newSemVer, err := version.NewVersion(v)
	if err != nil {
		t.Errorf("invalid override version %q: %s", v, err)
	}
	newVersionStr := newSemVer.String()

	tfversion.Version = newVersionStr
	tfversion.Prerelease = newPrereleaseStr
	tfversion.SemVer = newSemVer

	return func() { // reset function
		tfversion.Version = oldVersionStr
		tfversion.Prerelease = oldPrereleaseStr
		tfversion.SemVer = oldSemVer
	}
}
