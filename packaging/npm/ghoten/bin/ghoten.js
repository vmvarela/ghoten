#!/usr/bin/env node
'use strict';

const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

// Platform packages are the optionalDependencies of this package, so they are the only supported list.
function resolveBinary(platform, arch, resolve) {
  const key = `${platform}-${arch}`;
  const pkg = require('../package.json');
  const name = `@vmvarela/ghoten-${key}`;
  if (!(name in pkg.optionalDependencies)) {
    const supported = Object.keys(pkg.optionalDependencies)
      .map((dep) => dep.slice('@vmvarela/ghoten-'.length))
      .join(', ');
    throw new Error(`unsupported platform "${key}". Supported platforms: ${supported}`);
  }
  let manifest;
  try {
    manifest = resolve(`${name}/package.json`);
  } catch (err) {
    throw new Error(`package ${name}@${pkg.optionalDependencies[name]} is not installed; reinstall without --omit=optional`);
  }
  const bin = path.join(path.dirname(manifest), 'bin', platform === 'win32' ? 'ghoten.exe' : 'ghoten');
  if (!fs.existsSync(bin)) {
    throw new Error(`binary missing from ${name}: ${bin}`);
  }
  return bin;
}

if (require.main === module) {
  let bin;
  try {
    bin = resolveBinary(process.platform, process.arch, require.resolve);
  } catch (err) {
    console.error(`ghoten: ${err.message}`);
    process.exit(1);
  }
  const result = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' });
  if (result.error) {
    console.error(`ghoten: failed to run binary: ${result.error.message}`);
    process.exit(1);
  }
  process.exit(result.status === null ? 1 : result.status);
}

module.exports = { resolveBinary };
