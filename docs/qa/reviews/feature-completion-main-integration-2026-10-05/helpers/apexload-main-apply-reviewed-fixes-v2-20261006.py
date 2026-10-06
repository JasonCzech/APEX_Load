import datetime, hashlib, json, pathlib, subprocess

root = pathlib.Path('/Users/jasonczech/.codex/worktrees/feature-completion-resume/Project_Stormrunner')
qa = root / 'docs/qa/reviews/feature-completion-main-integration-2026-10-05/evidence'
receipt = qa / 'reviewed-integration-fixes-applied.json'
assert not receipt.exists()
def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()
def git(*args):
    return subprocess.check_output(['git', '--no-optional-locks', *args], cwd=root, text=True).strip()
assert git('rev-parse', 'HEAD') == '6bbdc9971d23bcd03e27ff6a54f30b5e0f191a43'
assert git('rev-parse', 'MERGE_HEAD') == 'da1d6dd00ffdd250c630de7a73328a08040571a1'
assert not git('diff', '--name-only', '--diff-filter=U')
for label in ['main-full-go-1', 'main-dashboard-full-cycle-1']:
    gate = json.loads((qa / (label + '.json')).read_text())
    assert gate['exit_code'] == 1 and not gate['source_changed_paths']
    assert digest(qa / (label + '.log')) == gate['log_sha256']

base = pathlib.Path('/private/tmp/apexload-network-acquisition-barrier-patch-0gen2wr4')
manifest_path = base / 'browser-network-acquisition-barrier-v3.manifest.json'
assert digest(manifest_path) == '84a78bd1048a90b06a01b61f0b0e6f63f1199b7068df99111d62574e82a5ec04'
manifest = json.loads(manifest_path.read_text())
expected = sorted(['action_helpers.go', 'browser.go', 'lifecycle.go', 'network_task_completion.go', 'network_task_completion_test.go', 'result_finalization.go', 'result_finalization_test.go', 'runtime_context.go'])
assert sorted(pathlib.PurePosixPath(x['path']).name for x in manifest['changes']) == expected
for item in manifest['changes']:
    assert item['path'] == 'pkg/protocol/browser/' + pathlib.PurePosixPath(item['path']).name
    path = root / item['path']
    assert not path.is_symlink()
    assert (digest(path) if path.exists() else None) == item['before_sha256']
for item in manifest['root_input_bookends']:
    assert digest(root / item['path']) == item['sha256']
for item in manifest['read_only_dependencies']:
    assert digest(root / item['path']) == item['sha256']
ui_diagnostic_path = pathlib.Path('/private/tmp/apexload-main-command-center-resize-readonly-diagnostic-20261006.json')
assert digest(ui_diagnostic_path) == 'ecae478d35598bc7f4af59da9eea1988ca476ef543e71abf41d0ea1dfd5d2cce'
ui_diagnostic = json.loads(ui_diagnostic_path.read_text())
for item in ui_diagnostic['source_hashes']:
    assert digest(root / item['path']) == item['sha256']
auth_path = pathlib.Path('/private/tmp/apexload-main-command-center-local-auth-coverage-private-review-20261006.json')
assert digest(auth_path) == '210821dbe1736a9c2a83dc2dc8b0f162f99a9260d46700ec85f6cf3f2e72d456'
auth = json.loads(auth_path.read_text())
for item in auth['readonly_root_inputs']:
    assert digest(root / item['path']) == item['sha256']
supplement = root / 'docs/qa/reviews/feature-completion-telemetry-policy-2026-10-05/evidence/telemetry-policy-final-scope-independent-review-supplement.json'
assert digest(supplement) == '6a4d38f01fbe6757932962e0757afeea82a58f38f44379ee84624532d0c5509f'

patches = [
    (base / 'browser-network-acquisition-barrier-v3.patch', '91f4299ca482618eb1637ee58a47119dc61a6d49d0e59499960e191597c8758d'),
    (pathlib.Path('/private/tmp/apexload-main-command-center-resize-offsets-test-only-20261006.patch'), '8346a55027174a7e891506ab7266be3d19a400e3664d22e31a2f5cda30eab0a8'),
    (pathlib.Path('/private/tmp/apexload-main-command-center-local-auth-coverage-additional-20261006.patch'), '74f2026197f49db1289e3daac322ae6b463eac6d58e473d45d6f96448dfc8277'),
]
# Validate every immutable patch before the first working-tree mutation.
for patch, sha in patches:
    assert digest(patch) == sha
for index, (patch, sha) in enumerate(patches):
    assert digest(patch) == sha
    subprocess.run(['git', 'apply', '--check', str(patch)], cwd=root, check=True)
    subprocess.run(['git', 'apply', str(patch)], cwd=root, check=True)
    if index == 1:
        expected_resize = ui_diagnostic['temporary_patch']
        assert digest(root / expected_resize['target']) == expected_resize['expected_after_sha256']
for item in manifest['changes']:
    assert digest(root / item['path']) == item['after_sha256']
for item in auth['changes']:
    assert digest(root / item['path']) == item['expected_after_sha256']
assert digest(supplement) == '6a4d38f01fbe6757932962e0757afeea82a58f38f44379ee84624532d0c5509f'
result = {
    'schema': 'apexload.guarded-integration-fix-application.v1',
    'at_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'head': git('rev-parse', 'HEAD'), 'integrating_main': git('rev-parse', 'MERGE_HEAD'),
    'original_gate_processes_joined': True,
    'application_only': True, 'build_test_qualification_pending': True,
    'patches': [{'path': str(p), 'sha256': s} for p, s in patches],
    'changed_paths': [x['path'] for x in manifest['changes']] + [x['path'] for x in auth['changes']],
    'exact_before_after_guards_passed': True,
    'unrelated_supplement_preserved': True,
}
with receipt.open('x') as output:
    json.dump(result, output, indent=2)
    output.write('\n')
print(json.dumps(result))
