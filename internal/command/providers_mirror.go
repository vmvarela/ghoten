// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/apparentlymart/go-versions/versions"
	"github.com/hashicorp/go-getter"
	"github.com/vmvarela/ghoten/internal/command/flags"

	"github.com/vmvarela/ghoten/internal/getproviders"
	"github.com/vmvarela/ghoten/internal/httpclient"
	"github.com/vmvarela/ghoten/internal/tfdiags"
)

// ProvidersMirrorCommand is a Command implementation that implements the
// "tofu providers mirror" command, which populates a directory with
// local copies of provider plugins needed by the current configuration so
// that the mirror can be used to work offline, or similar.
type ProvidersMirrorCommand struct {
	Meta
}

func (c *ProvidersMirrorCommand) Synopsis() string {
	return "Save local copies of all required provider plugins"
}

func (c *ProvidersMirrorCommand) Run(args []string) int {
	args = c.Meta.process(args)
	cmdFlags := c.Meta.defaultFlagSet("providers mirror")
	c.Meta.varFlagSet(cmdFlags)
	var optPlatforms flags.FlagStringSlice
	cmdFlags.Var(&optPlatforms, "platform", "target platform")
	cmdFlags.Usage = func() { c.Ui.Error(c.Help()) }
	if err := cmdFlags.Parse(args); err != nil {
		c.Ui.Error(fmt.Sprintf("Error parsing command-line flags: %s\n", err.Error()))
		return 1
	}

	var diags tfdiags.Diagnostics

	args = cmdFlags.Args()
	if len(args) != 1 {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No output directory specified",
			"The providers mirror command requires an output directory as a command-line argument.",
		))
		c.showDiagnostics(diags)
		return 1
	}
	outputDir := args[0]

	var platforms []getproviders.Platform
	if len(optPlatforms) == 0 {
		platforms = []getproviders.Platform{getproviders.CurrentPlatform}
	} else {
		platforms = make([]getproviders.Platform, 0, len(optPlatforms))
		for _, platformStr := range optPlatforms {
			platform, err := getproviders.ParsePlatform(platformStr)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Invalid target platform",
					fmt.Sprintf("The string %q given in the -platform option is not a valid target platform: %s.", platformStr, err),
				))
				continue
			}
			platforms = append(platforms, platform)
		}
	}

	// Installation steps can be cancelled by SIGINT and similar.
	ctx, done := c.InterruptibleContext(c.CommandContext())
	defer done()

	config, confDiags := c.loadConfig(ctx, ".")
	diags = diags.Append(confDiags)
	reqs, _, moreDiags := config.ProviderRequirements()
	diags = diags.Append(moreDiags)

	// Read lock file
	lockedDeps, lockedDepsDiags := c.Meta.lockedDependenciesWithPredecessorRegistryShimmed()
	diags = diags.Append(lockedDepsDiags)

	// If we have any error diagnostics already then we won't proceed further.
	if diags.HasErrors() {
		c.showDiagnostics(diags)
		return 1
	}

	// If lock file is present, validate it against configuration
	if !lockedDeps.Empty() {
		if errs := config.VerifyDependencySelections(lockedDeps); len(errs) > 0 {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Inconsistent dependency lock file",
				fmt.Sprintf("To update the locked dependency selections to match a changed configuration, run:\n  ghoten init -upgrade\n got:%v", errs),
			))
		}
	}

	// Unlike other commands, this command always consults the origin registry
	// for every provider so that it can be used to update a local mirror
	// directory without needing to first disable that local mirror
	// in the CLI configuration.
	source := getproviders.NewMemoizeSource(
		getproviders.NewRegistrySource(ctx, c.Services, c.registryHTTPClient(ctx), c.ProviderSourceLocationConfig),
	)

	// Providers from registries always use HTTP, so we don't need the full
	// generality of go-getter but it's still handy to use the HTTP getter
	// as an easy way to download over HTTP into a file on disk.
	httpGetter := getter.HttpGetter{
		Client:                httpclient.New(ctx),
		Netrc:                 true,
		XTerraformGetDisabled: true,
	}

	// The following logic is similar to that used by the provider installer
	// in package providercache, but different in a few ways:
	// - It produces the packed directory layout rather than the unpacked
	//   layout we require in provider cache directories.
	// - It generates JSON index files that can be read by the
	//   getproviders.HTTPMirrorSource installation method if the result were
	//   copied into the docroot of an HTTP server.
	// - It can mirror packages for potentially many different target platforms,
	//   so that we can construct a multi-platform mirror regardless of which
	//   platform we run this command on.
	// - It ignores what's already present and just always downloads everything
	//   that the configuration requires. This is a command intended to be run
	//   infrequently to update a mirror, so it doesn't need to optimize away
	//   fetches of packages that might already be present.
	//
	// trustedHashes records the hashes admitted for each exact
	// provider/version/platform identity that was successfully installed
	// during this run. They are merged into the JSON indexes later, so that
	// a trusted hash never gets attributed to a neighboring version or
	// platform.
	trustedHashes := map[mirrorHashKey][]getproviders.Hash{}

	for provider, constraints := range reqs {
		if provider.IsBuiltIn() {
			c.Ui.Output(fmt.Sprintf("- Skipping %s because it is built in to Ghoten CLI", provider.ForDisplay()))
			continue
		}
		constraintsStr := getproviders.VersionConstraintsString(constraints)
		c.Ui.Output(fmt.Sprintf("- Mirroring %s...", provider.ForDisplay()))
		// First we'll look for the latest version that matches the given
		// constraint, which we'll then try to mirror for each target platform.
		acceptable := versions.MeetingConstraints(constraints)
		avail, _, err := source.AvailableVersions(ctx, provider)
		candidates := avail.Filter(acceptable)
		if err == nil && len(candidates) == 0 {
			err = fmt.Errorf("no releases match the given constraints %s", constraintsStr)
		}
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Provider not available",
				fmt.Sprintf("Failed to download %s from its origin registry: %s.", provider.String(), err),
			))
			continue
		}
		selected := candidates.Newest()
		if !lockedDeps.Empty() {
			if lockedDeps.Provider(provider) == nil {
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Provider not found in lockfile",
					fmt.Sprintf("Failed to find %s in the lock file", provider.String()),
				))
				continue
			}
			selected = lockedDeps.Provider(provider).Version()
			c.Ui.Output(fmt.Sprintf("  - Selected v%s to match dependency lock file", selected.String()))
		} else if len(constraintsStr) > 0 {
			c.Ui.Output(fmt.Sprintf("  - Selected v%s to meet constraints %s", selected.String(), constraintsStr))
		} else {
			c.Ui.Output(fmt.Sprintf("  - Selected v%s with no constraints", selected.String()))
		}
		for _, platform := range platforms {
			c.Ui.Output(fmt.Sprintf("  - Downloading package for %s...", platform.String()))
			meta, err := source.PackageMeta(ctx, provider, selected, platform)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Provider release not available",
					fmt.Sprintf("Failed to download %s v%s for %s: %s.", provider.String(), selected.String(), platform.String(), err),
				))
				continue
			}
			httpPkg, ok := meta.Location.(getproviders.PackageHTTPURL)
			if !ok {
				// We don't expect to get non-HTTP locations here because we're
				// using the registry source, so this seems like a bug in the
				// registry source.
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Provider release not available",
					fmt.Sprintf("Failed to download %s v%s for %s: Ghoten's provider registry client returned unexpected location type %T. This is a bug in Ghoten.", provider.String(), selected.String(), platform.String(), meta.Location),
				))
				continue
			}
			urlObj, err := url.Parse(httpPkg.URL)
			if err != nil {
				// We don't expect to get non-HTTP locations here because we're
				// using the registry source, so this seems like a bug in the
				// registry source.
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Invalid URL for provider release",
					fmt.Sprintf("The origin registry for %s returned an invalid URL for v%s on %s: %s.", provider.String(), selected.String(), platform.String(), err),
				))
				continue
			}
			// targetPath is the path where we ultimately want to place the
			// downloaded archive, but we'll place it initially at stagingPath
			// so we can verify its checksums and signatures before making
			// it discoverable to mirror clients. (stagingPath intentionally
			// does not follow the filesystem mirror file naming convention.)
			targetPath := meta.PackedFilePath(outputDir)
			stagingPath := filepath.Join(filepath.Dir(targetPath), "."+filepath.Base(targetPath))
			err = httpGetter.GetFile(stagingPath, urlObj)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Cannot download provider release",
					fmt.Sprintf("Failed to download %s v%s for %s: %s.", provider.String(), selected.String(), platform.String(), err),
				))
				continue
			}
			var platformTrusted []getproviders.Hash
			if meta.Authentication != nil {
				result, err := meta.Authentication.AuthenticatePackage(getproviders.PackageLocalArchive(stagingPath))
				if err != nil {
					diags = diags.Append(tfdiags.Sourceless(
						tfdiags.Error,
						"Invalid provider package",
						fmt.Sprintf("Failed to authenticate %s v%s for %s: %s.", provider.String(), selected.String(), platform.String(), err),
					))
					continue
				}
				// Collect the hashes that the authentication admits for
				// exactly this target platform, including hashes reported
				// by the origin registry and locally-verified hashes. We
				// record them only after the package has been successfully
				// placed into the mirror, below.
				for hash := range result.HashesWithDisposition(func(d *getproviders.HashDisposition) bool {
					isTrusted := d.VerifiedLocally || d.ReportedByRegistry || d.ReportedByTrustedMirror
					return isTrusted && d.Platforms.Has(platform)
				}) {
					platformTrusted = append(platformTrusted, hash)
				}
				c.Ui.Output(fmt.Sprintf("  - Package authenticated: %s", result))
			}
			// Note that this remove-before-rename ordering means a rename
			// failure has already deleted the prior archive: there is no
			// transaction or rollback here. The remove-before-rename is
			// needed on Windows, where renaming over an existing file fails.
			os.Remove(targetPath) // okay if it fails because we're going to try to rename over it next anyway
			err = os.Rename(stagingPath, targetPath)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Cannot download provider release",
					fmt.Sprintf("Failed to place %s package into mirror directory: %s.", provider.String(), err),
				))
				continue
			}
			// The new package is now in place, so its admitted hashes are
			// trusted for exactly this provider, version, and platform.
			trustedHashes[mirrorHashKey{
				provider: provider.String(),
				version:  selected.String(),
				platform: platform.String(),
			}] = platformTrusted
		}
	}

	// Now we'll generate or update the JSON index files in the directory.
	// We do this by scanning the directory to see what is present, rather than
	// by relying on the selections we made above, because we want to still
	// include in the indices any packages that were already present and
	// not affected by the changes we just made.
	//
	// Each version's index entry combines:
	// - the local "h1" hash of the archive file present in the directory,
	// - the trusted hashes recorded during this run for exactly that
	//   provider/version/platform identity (including hashes reported by
	//   the origin registry), and
	// - the hashes from the prior index file for that version, but only for
	//   platform entries whose URL maps to the same local archive; a prior
	//   entry referring to a different archive file is dropped with a
	//   warning rather than inherited.
	//
	// The carried-forward prior hashes are trusted local data recorded by a
	// previous successful run (or hand-authored); they are neither re-verified
	// against the local archive nor covered by any signing key here.
	available, err := getproviders.SearchLocalDirectory(outputDir)
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Failed to update indexes",
			fmt.Sprintf("Could not scan the output directory to get package metadata for the JSON indexes: %s.", err),
		))
		available = nil // the following loop will be a no-op
	}
	for provider, metas := range available {
		if len(metas) == 0 {
			continue // should never happen, but we'll be resilient
		}
		// The index files live in the same directory as the package files,
		// so to figure that out without duplicating the path-building logic
		// we'll ask the getproviders package to build an archive filename
		// for a fictitious package and then use the directory portion of it.
		indexDir := filepath.Dir(getproviders.PackedFilePathForPackage(
			outputDir, provider, versions.Unspecified, getproviders.CurrentPlatform,
		))
		// Group the archive files by version, in deterministic order.
		versionMetas := map[string][]getproviders.PackageMeta{}
		var versionOrder []string
		for _, meta := range metas {
			if _, ok := meta.Location.(getproviders.PackageLocalArchive); !ok {
				// only archive files are eligible to be included in JSON
				// indices for a network mirror.
				continue
			}
			versionStr := meta.Version.String()
			if _, exists := versionMetas[versionStr]; !exists {
				versionOrder = append(versionOrder, versionStr)
			}
			versionMetas[versionStr] = append(versionMetas[versionStr], meta)
		}
		sort.Strings(versionOrder)

		indexVersions := map[string]interface{}{}
		for _, versionStr := range versionOrder {
			indexPath := filepath.Join(indexDir, versionStr+".json")

			// Read the prior index, if any, so its hashes can be carried
			// forward where they still apply. A missing file is normal.
			prior, priorBytes, err := readMirrorVersionIndex(indexPath)
			if err != nil {
				// We cannot tell whether the prior hashes were valid, so we
				// leave the file untouched rather than overwriting unknown
				// content, and we do not advertise the version in the main
				// index until its index file is valid again.
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Invalid existing version index",
					fmt.Sprintf("Cannot update the JSON index for %s v%s because %s is not valid: %s.", provider.String(), versionStr, filepath.Base(indexPath), err),
				))
				continue
			}

			// Collect the archive metas for this version by platform, in
			// deterministic order.
			platformMetas := make(map[string]getproviders.PackageMeta, len(versionMetas[versionStr]))
			var platformOrder []string
			for _, meta := range versionMetas[versionStr] {
				platformStr := meta.TargetPlatform.String()
				if _, exists := platformMetas[platformStr]; !exists {
					platformOrder = append(platformOrder, platformStr)
				}
				platformMetas[platformStr] = meta
			}
			slices.Sort(platformOrder)

			archiveIndex := map[string]interface{}{}
			for _, platformStr := range platformOrder {
				meta := platformMetas[platformStr]
				archivePath, ok := meta.Location.(getproviders.PackageLocalArchive)
				if !ok {
					continue // filtered out above; resilient anyway
				}
				archiveFilename := filepath.Base(string(archivePath))

				localHash, err := meta.Hash()
				if err != nil {
					diags = diags.Append(tfdiags.Sourceless(
						tfdiags.Error,
						"Failed to update indexes",
						fmt.Sprintf("Failed to determine a hash value for %s v%s on %s: %s.", provider, versionStr, platformStr, err),
					))
					continue
				}
				hashes := map[getproviders.Hash]struct{}{
					localHash: {},
				}
				for _, hash := range trustedHashes[mirrorHashKey{
					provider: provider.String(),
					version:  versionStr,
					platform: platformStr,
				}] {
					hashes[hash] = struct{}{}
				}
				// Carry forward the prior hashes for this platform only if
				// they referred to the same archive file identity.
				if priorArchive, ok := prior[platformStr]; ok {
					if priorArchive.URL == archiveFilename {
						for _, hashStr := range priorArchive.Hashes {
							hashes[getproviders.Hash(hashStr)] = struct{}{}
						}
					} else {
						diags = diags.Append(tfdiags.Sourceless(
							tfdiags.Warning,
							"Ignored existing index hashes",
							fmt.Sprintf("The existing index for %s v%s on %s refers to a different archive file (%q), so its hashes were not carried over.", provider.String(), versionStr, platformStr, priorArchive.URL),
						))
					}
				}
				hashList := make([]string, 0, len(hashes))
				for _, hash := range slices.Sorted(maps.Keys(hashes)) {
					hashList = append(hashList, hash.String())
				}
				archiveIndex[platformStr] = map[string]interface{}{
					"url":    archiveFilename, // a relative URL from the index file's URL
					"hashes": hashList,        // an array to allow for additional hash formats in future
				}
			}

			versionIndex := map[string]interface{}{
				"archives": archiveIndex,
			}
			versionIndexJSON, err := json.MarshalIndent(versionIndex, "", "  ")
			if err != nil {
				// Should never happen because the input here is entirely under
				// our control.
				panic(fmt.Sprintf("failed to encode version index: %s", err))
			}
			if bytes.Equal(priorBytes, versionIndexJSON) {
				// Nothing changed, so the file on disk already advertises
				// exactly what we'd write; treat it as updated without
				// rewriting it.
				indexVersions[versionStr] = map[string]interface{}{}
				continue
			}
			// TODO: Ideally we would do these updates as atomic swap operations by
			// creating a new file and then renaming it over the old one, in case
			// this directory is the docroot of a live mirror. An atomic swap
			// requires platform-specific code though: os.Rename alone can't do it
			// when running on Windows as of Go 1.13. We should revisit this once
			// we're supporting network mirrors, to avoid having them briefly
			// become corrupted during updates.
			err = os.WriteFile(indexPath, versionIndexJSON, 0644)
			if err != nil {
				// The version index didn't get updated, so we don't advertise
				// the version in the main index file: a mirror must not
				// advertise an archive version before its index succeeds.
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Failed to update indexes",
					fmt.Sprintf("Failed to write an updated JSON index for %s v%s: %s.", provider, versionStr, err),
				))
				continue
			}
			indexVersions[versionStr] = map[string]interface{}{}
		}

		mainIndex := map[string]interface{}{
			"versions": indexVersions,
		}
		mainIndexJSON, err := json.MarshalIndent(mainIndex, "", "  ")
		if err != nil {
			// Should never happen because the input here is entirely under
			// our control.
			panic(fmt.Sprintf("failed to encode main index: %s", err))
		}
		mainIndexPath := filepath.Join(indexDir, "index.json")
		priorMainBytes, err := readMirrorModelIndex(mainIndexPath)
		if err != nil {
			// As with a version index, we leave content we cannot parse
			// untouched rather than overwriting it.
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Invalid existing main index",
				fmt.Sprintf("Cannot update the main JSON index for %s because index.json is not valid: %s.", provider.String(), err),
			))
			continue // leave the affected file untouched
		}
		if !bytes.Equal(priorMainBytes, mainIndexJSON) {
			err = os.WriteFile(mainIndexPath, mainIndexJSON, 0644)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(
					tfdiags.Error,
					"Failed to update indexes",
					fmt.Sprintf("Failed to write an updated JSON index for %s: %s.", provider, err),
				))
			}
		}
	}

	c.showDiagnostics(diags)
	if diags.HasErrors() {
		return 1
	}
	return 0
}

func (c *ProvidersMirrorCommand) Help() string {
	return `
Usage: ghoten [global options] providers mirror [options] <target-dir>

  Populates a local directory with copies of the provider plugins needed for
  the current configuration, so that the directory can be used either directly
  as a filesystem mirror or as the basis for a network mirror and thus obtain
  those providers without access to their origin registries in future.

  The mirror directory will contain JSON index files that can be published
  along with the mirrored packages on a static HTTP file server to produce
  a network mirror. Those index files will be ignored if the directory is
  used instead as a local filesystem mirror.

Options:

  -platform=os_arch  Choose which target platform to build a mirror for.
                     By default Ghoten will obtain plugin packages
                     suitable for the platform where you run this command.
                     Use this flag multiple times to include packages for
                     multiple target systems.

                     Target names consist of an operating system and a CPU
                     architecture. For example, "linux_amd64" selects the
                     Linux operating system running on an AMD64 or x86_64
                     CPU. Each provider is available only for a limited
                     set of target platforms.

  -var 'foo=bar'     Set a value for one of the input variables in the root
                     module of the configuration. Use this option more than
                     once to set more than one variable.

  -var-file=filename Load variable values from the given file, in addition
                     to the default files terraform.tfvars and *.auto.tfvars.
                     Use this option more than once to include more than one
                     variables file.
`
}

// mirrorHashKey identifies the exact provider/version/platform identity
// whose hashes are installed into (or indexed for) a mirror directory.
type mirrorHashKey struct {
	provider string
	version  string
	platform string
}

// mirrorArchiveMeta is one platform entry in a generated version index
// file, matching the JSON shape read by HTTPMirrorSource.
type mirrorArchiveMeta struct {
	// URL is the archive file name relative to the index file's directory.
	URL string `json:"url"`
	// Hashes are the hashes previously recorded for that archive file.
	Hashes []string `json:"hashes"`
}

// readMirrorVersionIndex reads the version index file at the given path.
//
// A missing file is normal and returns no prior data. Any other read error
// or malformed JSON is returned as an error so the caller can leave the
// file untouched instead of overwriting unknown content.
func readMirrorVersionIndex(path string) (map[string]mirrorArchiveMeta, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No prior index at all: the normal case for a fresh mirror.
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var prior struct {
		Archives map[string]mirrorArchiveMeta `json:"archives"`
	}
	if err := json.Unmarshal(data, &prior); err != nil {
		return nil, nil, err
	}
	ret := make(map[string]mirrorArchiveMeta, len(prior.Archives))
	for platform, archive := range prior.Archives {
		// The carried-forward hashes must all be syntactically valid hash
		// strings; anything else means the prior index cannot be trusted
		// for merging and must be treated as invalid.
		for _, hashStr := range archive.Hashes {
			if _, err := getproviders.ParseHash(hashStr); err != nil {
				return nil, nil, fmt.Errorf("invalid hash %q in the %q entry: %w", hashStr, platform, err)
			}
		}
		ret[platform] = archive
	}
	return ret, data, nil
}

// readMirrorModelIndex reads the main index.json file at the given path,
// validating that it is JSON we can safely overwrite. A missing file is
// normal and returns no prior bytes. Any other read error or malformed
// JSON is returned as an error so the caller can leave the file untouched.
func readMirrorModelIndex(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var parsed struct {
		Versions map[string]interface{} `json:"versions"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	return data, nil
}
