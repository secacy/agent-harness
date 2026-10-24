"""保存的受控批次及标明的合成变体校准；不产生真实模型效果样本。"""
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
spec = importlib.util.spec_from_file_location("handoff_analyzer", BUNDLE / "analyze.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
DEFAULT = ROOT / "runs/final-response-handoff-01/delivery/final-response-3394863990/campaign"
REPAIR = ROOT / "runs/generation-diagnostics-01/repair-accepted/diagnostic-repair-3874741159/campaign"


class HandoffAnalysis(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.campaign = Path(os.environ.get("HARNESS_HANDOFF_CALIBRATION", str(DEFAULT)))
        if not cls.campaign.is_dir() or not REPAIR.is_dir():
            raise RuntimeError("saved handoff and diagnostic repair calibration campaigns are required")
        summary = json.loads((cls.campaign / "summary.json").read_text())
        cls.originals = {t["id"]: cls.campaign / t["attempt"]["directory"] for t in summary["targets"]}
        if set(cls.originals) != {"correct", "alternate-missing-timeout", "mapping-helper", "illegal"}:
            raise RuntimeError("unexpected calibration target set")

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.run = self.root / "run"
        self.plan = self.root / "plan.json"
        self.ledger = self.root / "budget.json"
        self.tools = self.root / "tools.json"
        tools = json.loads((BUNDLE / "tools.json").read_text())
        self.tools.write_bytes(module.compact([t for t in tools if t["Name"] != "migration_check"]))
        shutil.copyfile(self.campaign / "plan.json", self.plan)
        shutil.copyfile(self.campaign / "budget/budget.json", self.ledger)
        self.original = self.originals["correct"]
        initial = module.analyze(self.original, self.ledger, self.tools, self.plan)
        for name in initial["sources"]:
            target = self.run / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(self.original / name, target)

    def read(self, path):
        return json.loads(path.read_text())

    def put(self, path, value):
        path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")

    def trace(self):
        return self.read(self.run / "artifacts/agent.json")

    def save_trace(self, value):
        self.put(self.run / "artifacts/agent.json", value)

    def analyze(self):
        return module.analyze(self.run, self.ledger, self.tools, self.plan)

    def context(self):
        record = self.read(self.run / "run.json")
        plan = self.read(self.plan)
        target = next(t for t in plan["targets"] if t["input"]["id"] == record["target_id"])
        ledger = self.read(self.ledger)
        return record, plan, target, ledger

    def rebind_synthetic(self, trace):
        """仅为明确标记的合成变体重建策略消息和摘要，不修改实际批次。"""
        record, plan, target, ledger = self.context()
        own = [c for c in ledger["calls"] if c["run_id"] == record["id"]]
        index, executed, ordinal = 2, 0, 0
        trace.pop("FinalResponseAttempt", None)
        while index < len(trace["Messages"]):
            ordinal += 1
            remaining = target["agent_limits"]["MaxModelCalls"] - ordinal + 1
            available = target["agent_limits"]["MaxToolCalls"] - executed
            trace["Messages"][index] = module.budget_notice(remaining, available)
            index += 1
            final = remaining == 1 or available == 0
            if final:
                trace["FinalResponseAttempt"] = {"ModelCall": ordinal, "MessageCount": index}
            own[ordinal - 1]["input_sha256"] = module.digest(module.compact({"Messages": trace["Messages"][:index], "Tools": None if final else self.read(self.tools)}))
            if index < len(trace["Messages"]) and trace["Messages"][index]["Role"] == "assistant":
                index += 1
            while index < len(trace["Messages"]) and trace["Messages"][index]["Role"] == "tool":
                index += 1
                executed += 1
        self.save_trace(trace)
        self.put(self.ledger, ledger)

    def set_limits(self, models=None, tools=None):
        record, plan, target, ledger = self.context()
        report = self.read(self.run / "artifacts/report.json")
        if models is not None:
            target["agent_limits"]["MaxModelCalls"] = models
            report["input"]["max_model_calls"] = models
        if tools is not None:
            target["agent_limits"]["MaxToolCalls"] = tools
            report["input"]["max_tool_calls"] = tools
        self.put(self.plan, plan)
        self.put(self.run / "artifacts/report.json", report)
        self.put(self.run / "inputs/task.json", report["input"])

    def fail_without_last_reply(self, state):
        trace = self.trace()
        trace["Messages"].pop()
        trace["Reason"] = "limit_reached" if state == "rejected" else "failed"
        self.save_trace(trace)
        for path, key in [(self.run / "run.json", "status"), (self.run / "artifacts/report.json", "status")]:
            data = self.read(path)
            data[key] = "Failed"
            if path.name == "run.json":
                data["agent_exit_reason"] = trace["Reason"]
            self.put(path, data)
        record, _, _, ledger = self.context()
        own = [c for c in ledger["calls"] if c["run_id"] == record["id"]]
        if state in ("rejected", "missing"):
            last = own[-1]
            ledger["calls"] = [c for c in ledger["calls"] if c["id"] != last["id"]]
            if state == "rejected":
                ledger["rejections"] = [{"run_id": record["id"], "target_id": record["target_id"], "input_sha256": last["input_sha256"]}]
        else:
            own[-1].update(state="unknown", outcome="model_error", usage=None)
        self.put(self.ledger, ledger)

    def synthetic_repair(self):
        summary = self.read(REPAIR / "summary.json")
        original = REPAIR / summary["targets"][0]["attempt"]["directory"]
        shutil.rmtree(self.run)
        self.run.mkdir()
        for name in ["run.json", "artifacts/agent.json", "artifacts/report.json", "inputs/task.json"]:
            target = self.run / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(original / name, target)
        for name in ["baseline/translation", "workspace/translation", "generation-diagnostics"]:
            shutil.copytree(original / name, self.run / name)
        shutil.copyfile(REPAIR / "plan.json", self.plan)
        shutil.copyfile(REPAIR / "budget/budget.json", self.ledger)
        shutil.copyfile(BUNDLE / "tools.json", self.tools)
        report = self.read(self.run / "artifacts/report.json")
        report["input"]["completion_policy"] = "final-response-v1"
        self.put(self.run / "artifacts/report.json", report)
        self.put(self.run / "inputs/task.json", report["input"])
        plan = self.read(self.plan)
        plan["targets"][0]["completion_policy"] = "final-response-v1"
        self.put(self.plan, plan)
        self.rebind_synthetic(self.trace())

    def test_synthetic_diagnostic_repair_and_check_projection(self):
        self.synthetic_repair()
        trace = self.trace()
        final_notice = trace["FinalResponseAttempt"]["MessageCount"] - 1
        # 合成一条检查结果以校准提取口径，不将它当成实际执行。
        assistant_index = max(i for i, m in enumerate(trace["Messages"][:final_notice]) if m["Role"] == "assistant")
        trace["Messages"][assistant_index]["ToolCalls"].append({"ID": "synthetic-check", "Name": "go_check", "Arguments": '{"kind":"test"}'})
        trace["Messages"].insert(final_notice, {"Role": "tool", "Content": "go test passed\nconfiguration default\nconfiguration rpcx_alternate\n", "ToolCalls": None, "ToolCallID": "synthetic-check", "IsError": False})
        trace["ToolCalls"] += 1
        self.rebind_synthetic(trace)
        result = self.analyze()
        self.assertEqual([d["status"] for d in result["diagnostics"]], ["violated", "preserved"])
        self.assertEqual(result["diagnostic_errors"], 1)
        self.assertTrue(result["diagnostics"][0]["later_edits"][0]["feedback_in_prior_request"])
        self.assertEqual(result["handoff"]["outcome"], "final-response-submitted")
        self.assertEqual(result["checks_on_final_candidate"][0]["configuration_headers"], ["default", "rpcx_alternate"])
        self.assertTrue(result["checks_on_final_candidate"][0]["recorded_pass"])
        self.assertEqual(result["last_successful_edit"]["candidate_sha256"], result["final_candidate_sha256"])

    def test_synthetic_diagnostic_snapshot_tamper(self):
        self.synthetic_repair()
        report = self.read(self.run / "artifacts/report.json")
        ref = report["generation_diagnostics"][0]
        (self.run / Path(ref["path"]).parent / "candidate/extra.txt").write_text("changed")
        with self.assertRaisesRegex(ValueError, "snapshot"):
            self.analyze()

    def test_saved_four_outcomes(self):
        expected = {"correct": "ReadyForReview", "alternate-missing-timeout": "Rejected", "mapping-helper": "NeedsAttention", "illegal": None}
        for name, run in self.originals.items():
            with self.subTest(name=name):
                result = module.analyze(run, self.ledger, self.tools, self.plan)
                self.assertEqual(result["recorded_verdict"], expected[name])
                self.assertEqual(len(result["request_bindings"]), 2)
                self.assertEqual(result["request_bindings"][-1]["mode"], "final-response-only")
                self.assertTrue(result["handoff"]["boundary_reached"])
                self.assertEqual(result["handoff"]["outcome"], "boundary-failed" if name == "illegal" else "final-response-submitted")
                if name == "illegal":
                    self.assertEqual(result["pending_tool_calls"], ["late"])
                    self.assertFalse(result["handoff"]["validation_published"])

    def test_early_submission(self):
        self.set_limits(models=3)
        self.rebind_synthetic(self.trace())
        result = self.analyze()
        self.assertEqual(result["handoff"]["outcome"], "early-submitted")
        self.assertFalse(result["handoff"]["boundary_reached"])

    def test_tool_exhaustion(self):
        trace = self.trace()
        self.set_limits(models=4, tools=trace["ToolCalls"])
        self.rebind_synthetic(trace)
        result = self.analyze()
        self.assertTrue(result["handoff"]["boundary_reached"])
        self.assertEqual(result["handoff"]["attempt"]["ModelCall"], 2)

    def test_denied_missing_and_unknown_reply(self):
        for state in ["rejected", "missing", "unknown"]:
            with self.subTest(state=state):
                backup = {p: p.read_bytes() for p in [self.ledger, self.run / "run.json", self.run / "artifacts/report.json", self.run / "artifacts/agent.json"]}
                self.fail_without_last_reply(state)
                result = self.analyze()
                self.assertFalse(result["request_bindings"][-1]["has_reply"])
                expected = {"rejected": "rejected", "missing": "no-admission-record", "unknown": "admitted"}[state]
                self.assertEqual(result["request_bindings"][-1]["budget_status"], expected)
                for path, raw in backup.items():
                    path.write_bytes(raw)

    def test_marker_missing_or_changed(self):
        original = self.trace()
        for key in ["ModelCall", "MessageCount", None]:
            trace = copy.deepcopy(original)
            if key:
                trace["FinalResponseAttempt"][key] += 1
            else:
                trace.pop("FinalResponseAttempt")
            self.save_trace(trace)
            with self.assertRaisesRegex(ValueError, "marker"):
                self.analyze()

    def test_null_is_not_empty_tools(self):
        record, _, _, ledger = self.context()
        trace = self.trace()
        last = next(c for c in reversed(ledger["calls"]) if c["run_id"] == record["id"])
        last["input_sha256"] = module.digest(module.compact({"Messages": trace["Messages"][:trace["FinalResponseAttempt"]["MessageCount"]], "Tools": []}))
        self.put(self.ledger, ledger)
        with self.assertRaisesRegex(ValueError, "input digest"):
            self.analyze()

    def test_tool_execution_after_boundary(self):
        trace = self.trace()
        trace["Messages"].append({"Role": "tool", "Content": "fake", "ToolCalls": None, "ToolCallID": "late", "IsError": False})
        trace["ToolCalls"] += 1
        self.save_trace(trace)
        with self.assertRaisesRegex(ValueError, "unexpected tool execution"):
            self.analyze()

    def test_plan_and_run_policy_mismatch(self):
        record, plan, target, ledger = self.context()
        target["completion_policy"] = ""
        self.put(self.plan, plan)
        with self.assertRaisesRegex(ValueError, "strategy"):
            self.analyze()

    def test_rejected_request_hash(self):
        self.fail_without_last_reply("rejected")
        ledger = self.read(self.ledger)
        ledger["rejections"][0]["input_sha256"] = "0" * 64
        self.put(self.ledger, ledger)
        with self.assertRaisesRegex(ValueError, "rejected request"):
            self.analyze()

    def test_baseline_failure(self):
        report = self.read(self.run / "artifacts/report.json")
        report.update(agent_record="", status="Failed", generation_diagnostics=[], validation=None)
        self.put(self.run / "artifacts/report.json", report)
        record, _, _, ledger = self.context()
        record["status"] = "Failed"
        self.put(self.run / "run.json", record)
        ledger["calls"] = [c for c in ledger["calls"] if c["run_id"] != record["id"]]
        self.put(self.ledger, ledger)
        result = self.analyze()
        self.assertFalse(result["trace_available"])
        self.assertIsNone(result["tool_provided"])
        self.assertEqual(result["handoff"]["outcome"], "before-agent")

    def test_candidate_and_source_drift(self):
        (self.run / "workspace/translation/extra.txt").write_text("changed")
        with self.assertRaisesRegex(ValueError, "final candidate"):
            self.analyze()

    def test_source_stability(self):
        first = self.analyze()
        with mock.patch.object(module.Source, "verify", autospec=True, side_effect=ValueError("source changed during analysis")):
            with self.assertRaisesRegex(ValueError, "source changed"):
                self.analyze()
        self.assertEqual(first["sources"], self.analyze()["sources"])


if __name__ == "__main__":
    unittest.main()
