// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planfile

import (
	"archive/zip"
	"bytes"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/vmvarela/ghoten/internal/addrs"
	"github.com/vmvarela/ghoten/internal/configs"
	"github.com/vmvarela/ghoten/internal/configs/configload"
	"github.com/vmvarela/ghoten/internal/depsfile"
	"github.com/vmvarela/ghoten/internal/encryption"
	"github.com/vmvarela/ghoten/internal/plans"
	"github.com/vmvarela/ghoten/internal/providers"
	"github.com/vmvarela/ghoten/internal/states/statefile"
)

type CreateArgs struct {
	// ConfigSnapshot is a snapshot of the configuration that the plan
	// was created from.
	ConfigSnapshot *configload.Snapshot

	// PreviousRunStateFile is a representation of the state snapshot we used
	// as the original input when creating this plan, containing the same
	// information as recorded at the end of the previous apply except for
	// upgrading managed resource instance data to the provider's latest
	// schema versions.
	PreviousRunStateFile *statefile.File

	// BaseStateFile is a representation of the state snapshot we used to
	// create the plan, which is the result of asking the providers to refresh
	// all previously-stored objects to match the current situation in the
	// remote system. (If this plan was created with refreshing disabled,
	// this should be the same as PreviousRunStateFile.)
	StateFile *statefile.File

	// Plan records the plan itself, which is the main artifact inside a
	// saved plan file.
	Plan *plans.Plan

	// DependencyLocks records the dependency lock information that we
	// checked prior to creating the plan, so we can make sure that all of the
	// same dependencies are still available when applying the plan.
	DependencyLocks *depsfile.Locks

	// Schemas is the full set of provider schemas that were in memory at
	// the time the plan was created. If set, a trimmed-down subset
	// of these schemas is stored alongside the plan.
	// This is deliberately typed as a plain map, rather than as
	// *ghoten.Schemas, so that this package doesn't need to depend on
	// the package that collects all of the schemas together.
	Schemas map[addrs.Provider]providers.ProviderSchema

	// Config is the parsed configuration that the plan was created from.
	// It's used only to determine which parts of Schemas are actually
	// needed to render the plan (see Schemas above); it is not itself
	// written to the plan file; ConfigSnapshot is used for that.
	//
	// This may be left nil if Schemas is also nil.
	Config *configs.Config
}

// Create creates a new plan file with the given filename, overwriting any
// file that might already exist there.
//
// A plan file contains both a snapshot of the configuration and of the latest
// state file in addition to the plan itself, so that Ghoten can detect
// if the world has changed since the plan was created and thus refuse to
// apply it.
func Create(filename string, args CreateArgs, enc encryption.PlanEncryption) error {
	buff := bytes.NewBuffer(make([]byte, 0))
	zw := zip.NewWriter(buff)

	// tfplan file
	{
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     tfplanFilename,
			Method:   zip.Deflate,
			Modified: time.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to create tfplan file: %w", err)
		}
		err = writeTfplan(args.Plan, w)
		if err != nil {
			return fmt.Errorf("failed to write plan: %w", err)
		}
	}

	// tfstate file
	{
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     tfstateFilename,
			Method:   zip.Deflate,
			Modified: time.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to create embedded tfstate file: %w", err)
		}
		err = statefile.Write(args.StateFile, w, encryption.StateEncryptionDisabled())
		if err != nil {
			return fmt.Errorf("failed to write state snapshot: %w", err)
		}
	}

	// tfstate-prev file
	{
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     tfstatePreviousFilename,
			Method:   zip.Deflate,
			Modified: time.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to create embedded tfstate-prev file: %w", err)
		}
		err = statefile.Write(args.PreviousRunStateFile, w, encryption.StateEncryptionDisabled())
		if err != nil {
			return fmt.Errorf("failed to write previous state snapshot: %w", err)
		}
	}

	// tfconfig directory
	{
		err := writeConfigSnapshot(args.ConfigSnapshot, zw)
		if err != nil {
			return fmt.Errorf("failed to write config snapshot: %w", err)
		}
	}

	// .terraform.lock.hcl file, containing dependency lock information
	if args.DependencyLocks != nil { // (this was a later addition, so not all callers set it, but main callers should)
		src, diags := depsfile.SaveLocksToBytes(args.DependencyLocks)
		if diags.HasErrors() {
			return fmt.Errorf("failed to write embedded dependency lock file: %w", diags.Err())
		}

		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:     dependencyLocksFilename,
			Method:   zip.Deflate,
			Modified: time.Now(),
		})
		if err != nil {
			return fmt.Errorf("failed to create embedded dependency lock file: %w", err)
		}
		_, err = w.Write(src)
		if err != nil {
			return fmt.Errorf("failed to write embedded dependency lock file: %w", err)
		}
	}

	// tfschemas file, containing the trimmed provider schemas needed to
	// render the plan as JSON
	{
		trimmed := prepareSchemasForWrite(args.Plan, args.Config, args.Schemas)
		if len(trimmed) == 0 {
			log.Printf("[TRACE] planfile: no schemas embedded in plan file; show will need to fetch schemas from providers instead")
		} else {
			log.Printf("[TRACE] planfile: embedding trimmed schemas for %d provider(s) in plan file", len(trimmed))
			w, err := zw.CreateHeader(&zip.FileHeader{
				Name:     tfschemasFilename,
				Method:   zip.Deflate,
				Modified: time.Now(),
			})
			if err != nil {
				return fmt.Errorf("failed to create embedded schemas file: %w", err)
			}
			err = writeSchemas(trimmed, w)
			if err != nil {
				return fmt.Errorf("failed to write embedded schemas file: %w", err)
			}
		}
	}

	// Finish zip file
	zw.Close()
	// Encrypt payload
	encrypted, err := enc.EncryptPlan(buff.Bytes())
	if err != nil {
		return err
	}
	return os.WriteFile(filename, encrypted, 0644)
}
