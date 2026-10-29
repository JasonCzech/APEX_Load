import importlib.util
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("check-docs.py")
SPEC = importlib.util.spec_from_file_location("check_docs", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
check_docs = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(check_docs)


class CheckDocsTest(unittest.TestCase):
    def test_heading_anchors_and_fenced_code_exclusion(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs/archive").mkdir(parents=True)
            (root / "docs/GUIDE.md").write_text(
                "# Guide\n\n"
                "## Web/API Execution Support Matrix\n\n"
                "See [matrix](#webapi-execution-support-matrix) and "
                "[help alias](#web-api-execution-support-matrix) and "
                "[bad](#nonexistent-anchor).\n\n"
                "```markdown\n"
                "[ignored code block link](MISSING.md#missing)\n"
                "```\n"
            )
            (root / "docs/CALLER.md").write_text(
                "# Caller\n\n"
                "Valid [cross](GUIDE.md#webapi-execution-support-matrix).\n"
                "Broken [cross](GUIDE.md#wrong-section).\n"
            )
            (root / "docs/archive/OLD.md").write_text(
                "# Old\n\n[historical anchor](#retired-heading)\n"
            )
            problems = check_docs.check_local_links(
                [
                    Path("docs/GUIDE.md"),
                    Path("docs/CALLER.md"),
                    Path("docs/archive/OLD.md"),
                ],
                root=root,
            )
            self.assertEqual(
                problems,
                [
                    "docs/GUIDE.md: missing heading anchor -> #nonexistent-anchor",
                    "docs/CALLER.md: missing heading anchor #wrong-section in docs/GUIDE.md -> GUIDE.md#wrong-section",
                ],
            )

    def test_index_membership_covers_docs_guides_and_deployments(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs/guides").mkdir(parents=True)
            (root / "deployments/k8s/aks").mkdir(parents=True)
            (root / "docs/INDEX.md").write_text(
                "# Index\n\n"
                "| Doc | Status | Purpose |\n"
                "| --- | --- | --- |\n"
                "| [A.md](A.md) | Current | Doc A |\n"
                "| [guides/G1.md](guides/G1.md) | Current | Guide 1 |\n"
            )
            (root / "docs/A.md").write_text("# A\n")
            (root / "docs/MISSING_DOC.md").write_text("# Missing\n")
            (root / "docs/guides/G1.md").write_text("# G1\n")
            (root / "docs/guides/MISSING_GUIDE.md").write_text("# G2\n")
            (root / "deployments/k8s/aks/README.md").write_text("# AKS\n")
            files = [
                Path("docs/INDEX.md"),
                Path("docs/A.md"),
                Path("docs/MISSING_DOC.md"),
                Path("docs/guides/G1.md"),
                Path("docs/guides/MISSING_GUIDE.md"),
                Path("deployments/k8s/aks/README.md"),
            ]
            problems = check_docs.check_index_membership(files, root=root)
            self.assertEqual(
                problems,
                [
                    "deployments/k8s/aks/README.md: not listed in a docs/INDEX.md table, which claims to be the canonical documentation map",
                    "docs/MISSING_DOC.md: not listed in a docs/INDEX.md table, which claims to be the canonical documentation map",
                    "docs/guides/MISSING_GUIDE.md: not listed in a docs/INDEX.md table, which claims to be the canonical documentation map",
                ],
            )

    def test_package_inventory_checks_cmd_and_pkg(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "cmd/apexctl").mkdir(parents=True)
            (root / "cmd/missingbin").mkdir(parents=True)
            (root / "pkg/engine").mkdir(parents=True)
            (root / "pkg/missingpkg").mkdir(parents=True)
            (root / "AGENTS.md").write_text(
                "# AGENTS\n\n## Project Structure\n```\ncmd/\n  apexctl/ — CLI\npkg/\n  engine/ — Engine\n```\n"
            )
            problems = check_docs.check_package_inventory(root=root)
            self.assertEqual(
                problems,
                [
                    "AGENTS.md: cmd/missingbin/ is missing from the Project Structure tree",
                    "AGENTS.md: pkg/missingpkg/ is missing from the Project Structure tree",
                ],
            )

    def test_architecture_diagram_refs_and_archive_route_exemption(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "docs/archive").mkdir(parents=True)
            (root / "docs/PRESENT.md").write_text("# Present\n")
            (root / "docs/architecture.html").write_text(
                "<div>See docs/PRESENT.md and docs/GONE.md</div>\n"
            )
            self.assertEqual(
                check_docs.check_architecture_diagram_refs(root=root),
                ["docs/architecture.html: referenced doc does not exist -> docs/GONE.md"],
            )

            (root / "docs/archive/OLD_SPEC.md").write_text("GET /api/v1/removed-route\n")
            (root / "docs/ACTIVE.md").write_text("GET /api/v1/tests\n")
            checked = check_docs.route_checked_markdown_files(
                [Path("docs/archive/OLD_SPEC.md"), Path("docs/ACTIVE.md")],
                root=root,
            )
            self.assertEqual(checked, [Path("docs/ACTIVE.md")])

    def test_prohibited_repo_artifacts(self) -> None:
        files = [
            Path("docs/qa/reviews/README.md"),
            Path("docs/qa/reviews/import-fixture-matrix-2026-09-12/brokers.compose.yaml"),
            Path("docs/qa/reviews/run-1/error-context.md"),
            Path("docs/qa/reviews/run-1/evidence/capture.md"),
            Path("docs/qa/frontend/shot.png"),
            Path("docs/archive/legacy.docx"),
        ]
        problems = check_docs.check_prohibited_repo_artifacts(files)
        self.assertEqual(len(problems), 4)
        self.assertTrue(any("error-context.md" in p for p in problems))
        self.assertTrue(any("evidence/" in p for p in problems))
        self.assertTrue(any("shot.png" in p for p in problems))
        self.assertTrue(any("legacy.docx" in p for p in problems))


if __name__ == "__main__":
    unittest.main()
