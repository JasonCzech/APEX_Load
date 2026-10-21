"""Render execution evidence separately from static test coverage."""

from datetime import date, datetime


def render_lane_status(ledger: dict) -> list[str]:
    as_of = date.fromisoformat(ledger["as_of"])
    stale_after = ledger.get("stale_after_days", 30)
    if not isinstance(stale_after, int) or stale_after < 1:
        raise ValueError("stale_after_days must be a positive integer")
    rows = ["## Last verified executions", "",
            f"Evidence checked through {as_of.isoformat()}; successes older than {stale_after} days are marked stale. "
            "Coverage counts above do not prove that a lane ran. Missing receipts are unverified, never green.", "",
            "| Lane | Latest status | Last verified success | Evidence / limitation |",
            "|---|---|---|---|"]
    seen = set()
    for lane in ledger["lanes"]:
        name = lane["name"]
        if name in seen:
            raise ValueError(f"duplicate execution lane: {name}")
        seen.add(name)
        status = lane["status"]
        if status not in {"passed", "failed", "not configured", "not run", "unverified"}:
            raise ValueError(f"invalid execution status: {status}")
        success = lane.get("last_success")
        last = "No verified success recorded"
        if success:
            at = datetime.fromisoformat(success["at"].replace("Z", "+00:00")).date()
            if at > as_of:
                raise ValueError(f"future execution receipt for {name}")
            evidence = success["evidence"]
            if not evidence:
                raise ValueError(f"missing success receipt for {name}")
            # Review evidence other than Markdown records is git-ignored, so cite its path.
            target = evidence.split("#", 1)[0]
            if "reviews/" in target and not target.endswith(".md"):
                last = f"{at.isoformat()} (`{evidence}`)"
            else:
                last = f"[{at.isoformat()}]({evidence})"
            if (as_of - at).days > stale_after:
                last += " (stale)"
        elif status == "passed":
            raise ValueError(f"passed lane has no success receipt: {name}")
        detail = lane.get("detail", "").replace("|", "\\|").replace("\n", " ")
        rows.append(f"| {name} | {status} | {last} | {detail} |")
    return [*rows, ""]
