"""Exercise publication ordering and failure isolation without GitHub writes."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('publish-release.sh').resolve()
REPO = 'test/ghoten'
SOURCE_SHA = 'a' * 40
FAIL_MODES = ('upload-failure', 'corruption', 'extra', 'drift')


def find_release(state, **kw):
    return next(release for release in state['releases']
                if all(release.get(key) == value for key, value in kw.items()))

FAKE_GH = '''#!/usr/bin/env python3
"""Small stateful simulation of the GitHub CLI used by publish-release.sh."""
import json, os, pathlib, re, sys

args = sys.argv[1:]
with open('calls.jsonl', 'a') as log:
    log.write(json.dumps(args) + '\\n')
mode = os.environ.get('TEST_MODE', 'success')
state_path = pathlib.Path('gh-state.json')
REPO = os.environ['GITHUB_REPOSITORY']

def load():
    return json.loads(state_path.read_text())

def save(state):
    state_path.write_text(json.dumps(state))

def find_release(state, **kw):
    for release in state['releases']:
        if all(release.get(k) == v for k, v in kw.items()):
            return release
    return None

def typed(flag, value):
    # -F lets gh infer booleans; -f always sends strings.
    return (value == 'true') if flag == '-F' and value in ('true', 'false') else value

def fail(message):
    sys.exit('gh mock: ' + message)

if not args:
    fail('no arguments')

if args[0] == 'api':
    rest = args[1:]
    method = 'GET'
    if rest[:2] == ['--method', 'PATCH'] or rest[:2] == ['--method', 'DELETE']:
        method = rest[1]
        rest = rest[2:]
    if rest[:1] == ['--paginate']:
        rest = rest[1:]
    endpoint = rest[0]
    fields = {}
    i = 1
    while i < len(rest):
        if rest[i] in ('-f', '-F'):
            key, _, value = rest[i + 1].partition('=')
            fields[key] = typed(rest[i], value)
            i += 2
        elif rest[i] == '--jq':
            i += 2
        else:
            fail('unsupported gh api argument: ' + rest[i])
    prefix = 'repos/' + REPO + '/'
    if not endpoint.startswith(prefix):
        fail('unsupported endpoint: ' + endpoint)
    path = endpoint[len(prefix):]
    state = load()
    by_id = re.fullmatch(r'releases/(\\d+)', path)
    by_assets = re.fullmatch(r'releases/(\\d+)/assets', path)
    by_asset = re.fullmatch(r'releases/assets/(\\d+)', path)

    if path == 'releases?per_page=100' and method == 'GET':
        print(json.dumps(state['releases']))
    elif by_id and method == 'GET':
        release = find_release(state, id=int(by_id.group(1)))
        if release is None:
            fail('release not found')
        if mode == 'drift':
            # Simulate an external actor rewriting the draft after staging.
            release['tag_name'] = 'v9.9.9-drifted'
            release['name'] = 'v9.9.9-drifted'
            save(state)
        print(json.dumps(release))
    elif by_id and method == 'PATCH':
        release = find_release(state, id=int(by_id.group(1)))
        if release is None:
            fail('release not found')
        release.update(fields)
        save(state)
    elif by_assets and method == 'GET':
        release = find_release(state, id=int(by_assets.group(1)))
        if release is None:
            fail('release not found')
        if release['assets']:
            print('\\n'.join(str(asset['id']) for asset in release['assets']))
    elif by_asset and method == 'DELETE':
        asset_id = int(by_asset.group(1))
        for release in state['releases']:
            release['assets'] = [a for a in release['assets'] if a['id'] != asset_id]
        save(state)
    else:
        fail('unsupported gh api call: ' + method + ' ' + path)
    sys.exit(0)

if args[0] != 'release':
    fail('unsupported gh command: ' + repr(args))
sub, rest = args[1], args[2:]

if sub == 'create':
    tag = rest[0]
    has_draft = has_prerelease = has_notes = False
    target = title = None
    i = 1
    while i < len(rest):
        if rest[i] == '--repo':
            if rest[i + 1] != REPO:
                fail('unexpected create repository')
            i += 2
        elif rest[i] == '--target':
            target = rest[i + 1]; i += 2
        elif rest[i] == '--title':
            title = rest[i + 1]; i += 2
        elif rest[i] == '--generate-notes':
            has_notes = True; i += 1
        elif rest[i] == '--draft':
            has_draft = True; i += 1
        elif rest[i] == '--prerelease':
            has_prerelease = True; i += 1
        else:
            fail('unsupported create flag: ' + rest[i])
    if not (has_draft and has_notes and target and title == tag):
        fail('unexpected create options')
    state = load()
    if find_release(state, tag_name=tag) is not None:
        fail('tag already exists')
    state['releases'].append({
        'id': state['next_id'], 'tag_name': tag, 'name': tag, 'body': 'notes',
        'draft': True, 'prerelease': has_prerelease, 'target_commitish': target,
        'assets': [],
    })
    state['next_id'] += 1
    save(state)
elif sub == 'view':
    tag = rest[0]
    if rest[1:] != ['--repo', REPO, '--json', 'databaseId', '--jq', '.databaseId']:
        fail('unexpected view arguments')
    release = find_release(load(), tag_name=tag)
    if release is None:
        fail('release not found')
    print(release['id'])
elif sub == 'upload':
    tag, files = rest[0], rest[1:]
    if files[-2:] != ['--repo', REPO]:
        fail('unexpected upload arguments')
    files = files[:-2]
    if mode == 'upload-failure':
        fail('upload failed')
    state = load()
    release = find_release(state, tag_name=tag)
    if release is None:
        fail('release not found')
    names = {asset['name'] for asset in release['assets']}
    for file in files:
        name = pathlib.Path(file).name
        if name in names:
            fail('asset already uploaded: ' + name)
        release['assets'].append({'id': state['next_id'], 'name': name,
                                  'data': pathlib.Path(file).read_text()})
        state['next_id'] += 1
    save(state)
elif sub == 'download':
    tag = rest[0]
    if rest[1:3] != ['--repo', REPO] or '--dir' not in rest:
        fail('unexpected download arguments')
    dest = pathlib.Path(rest[rest.index('--dir') + 1])
    release = find_release(load(), tag_name=tag)
    if release is None:
        fail('release not found')
    for asset in release['assets']:
        (dest / asset['name']).write_text(asset['data'])
    if mode == 'corruption':
        (dest / 'ghoten.tar.gz').write_text('corrupt')
    if mode == 'extra':
        (dest / 'unexpected').write_text('extra')
else:
    fail('unsupported gh release command: ' + sub)
'''

# Harness-only shim: on macOS, /usr/bin/find is BSD and lacks -printf, which the
# script's file-set gate needs. Serve exactly that invocation and delegate the rest.
FIND_SHIM = r'''#!/usr/bin/env python3
import os, subprocess, sys
args = sys.argv[1:]
if len(args) == 7 and args[1:6] == ['-maxdepth', '1', '-type', 'f', '-printf'] \
        and args[6] == r'%f\n':
    for entry in sorted(os.listdir(args[0])):
        if os.path.isfile(os.path.join(args[0], entry)) and not os.path.islink(
                os.path.join(args[0], entry)):
            print(entry)
    sys.exit(0)
sys.exit(subprocess.call(['/usr/bin/find'] + args))
'''


class PublishReleaseTests(unittest.TestCase):
    def run_case(self, mode, prerelease=False):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            (root / 'bin').mkdir()
            gh = root / 'bin/gh'
            gh.write_text(FAKE_GH)
            gh.chmod(0o755)
            # macOS find lacks GNU -printf, which the script's file-set gate uses.
            gnu_find = root / 'bin/find'
            gnu_find.write_text(FIND_SHIM)
            gnu_find.chmod(0o755)
            (root / 'artifacts').mkdir()
            (root / 'artifacts/ghoten.tar.gz').write_bytes(b'archive')
            digest = hashlib.sha256(b'archive').hexdigest()
            (root / 'artifacts/sha256sums.txt').write_text(f'{digest}  ghoten.tar.gz\n')
            state = {'next_id': 100, 'releases': []}
            if mode != 'new':
                # Pre-existing stable draft with a stale asset from a failed attempt.
                state['releases'].append({
                    'id': 42, 'tag_name': 'next', 'name': 'Next Ghoten release',
                    'body': '', 'draft': True, 'prerelease': False,
                    'target_commitish': 'b' * 40,
                    'assets': [{'id': 7, 'name': 'stale.tar.gz', 'data': 'stale'}],
                })
            (root / 'gh-state.json').write_text(json.dumps(state))
            env = os.environ | {
                'PATH': str(root / 'bin') + ':' + os.environ['PATH'],
                'TAG': 'v1.13.1-rc.2' if prerelease else 'v1.13.1',
                'SOURCE_SHA': SOURCE_SHA,
                'IS_PRERELEASE': str(prerelease).lower(),
                'GITHUB_REPOSITORY': REPO, 'TEST_MODE': mode,
            }
            tag = env['TAG']
            result = subprocess.run(['bash', str(SCRIPT)], cwd=root, env=env,
                                    capture_output=True, text=True)
            calls = [json.loads(line) for line in (root / 'calls.jsonl').read_text().splitlines()]
            state = json.loads((root / 'gh-state.json').read_text())
            published = [call for call in calls
                         if call[:3] == ['api', '--method', 'PATCH'] and 'draft=false' in call]
            if mode in FAIL_MODES:
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertEqual(published, [])
                self.assertTrue(
                    all(release['draft'] for release in state['releases']),
                    'nothing may publish after a failed run')
                return calls, state
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(len(published), 1)
            self.assertEqual(calls[-1], published[0], 'publication must be the last write')
            endpoint = published[0][3]
            release_id = int(endpoint.rsplit('/', 1)[1])
            expected = ['api', 'repos/' + REPO + '/releases/' + str(release_id)]
            self.assertIn(expected, calls, 'draft must be re-read before publication')
            self.assertEqual(calls.index(published[0]) - calls.index(expected), 1,
                             'the identity re-read must immediately precede publication')
            self.assertEqual(published[0][4:12], [
                '-f', 'tag_name=' + tag, '-f', 'target_commitish=' + SOURCE_SHA,
                '-f', 'name=' + tag, '-F', 'draft=false',
            ])
            self.assertIn('prerelease=' + str(prerelease).lower(), published[0])
            self.assertIn('make_latest=' + str(not prerelease).lower(), published[0])
            self.assertTrue(any(call[:2] == ['release', 'download'] for call in calls))
            release = find_release(state, id=release_id)
            self.assertEqual(release['draft'], False)
            self.assertEqual(release['prerelease'], prerelease)
            self.assertEqual(release['tag_name'], tag)
            self.assertEqual(release['name'], tag)
            self.assertEqual(release['target_commitish'], SOURCE_SHA)
            self.assertEqual({a['name'] for a in release['assets']},
                             {'ghoten.tar.gz', 'sha256sums.txt'})
            if not prerelease and mode != 'new':
                self.assertTrue(any(
                    call == ['api', '--method', 'DELETE',
                             'repos/' + REPO + '/releases/assets/7'] for call in calls),
                    'stale assets from earlier attempts must be cleaned up')
            return calls, state

    def test_draft_success(self): self.run_case('success')

    def test_new_release(self): self.run_case('new')

    def test_new_prerelease(self):
        calls, state = self.run_case('new', prerelease=True)
        created = [call for call in calls if call[:2] == ['release', 'create']]
        self.assertEqual(len(created), 1)
        self.assertIn('--prerelease', created[0])

    def test_prerelease_preserves_stable_draft(self):
        calls, state = self.run_case('success', prerelease=True)
        stable = next(release for release in state['releases'] if release['id'] == 42)
        self.assertTrue(stable['draft'])
        self.assertEqual(stable['tag_name'], 'next')

    def test_upload_failure(self): self.run_case('upload-failure')
    def test_corrupt_download(self): self.run_case('corruption')
    def test_unexpected_asset(self): self.run_case('extra')
    def test_identity_drift_abort(self):
        calls, state = self.run_case('drift')
        drifted = next(release for release in state['releases'] if release['tag_name'] != 'next')
        self.assertEqual(drifted['tag_name'], 'v9.9.9-drifted')
        self.assertTrue(drifted['draft'])


if __name__ == '__main__':
    unittest.main()
