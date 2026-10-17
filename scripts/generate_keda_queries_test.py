import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("keda_queries", Path(__file__).with_name("generate-keda-queries.py"))
keda = importlib.util.module_from_spec(spec)
spec.loader.exec_module(keda)


class KEDAQueryGenerationTest(unittest.TestCase):
    def test_every_shipped_target_is_fresh(self):
        self.assertEqual(keda.generate(keda.ROOT, check=True), [])

    def test_only_expressions_change_and_second_render_is_identical(self):
        for path, fleet in keda.TARGETS.items():
            with self.subTest(path=path):
                source = (keda.ROOT / path).read_text()
                stale = "\n".join("        query: stale" if line.lstrip().startswith("query:") else line
                                  for line in source.split("\n"))
                rendered = keda.render(stale, fleet)
                unchanged = lambda text: [line for line in text.splitlines() if not line.lstrip().startswith("query:")]
                self.assertEqual(unchanged(stale), unchanged(rendered))
                self.assertEqual(rendered, source)
                self.assertEqual(keda.render(rendered, fleet), rendered)

    def test_missing_unknown_and_duplicate_metrics_are_rejected(self):
        source = (keda.ROOT / next(iter(keda.TARGETS))).read_text()
        for modified in (source.replace("        query:", "        omitted:", 1),
                         source.replace("apexload_queued_vusers", "unknown", 1),
                         source.replace("apexload_active_vusers", "apexload_queued_vusers", 1)):
            with self.assertRaises(ValueError):
                keda.render(modified, "fleet")

    def test_generation_validates_all_inputs_before_writing(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for path in keda.TARGETS:
                target = root / path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text((keda.ROOT / path).read_text().replace("query: max(", "query: changed("))
            first = root / next(iter(keda.TARGETS))
            before = first.read_text()
            last = root / list(keda.TARGETS)[-1]
            last.write_text(last.read_text().replace("metricName: apexload_queued_vusers", "metricName: unknown"))
            with self.assertRaises(ValueError):
                keda.generate(root, check=False)
            self.assertEqual(first.read_text(), before)


if __name__ == "__main__":
    unittest.main()
