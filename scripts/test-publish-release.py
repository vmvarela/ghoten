"""Exercise publication ordering and failure isolation without GitHub writes."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('publish-release.sh').resolve()
FAKE_GH = '''#!/usr/bin/env python3
import json, os, pathlib, shutil, sys
args = sys.argv[1:]
with open('calls.jsonl', 'a') as log:
    log.write(json.dumps(args) + '\\n')
mode = os.environ['TEST_MODE']
if args[0] == 'api' and args[-1].endswith('releases?per_page=100'):
    print(json.dumps([{'id': 42, 'draft': True, 'name': 'Next Ghoten release', 'tag_name': 'next'}] if mode != 'new' else []))
elif args[:2] == ['release', 'view']:
    print(42)
elif args[:2] == ['release', 'upload'] and mode == 'upload-failure':
    sys.exit(1)
elif args[:2] == ['release', 'download']:
    dest = pathlib.Path(args[args.index('--dir') + 1])
    for file in pathlib.Path('artifacts').iterdir():
        shutil.copy(file, dest / file.name)
    if mode == 'corruption':
        (dest / 'ghoten.tar.gz').write_bytes(b'corrupt')
    if mode == 'extra':
        (dest / 'unexpected').write_bytes(b'extra')
'''


class PublishReleaseTests(unittest.TestCase):
    def run_case(self, mode, prerelease=False):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            (root / 'bin').mkdir()
            gh = root / 'bin/gh'
            gh.write_text(FAKE_GH)
            gh.chmod(0o755)
            (root / 'artifacts').mkdir()
            (root / 'artifacts/ghoten.tar.gz').write_bytes(b'archive')
            digest = hashlib.sha256(b'archive').hexdigest()
            (root / 'artifacts/sha256sums.txt').write_text(f'{digest}  ghoten.tar.gz\n')
            env = os.environ | {
                'PATH': str(root / 'bin') + ':' + os.environ['PATH'],
                'TAG': 'v1.13.1-rc.2' if prerelease else 'v1.13.1',
                'SOURCE_SHA': 'a' * 40, 'IS_PRERELEASE': str(prerelease).lower(),
                'GITHUB_REPOSITORY': 'test/ghoten', 'TEST_MODE': mode,
            }
            result = subprocess.run(['bash', str(SCRIPT)], cwd=root, env=env,
                                    capture_output=True, text=True)
            calls = [json.loads(line) for line in (root / 'calls.jsonl').read_text().splitlines()]
            published = [call for call in calls if 'draft=false' in call]
            if mode in ('upload-failure', 'corruption', 'extra'):
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(published, [])
            else:
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(len(published), 1)
                self.assertEqual(calls[-1], published[0])
                self.assertIn('make_latest=' + str(not prerelease).lower(), published[0])
                self.assertTrue(any(call[:2] == ['release', 'download'] for call in calls))
                if prerelease:
                    self.assertTrue(any(call[:2] == ['release', 'create'] for call in calls))

    def test_draft_success(self): self.run_case('success')
    def test_new_release(self): self.run_case('new')
    def test_prerelease_preserves_stable_draft(self): self.run_case('success', True)
    def test_upload_failure(self): self.run_case('upload-failure')
    def test_corrupt_download(self): self.run_case('corruption')
    def test_unexpected_asset(self): self.run_case('extra')


if __name__ == '__main__':
    unittest.main()
