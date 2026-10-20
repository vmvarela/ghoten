'use strict';

// Run: node --test packaging/npm/test/launcher.test.js
const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { test } = require('node:test');
const { resolveBinary } = require('../ghoten/bin/ghoten.js');

const pkgRoot = (name) => path.join(os.tmpdir(), 'node_modules', '@vmvarela', name);

function fakeResolver(name, exe) {
  return (request) => {
    assert.strictEqual(request, `@vmvarela/ghoten-${name}/package.json`);
    const root = pkgRoot(`ghoten-${name}`);
    fs.mkdirSync(path.join(root, 'bin'), { recursive: true });
    fs.writeFileSync(path.join(root, 'package.json'), '{}');
    fs.writeFileSync(path.join(root, 'bin', exe), '');
    return path.join(root, 'package.json');
  };
}

test('resolves the linux-x64 platform binary', () => {
  const bin = resolveBinary('linux', 'x64', fakeResolver('linux-x64', 'ghoten'));
  assert.strictEqual(bin, path.join(pkgRoot('ghoten-linux-x64'), 'bin', 'ghoten'));
});

test('resolves ghoten.exe on win32', () => {
  const bin = resolveBinary('win32', 'x64', fakeResolver('win32-x64', 'ghoten.exe'));
  assert.strictEqual(path.basename(bin), 'ghoten.exe');
});

test('rejects platforms without a package', () => {
  assert.throws(
    () => resolveBinary('linux', 'arm', () => { throw new Error('unreachable'); }),
    /unsupported platform "linux-arm". Supported platforms: linux-x64/
  );
});

test('reports a missing platform package with a reinstall hint', () => {
  assert.throws(
    () => resolveBinary('darwin', 'arm64', () => { throw new Error('not found'); }),
    /ghoten-darwin-arm64@0\.0\.0 is not installed/
  );
});
