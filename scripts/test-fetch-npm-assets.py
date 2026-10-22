"""Exercise npm recovery asset download and checksum verification without network access."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('fetch-npm-assets.sh').resolve()
WORKFLOW = Path(__file__).parent.parent / '.github' / 'workflows' / 'release.yml'
VERSION = '1.2.3'
TAG = f'v{VERSION}'
ARCHIVES = [
    f'ghoten_{VERSION}_linux_amd64.tar.gz',
    f'ghoten_{VERSION}_linux_arm64.tar.gz',
    f'ghoten_{VERSION}_darwin_amd64.tar.gz',
    f'ghoten_{VERSION}_darwin_arm64.tar.gz',
    f'ghoten_{VERSION}_windows_amd64.zip',
]
REQUIRED = ARCHIVES + ['sha256sums.txt']

FAKE_GH = '''#!/usr/bin/env python3
"""Serves fixture release metadata and assets to fetch-npm-assets.sh."""
import os, pathlib, shutil, sys

args = sys.argv[1:]
if args[:2] == ['release', 'view']:
    sys.stdout.write(pathlib.Path(os.environ['FAKE_GH_METADATA']).read_text())
    sys.exit(0)
if args[:2] == ['release', 'download']:
    options = dict(zip(args[3::2], args[4::2]))
    name = options['--pattern']
    shutil.copyfile(os.path.join(os.environ['FAKE_GH_ASSETS'], name),
                    os.path.join(options['--dir'], name))
    sys.exit(0)
sys.exit('fake gh: unsupported arguments: ' + ' '.join(args))
'''


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


class RecoveryTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.assets = self.root / 'assets'
        self.assets.mkdir()
        self.dest = self.root / 'artifacts'
        bin_dir = self.root / 'bin'
        bin_dir.mkdir()
        gh = bin_dir / 'gh'
        gh.write_text(FAKE_GH)
        gh.chmod(0o755)
        self.metadata = self.root / 'release.json'
        self.env = {
            **os.environ,
            'PATH': f'{bin_dir}{os.pathsep}{os.environ["PATH"]}',
            'FAKE_GH_ASSETS': str(self.assets),
            'FAKE_GH_METADATA': str(self.metadata),
        }

    def tearDown(self):
        self.tmp.cleanup()

    def write_release(self, *, tag=TAG, draft=False, prerelease=False,
                      asset_names=None, checksum_entries=None):
        for index, name in enumerate(ARCHIVES):
            (self.assets / name).write_bytes(f'archive-{index}'.encode())
        entries = ARCHIVES if checksum_entries is None else checksum_entries
        lines = [f'{sha256(self.assets / name)}  {name}' for name in entries]
        (self.assets / 'sha256sums.txt').write_text('\n'.join(lines) + '\n')
        names = REQUIRED if asset_names is None else asset_names
        self.metadata.write_text(json.dumps({
            'tagName': tag,
            'isDraft': draft,
            'isPrerelease': prerelease,
            'assets': [{'name': name} for name in names],
        }))

    def run_script(self, *args):
        return subprocess.run(['bash', str(SCRIPT), *args], cwd=self.root,
                              env=self.env, text=True, capture_output=True)

    def assert_fails(self, result, message):
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn(message, result.stderr)

    def test_stable_release_is_downloaded_and_verified(self):
        self.write_release()
        result = self.run_script(VERSION, str(self.dest))
        self.assertEqual(result.returncode, 0, result.stderr)
        for name in REQUIRED:
            self.assertTrue((self.dest / name).is_file(), name)
        self.assertFalse((self.dest / 'verified-sha256sums.txt').exists())

    def test_relative_destination_is_supported(self):
        # The workflow passes a relative path.
        self.write_release()
        result = self.run_script(VERSION, 'artifacts')
        self.assertEqual(result.returncode, 0, result.stderr)
        for name in REQUIRED:
            self.assertTrue((self.root / 'artifacts' / name).is_file(), name)

    def test_draft_release_fails_before_download(self):
        self.write_release(draft=True)
        self.assert_fails(self.run_script(VERSION, str(self.dest)), 'published')
        self.assertFalse(self.dest.exists())

    def test_prerelease_fails_before_download(self):
        self.write_release(prerelease=True)
        self.assert_fails(self.run_script(VERSION, str(self.dest)), 'published')
        self.assertFalse(self.dest.exists())

    def test_mismatched_tag_fails(self):
        self.write_release(tag='v9.9.9')
        self.assert_fails(self.run_script(VERSION, str(self.dest)), f'must be {TAG}')

    def test_missing_required_asset_fails(self):
        self.write_release(asset_names=REQUIRED[:-1])
        self.assert_fails(self.run_script(VERSION, str(self.dest)), 'exactly one sha256sums.txt')

    def test_missing_checksum_entry_fails(self):
        self.write_release(checksum_entries=ARCHIVES[1:])
        self.assert_fails(self.run_script(VERSION, str(self.dest)), 'exactly once (found 0)')

    def test_duplicate_checksum_entry_fails(self):
        self.write_release(checksum_entries=ARCHIVES + [ARCHIVES[0]])
        self.assert_fails(self.run_script(VERSION, str(self.dest)), 'exactly once (found 2)')

    def test_altered_archive_fails(self):
        self.write_release()
        (self.assets / ARCHIVES[2]).write_bytes(b'tampered')
        self.assert_fails(self.run_script(VERSION, str(self.dest)), 'checksum verification failed')

    def test_prerelease_version_argument_is_rejected(self):
        self.write_release()
        result = self.run_script(f'{VERSION}-rc.1', str(self.dest))
        self.assertEqual(result.returncode, 2, result.stderr)


class WorkflowRoutingTest(unittest.TestCase):
    """Static checks that recovery cannot bypass verification or invert publication order."""

    def setUp(self):
        self.lines = WORKFLOW.read_text().splitlines()

    def index(self, needle, start=0):
        for number in range(start, len(self.lines)):
            if needle in self.lines[number]:
                return number
        self.fail(f'missing {needle!r} in release.yml')

    def test_npm_only_is_a_release_type(self):
        start = self.index('        options:')
        options = self.lines[start:start + 5]
        self.assertIn('          - npm-only', options)
        self.assertIn('          - stable', options)
        self.assertIn('          - prerelease', options)

    def test_verify_is_skipped_for_npm_only(self):
        verify = self.index('  verify:')
        self.assertIn("if: inputs.release_type != 'npm-only'", self.lines[verify + 2])

    def test_recovery_verifies_assets_before_staging_and_publishing(self):
        download = self.index('fetch-npm-assets.sh')
        extract = self.index('- name: Extract binaries')
        stage = self.index('- name: Stage npm packages', extract)
        publish = self.index('- name: Publish to npm', stage)
        self.assertLess(download, extract)
        self.assertLess(extract, stage)
        self.assertLess(stage, publish)

    def test_publish_keeps_trusted_publishing_gate_and_normal_path(self):
        publish = self.index('  publish-npm:')
        condition = '\n'.join(self.lines[publish:publish + 10])
        self.assertIn("vars.NPM_TRUSTED_PUBLISHING == 'true'", condition)
        self.assertIn("inputs.release_type == 'npm-only'", condition)
        self.assertIn("needs.release.result == 'success'", condition)
        self.assertIn('id-token: write', '\n'.join(self.lines[publish:publish + 20]))


if __name__ == '__main__':
    unittest.main()
