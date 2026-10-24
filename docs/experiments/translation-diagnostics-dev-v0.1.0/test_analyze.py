"""用保存的受控轨迹及明确标记的合成变体校准分析，不产生迁移效果样本。"""
import copy
import importlib.util
import json
import os
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest import mock

BUNDLE = Path(__file__).resolve().parent
ROOT = BUNDLE.parents[2]
spec = importlib.util.spec_from_file_location("trajectory_analyzer", BUNDLE / "analyze.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
DEFAULT = ROOT / "runs/generation-diagnostics-01/repair-accepted/diagnostic-repair-3874741159/campaign"


class TrajectoryCalibration(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.campaign = Path(os.environ.get("HARNESS_TRACE_CAMPAIGN", str(DEFAULT)))
        if not cls.campaign.exists():
            raise unittest.SkipTest("saved controlled campaign is required")
        summary = json.loads((cls.campaign / "summary.json").read_text())
        cls.original = cls.campaign / summary["targets"][0]["attempt"]["directory"]
        cls.tools = BUNDLE / "tools.json"
        cls.ledger = cls.campaign / "budget/budget.json"
        cls.calibration = module.analyze(cls.original, cls.ledger, cls.tools)

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.run = self.root / "run"
        for name in self.calibration["sources"]:
            target = self.run / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(self.original / name, target)
        self.budget = self.root / "budget.json"
        shutil.copyfile(self.ledger, self.budget)

    def read(self, path):
        return json.loads((self.run / path).read_text())

    def put(self, path, value):
        (self.run / path).write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")

    def analyze(self):
        return module.analyze(self.run, self.budget, self.tools)

    def stop_after_first_diagnostic(self):
        trace = self.read("artifacts/agent.json")
        end = next(i for i, m in enumerate(trace["Messages"]) if m["Role"] == "tool" and m["ToolCallID"] == "before")
        trace["Messages"] = trace["Messages"][:end + 1]
        trace.update(Reason="limit_reached", ModelCalls=1, ToolCalls=sum(m["Role"] == "tool" for m in trace["Messages"]), Usage=trace["Usage"][:1])
        self.put("artifacts/agent.json", trace)
        report = self.read("artifacts/report.json")
        report["generation_diagnostics"] = report["generation_diagnostics"][:1]
        first = self.read(report["generation_diagnostics"][0]["path"])
        source = self.run / Path(report["generation_diagnostics"][0]["path"]).parent / "candidate"
        destination = self.run / "workspace/translation"
        shutil.rmtree(destination)
        shutil.copytree(source, destination)
        report.update(status="Failed", candidate_sha256=first["binding"]["candidate_sha256"], validation=None)
        self.put("artifacts/report.json", report)
        record = self.read("run.json")
        record.update(status="Failed", candidate_sha256=report["candidate_sha256"])
        self.put("run.json", record)
        budget = json.loads(self.budget.read_text())
        budget["calls"] = budget["calls"][:1]
        self.budget.write_text(json.dumps(budget))

    def test_controlled_repair(self):
        result = self.analyze()
        self.assertEqual(len(result["request_bindings"]), 4)
        self.assertEqual(result["diagnostic_attempts_with_result"], 3)
        self.assertEqual(result["diagnostic_errors"], 1)
        self.assertEqual([d["status"] for d in result["diagnostics"]], ["violated", "preserved"])
        self.assertTrue(result["diagnostics"][0]["later_edits"][0]["feedback_in_prior_request"])

    def test_no_followup_request(self):
        self.stop_after_first_diagnostic()
        result = self.analyze()
        self.assertEqual(len(result["diagnostics"]), 1)
        self.assertIsNone(result["diagnostics"][0]["following_reply_call_id"])
        self.assertEqual(result["diagnostics"][0]["later_edits"], [])

    def test_same_batch_edit_was_preplanned(self):
        self.stop_after_first_diagnostic()
        trace = self.read("artifacts/agent.json")
        assistant = next(m for m in trace["Messages"] if m["Role"] == "assistant")
        calls = assistant["ToolCalls"]
        weak, check = calls[-2:]
        calls[-2:] = [check, weak]
        results = [m for m in trace["Messages"] if m["Role"] == "tool"]
        weak_result, check_result = results[-2:]
        index = next(i for i, m in enumerate(trace["Messages"]) if m["Role"] == "tool" and m["ToolCallID"] == weak["ID"])
        trace["Messages"][index:] = [check_result, weak_result]
        report = self.read("artifacts/report.json")
        ref = report["generation_diagnostics"][0]
        entry = self.read(ref["path"])
        good = self.original / "workspace/translation"
        snapshot = self.run / Path(ref["path"]).parent / "candidate"
        shutil.rmtree(snapshot)
        shutil.copytree(good, snapshot)
        good_hash = module.tree_hash(module.Source(self.run).tree(str(Path(ref["path"]).parent / "candidate")))
        entry["binding"]["candidate_sha256"] = good_hash
        feedback = json.loads(check_result["Content"])
        feedback["binding"]["candidate_sha256"] = good_hash
        feedback.update(test_structure="preserved", findings=[], total_findings=0, omitted_findings=0)
        check_result["Content"] = module.compact(feedback).decode()
        entry["response"]["Content"] = check_result["Content"]
        self.put(ref["path"], entry)
        ref["sha256"] = module.digest((self.run / ref["path"]).read_bytes())
        self.put("artifacts/report.json", report)
        self.put("artifacts/agent.json", trace)
        result = self.analyze()
        edit = result["diagnostics"][0]["later_edits"][0]
        self.assertEqual(edit["tool_call_id"], weak["ID"])
        self.assertFalse(edit["feedback_in_prior_request"])

    def test_proposed_but_unanswered_diagnostic(self):
        self.stop_after_first_diagnostic()
        trace = self.read("artifacts/agent.json")
        trace["Messages"].pop()
        trace["ToolCalls"] -= 1
        self.put("artifacts/agent.json", trace)
        report = self.read("artifacts/report.json")
        report["generation_diagnostics"] = []
        self.put("artifacts/report.json", report)
        result = self.analyze()
        self.assertEqual(result["diagnostic_proposals"], 1)
        self.assertEqual(result["diagnostic_attempts_with_result"], 0)
        self.assertEqual(result["pending_tool_calls"], ["before"])

    def test_no_diagnostic_used(self):
        self.stop_after_first_diagnostic()
        trace = self.read("artifacts/agent.json")
        first = next(m for m in trace["Messages"] if m["Role"] == "assistant")
        first["ToolCalls"].pop()
        trace["Messages"].pop()
        trace["ToolCalls"] -= 1
        self.put("artifacts/agent.json", trace)
        report = self.read("artifacts/report.json")
        report["generation_diagnostics"] = []
        self.put("artifacts/report.json", report)
        result = self.analyze()
        self.assertTrue(result["tool_provided"])
        self.assertEqual(result["diagnostic_proposals"], 0)
        self.assertEqual(result["diagnostics"], [])

    def test_failed_before_agent(self):
        report = self.read("artifacts/report.json")
        report.update(status="Failed", agent_record="", generation_diagnostics=[])
        self.put("artifacts/report.json", report)
        record = self.read("run.json")
        record["status"] = "Failed"
        self.put("run.json", record)
        budget = json.loads(self.budget.read_text())
        budget["calls"] = []
        self.budget.write_text(json.dumps(budget))
        result = self.analyze()
        self.assertFalse(result["trace_available"])
        self.assertTrue(result["tool_configured"])
        self.assertIsNone(result["tool_provided"])

    def test_model_input_tamper(self):
        budget = json.loads(self.budget.read_text())
        budget["calls"][0]["input_sha256"] = "0" * 64
        self.budget.write_text(json.dumps(budget))
        with self.assertRaisesRegex(ValueError, "input digest"):
            self.analyze()

    def test_snapshot_tamper(self):
        report = self.read("artifacts/report.json")
        root = self.run / Path(report["generation_diagnostics"][0]["path"]).parent / "candidate"
        (root / "extra.txt").write_text("changed")
        with self.assertRaisesRegex(ValueError, "snapshot"):
            self.analyze()

    def test_ambiguous_edit(self):
        trace = self.read("artifacts/agent.json")
        result = next(m for m in trace["Messages"] if m["Role"] == "assistant")
        args = json.loads(result["ToolCalls"][0]["Arguments"])
        args["old"] = "not present in baseline"
        result["ToolCalls"][0]["Arguments"] = json.dumps(args)
        self.put("artifacts/agent.json", trace)
        with self.assertRaisesRegex(ValueError, "uniquely replayed"):
            self.analyze()

    def test_unsafe_edit_path(self):
        trace = self.read("artifacts/agent.json")
        first = next(m for m in trace["Messages"] if m["Role"] == "assistant")
        args = json.loads(first["ToolCalls"][0]["Arguments"])
        args["path"] = "../outside"
        first["ToolCalls"][0]["Arguments"] = json.dumps(args)
        self.put("artifacts/agent.json", trace)
        with self.assertRaisesRegex(ValueError, "unsafe relative"):
            self.analyze()

    def test_candidate_membership_tamper(self):
        (self.run / "workspace/translation/extra.txt").write_text("changed")
        with self.assertRaisesRegex(ValueError, "final candidate"):
            self.analyze()

    def test_cross_run_report(self):
        report = self.read("artifacts/report.json")
        ref = report["generation_diagnostics"][0]
        entry = self.read(ref["path"])
        entry["run_id"] = "another-run"
        self.put(ref["path"], entry)
        ref["sha256"] = module.digest((self.run / ref["path"]).read_bytes())
        self.put("artifacts/report.json", report)
        with self.assertRaisesRegex(ValueError, "cross-run"):
            self.analyze()

    def test_source_membership_drift(self):
        original_verify = module.Source.verify
        def changed(source):
            if source.root == self.run.resolve():
                (self.run / "workspace/translation/late.txt").write_text("changed")
            return original_verify(source)
        with mock.patch.object(module.Source, "verify", changed):
            with self.assertRaisesRegex(ValueError, "membership changed"):
                self.analyze()

    def test_source_stability(self):
        original = {name: module.digest((self.run / name).read_bytes()) for name in self.calibration["sources"]}
        self.analyze()
        self.assertEqual(original, {name: module.digest((self.run / name).read_bytes()) for name in original})


if __name__ == "__main__":
    unittest.main()
