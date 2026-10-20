#!/usr/bin/env node
'use strict';

// Usage: node stage.js <version> <binDir> <outDir>
// binDir holds <platform>-<arch>/ghoten (ghoten.exe on win32) for every supported platform.
const fs = require('fs');
const path = require('path');

const [version, binDir, outDir] = process.argv.slice(2);
if (!version || !binDir || !outDir) {
  console.error('usage: stage.js <version> <binDir> <outDir>');
  process.exit(2);
}
if (!/^\d+\.\d+\.\d+$/.test(version)) {
  console.error(`stage.js: version must be stable X.Y.Z, got "${version}"`);
  process.exit(2);
}

const here = __dirname;
const repoRoot = path.resolve(here, '..', '..');
const template = path.join(here, 'ghoten');
const tpl = JSON.parse(fs.readFileSync(path.join(template, 'package.json'), 'utf8'));
const scope = '@vmvarela/ghoten-';
const write = (file, data) => fs.writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`);

// Main package: exact optionalDependency versions so the launcher and binaries always match.
const mainOut = path.join(outDir, 'ghoten');
fs.cpSync(template, mainOut, { recursive: true });
const optional = Object.fromEntries(Object.keys(tpl.optionalDependencies).map((name) => [name, version]));
write(path.join(mainOut, 'package.json'), { ...tpl, version, optionalDependencies: optional });
fs.copyFileSync(path.join(repoRoot, 'LICENSE'), path.join(mainOut, 'LICENSE'));

for (const name of Object.keys(tpl.optionalDependencies)) {
  const key = name.slice(scope.length);
  const cut = key.lastIndexOf('-');
  const os = key.slice(0, cut);
  const cpu = key.slice(cut + 1);
  const exe = os === 'win32' ? 'ghoten.exe' : 'ghoten';
  const dir = path.join(outDir, `ghoten-${key}`);
  fs.mkdirSync(path.join(dir, 'bin'), { recursive: true });
  fs.copyFileSync(path.join(binDir, key, exe), path.join(dir, 'bin', exe));
  fs.chmodSync(path.join(dir, 'bin', exe), 0o755);
  fs.copyFileSync(path.join(repoRoot, 'LICENSE'), path.join(dir, 'LICENSE'));
  write(path.join(dir, 'package.json'), {
    name,
    version,
    description: `Ghoten binary for ${os} ${cpu}`,
    license: tpl.license,
    repository: tpl.repository,
    homepage: tpl.homepage,
    os: [os],
    cpu: [cpu],
    files: ['bin/'],
  });
}
