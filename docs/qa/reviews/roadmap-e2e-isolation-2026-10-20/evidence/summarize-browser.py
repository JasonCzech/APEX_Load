from pathlib import Path
import json
import xml.etree.ElementTree as ET

root = Path(__file__).parent
attempts = [
    ("initial-shared-import-root", "cycle-reviewed-artifacts", "dev-admin"),
    ("initial-shared-import-root", "cycle-reviewed-artifacts", "local-auth"),
    ("final-cycle", "cycle-final-artifacts", "local-auth"),
    ("final-dev-repetition", "dev-final-artifacts", "dev-admin"),
]
results = []
for attempt, directory, profile in attempts:
    base = root / directory
    selections = list((base / "controller-logs" / profile).glob("run-*/selection.json"))
    expected = {}
    def walk(suites):
        for suite in suites:
            for spec in suite.get("specs", []):
                expected[spec["file"]] = expected.get(spec["file"], 0) + len(spec["tests"])
            walk(suite.get("suites", []))
    if len(selections) == 1:
        walk(json.loads(selections[0].read_text())["suites"])
    actual = {}
    failed = []
    for report in (base / "files").glob(f"*/{profile}/playwright-junit.xml"):
        tree = ET.parse(report).getroot()
        for suite in tree.findall("testsuite"):
            name = suite.get("name")
            counts = actual.setdefault(name, {k: 0 for k in ("tests", "failures", "errors", "skipped")})
            for k in counts:
                counts[k] += int(suite.get(k, 0))
            failed += [{"file": name, "test": case.get("name")} for case in suite.findall("testcase")
                       if case.find("failure") is not None or case.find("error") is not None]
    expected = {k.removeprefix("e2e/"): v for k, v in expected.items()}
    actual = {k.removeprefix("e2e/"): v for k, v in actual.items()}
    mismatches = [{"file": f, "selected": n, "reported": actual.get(f, {}).get("tests", 0)}
                  for f, n in expected.items() if actual.get(f, {}).get("tests", 0) != n]
    totals = {k: sum(v[k] for v in actual.values()) for k in ("tests", "failures", "errors", "skipped")}
    results.append({
        "attempt": attempt, "profile": profile, "selected_files": len(expected),
        "selected_tests": sum(expected.values()), "reported_files": len(actual),
        **totals, "passed": totals["tests"] - totals["failures"] - totals["errors"] - totals["skipped"],
        "missing_or_incomplete": mismatches, "unexpected_files": sorted(set(actual) - set(expected)),
        "failed_tests": failed,
        "complete_and_green": bool(expected) and not mismatches and set(actual) == set(expected) and not failed
    })
print(json.dumps(results, indent=2))
