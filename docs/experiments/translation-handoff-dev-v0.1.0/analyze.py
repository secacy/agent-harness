#!/usr/bin/env python3
"""只读重放一次 Run 的文件编辑与诊断轨迹；不执行候选或判定迁移质量。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import stat


def digest(data):
    return hashlib.sha256(data).hexdigest()


def compact(value):
    # 与 Go encoding/json 的字符串转义及当前结构字段顺序保持一致。
    raw = json.dumps(value, ensure_ascii=False, separators=(",", ":"))
    for before, after in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"), ("\u2028", "\\u2028"), ("\u2029", "\\u2029")):
        raw = raw.replace(before, after)
    return raw.encode()


def tree_hash(files):
    return digest(compact({name: digest(data) for name, data in sorted(files.items())}))


def relative(name):
    parts = name.split("/")
    if not name or name.startswith("/") or "\\" in name or "\0" in name or any(p in ("", ".", "..", ".git") for p in parts):
        raise ValueError("unsafe relative path")
    return parts


class Source:
    def __init__(self, root):
        self.root = Path(root).resolve(strict=True)
        self.files = {}
        self.directories = {}

    def locate(self, name):
        path = self.root
        for part in relative(name):
            path = path / part
            if path.is_symlink():
                raise ValueError("symbolic links are unsupported")
        return path

    def read(self, name):
        path = self.locate(name)
        info = path.stat()
        if not stat.S_ISREG(info.st_mode) or info.st_size > 16 * 1024 * 1024:
            raise ValueError("source file exceeds supported boundaries")
        raw = path.read_bytes()
        if len(raw) > 16 * 1024 * 1024:
            raise ValueError("source file grew beyond limit")
        self.files[name] = digest(raw)
        return raw

    def json(self, name):
        return json.loads(self.read(name))

    def tree(self, name):
        root = self.locate(name)
        result = {}
        total = 0
        for parent, dirs, files in os.walk(root, followlinks=False):
            names = sorted(dirs + files)
            rel = Path(parent).relative_to(self.root).as_posix()
            self.directories[rel] = names
            for child in names:
                if (Path(parent) / child).is_symlink():
                    raise ValueError("symbolic links are unsupported")
            for child in sorted(files):
                path = Path(parent) / child
                raw = self.read(path.relative_to(self.root).as_posix())
                total += len(raw)
                if len(raw) > 1024 * 1024 or total > 16 * 1024 * 1024 or len(result) >= 10000:
                    raise ValueError("tree exceeds supported boundaries")
                result[path.relative_to(root).as_posix()] = raw
        if not root.is_dir():
            raise ValueError("missing source tree")
        return result

    def verify(self):
        for name, expected in list(self.files.items()):
            if digest(self.locate(name).read_bytes()) != expected:
                raise ValueError("source changed during analysis")
        for name, expected in self.directories.items():
            if sorted(p.name for p in self.locate(name).iterdir()) != expected:
                raise ValueError("source tree membership changed during analysis")


def budget_notice(models, tools):
    content = f"Harness budget (current run):\nModel calls remaining: {models} (including this call).\nTool calls remaining: {tools}."
    turns = models - 1 if tools else 0
    content += f"\nCompletion policy: final-response-v1. The last model call is reserved for a final response. Tool-enabled model calls remaining: {turns}."
    if models == 1 or tools == 0:
        content += "\nTools are disabled for this call. Give a final response describing completed changes, actual checks and unresolved or unverified issues. Your final response submits the current candidate for independent verification; unresolved issues do not prevent submission."
    elif models <= 3:
        content += "\nBudget is low. Prioritize necessary fixes, verification and a final response; avoid expanding scope."
    return {"Role": "user", "Content": content, "ToolCalls": None, "ToolCallID": "", "IsError": False}


def bind_requests(trace, target, ledger, record, tools):
    limits = target["agent_limits"]
    maximum, tool_limit = limits["MaxModelCalls"], limits["MaxToolCalls"]
    total = trace["ModelCalls"]
    if not (0 <= total <= maximum and 0 <= trace["ToolCalls"] <= tool_limit) or len(trace["Usage"] or []) != total:
        raise ValueError("invalid attempt or usage counts")
    own = lambda c: c["run_id"] == record["id"] and c["target_id"] == record["target_id"]
    calls = [c for c in ledger["calls"] if own(c)]
    rejected = [c for c in ledger.get("rejections", []) if own(c)]
    messages = trace["Messages"]
    if len(messages) < 2 or messages[0]["Role"] != "system" or messages[1]["Role"] != "user":
        raise ValueError("unexpected runner initial history")
    index, executed = 2, 0
    expected_marker, bindings, replies = None, [], {}
    seen = set()
    for ordinal in range(1, total + 1):
        remaining = maximum - ordinal + 1
        available = tool_limit - executed
        if index >= len(messages) or messages[index] != budget_notice(remaining, available):
            raise ValueError("budget notice differs from plan and execution history")
        index += 1
        final_only = remaining == 1 or available == 0
        if final_only:
            expected_marker = {"ModelCall": ordinal, "MessageCount": index}
            if ordinal != total:
                raise ValueError("request after final-response boundary")
        expected = digest(compact({"Messages": messages[:index], "Tools": None if final_only else tools}))
        has_reply = index < len(messages) and messages[index]["Role"] == "assistant"
        binding = {"attempt": ordinal, "mode": "final-response-only" if final_only else "tools-enabled", "message_count": index, "input_sha256": expected, "has_reply": has_reply, "call_id": None}
        if ordinal <= len(calls):
            call = calls[ordinal - 1]
            if call["input_sha256"] != expected:
                raise ValueError("model interface input digest mismatch")
            binding.update(budget_status="admitted", call_id=call["id"], settlement=call["state"], outcome=call.get("outcome"))
        else:
            if ordinal != total or has_reply or len(calls) != ordinal - 1:
                raise ValueError("reply or attempt lacks budget admission")
            if rejected:
                if len(rejected) != 1 or rejected[0]["input_sha256"] != expected:
                    raise ValueError("rejected request input digest mismatch")
                binding["budget_status"] = "rejected"
            else:
                binding["budget_status"] = "no-admission-record"
        bindings.append(binding)
        if not has_reply:
            if ordinal != total or index != len(messages) or trace["Reason"] == "completed":
                raise ValueError("incomplete request is not a terminal failure")
            continue
        reply = messages[index]
        binding["reply_message_index"] = index
        replies[index] = binding
        index += 1
        batch = reply.get("ToolCalls") or []
        for call in batch:
            if not call["ID"] or call["ID"] in seen:
                raise ValueError("duplicate tool call identity")
            seen.add(call["ID"])
        received = 0
        while index < len(messages) and messages[index]["Role"] == "tool":
            if final_only or received >= len(batch) or messages[index]["ToolCallID"] != batch[received]["ID"]:
                raise ValueError("unexpected tool execution at response boundary")
            index += 1
            received += 1
            executed += 1
        if ordinal < total and (not batch or received != len(batch)):
            raise ValueError("request has incomplete tool history")
    if index != len(messages) or executed != trace["ToolCalls"] or len(calls) > total or trace.get("FinalResponseAttempt") != expected_marker:
        raise ValueError("attempt marker, trace or ledger counts differ")
    if trace["Reason"] == "completed" and (not bindings or not bindings[-1]["has_reply"] or messages[-1]["Role"] != "assistant" or messages[-1].get("ToolCalls")):
        raise ValueError("completed result lacks final response")
    return bindings, replies


def analyze(run, ledger_path, tools_path, plan_path):
    source = Source(run)
    record = source.json("run.json")
    report = source.json("artifacts/report.json")
    if record["id"] != report["run_id"] or record["status"] != report["status"]:
        raise ValueError("run/report identity mismatch")
    ledger_path, tools_path = Path(ledger_path).resolve(strict=True), Path(tools_path).resolve(strict=True)
    ledger_source, tools_source = Source(ledger_path.parent), Source(tools_path.parent)
    ledger_raw, tools_raw = ledger_source.read(ledger_path.name), tools_source.read(tools_path.name)
    ledger, tools = json.loads(ledger_raw), json.loads(tools_raw)
    budget_ref = report["input"].get("budget")
    if budget_ref and (budget_ref["campaign_id"] != ledger["campaign_id"] or budget_ref["target_id"] != record.get("target_id") or budget_ref["run_id"] != record["id"] or budget_ref["profile_sha256"] != digest(compact(ledger["profile"]))):
        raise ValueError("ledger identity differs from the run budget reference")
    if not report.get("agent_record") and (report.get("generation_diagnostics") or any(c["run_id"] == record["id"] for c in ledger["calls"])):
        raise ValueError("missing agent history despite generated evidence")
    plan_path = Path(plan_path).resolve(strict=True)
    plan_source = Source(plan_path.parent)
    plan_raw = plan_source.read(plan_path.name)
    plan = json.loads(plan_raw)
    targets = [t for t in plan["targets"] if t["input"]["id"] == record.get("target_id")]
    if len(targets) != 1 or plan["id"] != ledger["campaign_id"]:
        raise ValueError("plan identity differs from ledger or run")
    target = targets[0]
    if plan["profile"] != ledger["profile"]:
        raise ValueError("plan model profile differs from ledger")
    task_raw = source.json("inputs/task.json")
    if task_raw != report["input"]:
        raise ValueError("saved task differs from report input")
    if target.get("completion_policy") != "final-response-v1" or report["input"].get("completion_policy") != target["completion_policy"] or report["input"].get("max_model_calls") != target["agent_limits"]["MaxModelCalls"] or report["input"].get("max_tool_calls") != target["agent_limits"]["MaxToolCalls"] or target["input"] != report["input"].get("target") or target.get("generation_diagnostic_limit", 0) != report["input"].get("generation_diagnostic_limit", 0):
        raise ValueError("plan and run strategy or input differ")
    configured = report["input"].get("generation_diagnostic_limit") == 2 and any(t["Name"] == "migration_check" for t in tools)
    out = {"schema_version": 2, "scope": "trajectory facts only; requires separate archive audit and independent candidate review", "run_id": record["id"], "target_id": record.get("target_id"), "recorded_status": record["status"], "recorded_verdict": record.get("verdict"), "tool_configured": configured, "tool_provided": None, "trace_available": bool(report.get("agent_record")), "baseline_reads": [], "diagnostic_proposals": 0, "diagnostic_attempts_with_result": 0, "diagnostic_errors": 0, "diagnostics": [], "edits": [], "request_bindings": [], "pending_tool_calls": [], "checks": [], "handoff": {"policy": "final-response-v1", "boundary_reached": False, "outcome": "before-agent", "validation_published": report.get("validation") is not None}}
    if report.get("agent_record"):
        if report["agent_record"] != "agent.json":
            raise ValueError("unexpected agent record path")
        trace = source.json("artifacts/agent.json")
        if record.get("agent_exit_reason") and record["agent_exit_reason"] != trace["Reason"]:
            raise ValueError("run exit reason differs from agent history")
        module = report["input"]["repository_name"]
        relative(module)
        baseline = source.tree("baseline/" + module)
        files = dict(baseline)
        if tree_hash(baseline) != report["input"]["baseline_sha256"]:
            raise ValueError("baseline identity mismatch")
        published = {}
        for ref in report.get("generation_diagnostics", []):
            path = ref["path"]
            relative(path)
            raw = source.read(path)
            if digest(raw) != ref["sha256"]:
                raise ValueError("diagnostic report digest mismatch")
            entry = json.loads(raw)
            if entry["run_id"] != record["id"] or entry["target_id"] != record.get("target_id") or entry["profile_sha256"] != report["input"]["profile_sha256"]:
                raise ValueError("cross-run diagnostic identity")
            if entry["tool_call_id"] in published:
                raise ValueError("duplicate diagnostic identity")
            published[entry["tool_call_id"]] = (path, entry)
        bindings, replies = bind_requests(trace, target, ledger, record, tools)
        out["request_bindings"] = bindings
        marker = trace.get("FinalResponseAttempt")
        out["handoff"].update(boundary_reached=marker is not None, attempt=marker, exit_reason=trace["Reason"])
        if marker:
            last = bindings[-1]
            outcome = "final-response-submitted" if trace["Reason"] == "completed" else "boundary-failed"
            if last["budget_status"] != "admitted":
                outcome = "boundary-" + last["budget_status"]
            out["handoff"].update(outcome=outcome, budget_status=last["budget_status"], reply_saved=last["has_reply"])
        else:
            out["handoff"]["outcome"] = "early-submitted" if trace["Reason"] == "completed" else "failed-before-boundary"
        replies_by_id = {b["call_id"]: b for b in bindings if b["has_reply"]}
        pending, seen, diagnostics = {}, set(), []
        reply_index, results = 0, 0
        messages = trace["Messages"]
        for index, message in enumerate(messages):
            if message["Role"] == "assistant":
                binding = replies[index]
                call = {"id": binding["call_id"]}
                if binding["mode"] == "tools-enabled":
                    out["tool_provided"] = any(t["Name"] == "migration_check" for t in tools)
                elif out["tool_provided"] is None:
                    out["tool_provided"] = False
                reply_index += 1
                for d in diagnostics:
                    if d["following_reply_call_id"] is None:
                        d["following_reply_call_id"] = call["id"]
                for item in message.get("ToolCalls") or []:
                    if item["ID"] in seen:
                        raise ValueError("duplicate tool call identity")
                    seen.add(item["ID"])
                    pending[item["ID"]] = (item, call["id"])
                    if item["Name"] == "migration_check":
                        out["diagnostic_proposals"] += 1
            elif message["Role"] == "tool":
                identity = message["ToolCallID"]
                if identity not in pending:
                    raise ValueError("tool result lacks a matching request")
                item, reply_call = pending.pop(identity)
                results += 1
                name = item["Name"]
                if name == "migration_check":
                    out["diagnostic_attempts_with_result"] += 1
                    if message["IsError"]:
                        out["diagnostic_errors"] += 1
                        continue
                    if identity not in published:
                        raise ValueError("successful diagnostic lacks a snapshot reference")
                    path, entry = published.pop(identity)
                    binding = entry["binding"]
                    if binding["baseline_sha256"] != tree_hash(baseline) or binding["spec_sha256"] != report["input"]["spec_sha256"] or binding["candidate_sha256"] != tree_hash(files):
                        raise ValueError("diagnostic candidate does not match replay")
                    snapshot = source.tree(str(Path(path).parent / "candidate"))
                    if tree_hash(snapshot) != tree_hash(files):
                        raise ValueError("diagnostic snapshot differs from replay")
                    if entry["response"]["IsError"] or entry["response"]["Content"] != message["Content"]:
                        raise ValueError("diagnostic response differs from agent trace")
                    feedback = json.loads(message["Content"])
                    locations = [loc for f in feedback["findings"] for loc in f.get("locations", [])]
                    diagnostics.append({"tool_call_id": identity, "sequence": entry["sequence"], "status": feedback["test_structure"], "candidate_sha256": tree_hash(files), "locations": locations, "omitted_findings": feedback["omitted_findings"], "following_reply_call_id": None, "later_edits": []})
                elif not message["IsError"] and name == "replace_text":
                    args = json.loads(item["Arguments"])
                    path = args["path"]
                    relative(path)
                    old, new = args["old"].encode(), args["new"].encode()
                    if not old or path not in files or files[path].count(old) != 1:
                        raise ValueError("successful edit cannot be uniquely replayed")
                    before = digest(files[path])
                    files[path] = files[path].replace(old, new, 1)
                    edit = {"model_attempt": replies_by_id[reply_call]["attempt"], "candidate_sha256": tree_hash(files), "tool_call_id": identity, "path": path, "reply_call_id": reply_call, "before_sha256": before, "after_sha256": digest(files[path])}
                    out["edits"].append(edit)
                    for d in diagnostics:
                        d["later_edits"].append({"tool_call_id": identity, "path": path, "same_file_as_finding": any(loc.get("file") == path for loc in d["locations"]), "feedback_in_prior_request": d["following_reply_call_id"] is not None})
                elif name == "go_check":
                    args = json.loads(item["Arguments"])
                    kind = args.get("kind")
                    content = message["Content"]
                    out["checks"].append({"tool_call_id": identity, "model_attempt": replies_by_id[reply_call]["attempt"], "kind": kind, "candidate_sha256": tree_hash(files), "tool_error": message["IsError"], "recorded_pass": not message["IsError"] and content.startswith(f"go {kind} passed\n"), "configuration_headers": [line.removeprefix("configuration ") for line in content.splitlines() if line.startswith("configuration ")], "response_sha256": digest(content.encode())})
                elif not message["IsError"] and name in ("list_files", "read_file", "search"):
                    args = json.loads(item["Arguments"])
                    if args.get("root") == "baseline":
                        out["baseline_reads"].append({"tool_call_id": identity, "tool": name, "path": args["path"]})
        if published or results != trace["ToolCalls"] or reply_index != len(replies):
            raise ValueError("trace, ledger or diagnostic publication is incomplete")
        final = source.tree("workspace/" + module)
        if tree_hash(files) != tree_hash(final) or tree_hash(final) != record["candidate_sha256"] or record["candidate_sha256"] != report["candidate_sha256"]:
            raise ValueError("final candidate differs from replay")
        out["diagnostics"] = diagnostics
        out["pending_tool_calls"] = sorted(pending)
        out["final_candidate_sha256"] = tree_hash(files)
        out["last_successful_edit"] = out["edits"][-1] if out["edits"] else None
        out["checks_on_final_candidate"] = [c for c in out["checks"] if c["candidate_sha256"] == tree_hash(files)]
        out["diagnostics_on_final_candidate"] = [d["sequence"] for d in diagnostics if d["candidate_sha256"] == tree_hash(files)]
    source.verify()
    ledger_source.verify()
    tools_source.verify()
    plan_source.verify()
    out.update(valid=True, inputs_unchanged=True, sources=source.files, directory_membership=source.directories, plan_sha256=digest(plan_raw), ledger_sha256=digest(ledger_raw), tools_sha256=digest(tools_raw))
    return out


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", required=True)
    parser.add_argument("--ledger", required=True)
    parser.add_argument("--tools", required=True)
    parser.add_argument("--output", required=True)
    parser.add_argument("--plan", required=True)
    args = parser.parse_args()
    run, output = Path(args.run).resolve(strict=True), Path(args.output).resolve()
    for source in (run, Path(args.ledger).resolve(strict=True).parent, Path(args.tools).resolve(strict=True).parent, Path(args.plan).resolve(strict=True).parent):
        if output.is_relative_to(source) or source.is_relative_to(output):
            parser.error("output overlaps an analysis input")
    output.mkdir(mode=0o700)
    try:
        result = analyze(run, args.ledger, args.tools, args.plan)
    except (ValueError, OSError, KeyError, TypeError) as error:
        (output / "analysis.json").write_text(json.dumps({"valid": False, "error": str(error)}, ensure_ascii=False, indent=2) + "\n")
        raise SystemExit("trajectory analysis failed; inspect the new output directory")
    (output / "analysis.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"valid": True, "trace_available": result["trace_available"], "diagnostics": len(result["diagnostics"]), "request_bindings": len(result["request_bindings"]) }))


if __name__ == "__main__":
    main()
