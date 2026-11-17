"""Planner engine for decomposing complex tasks and driving the Worker execution ring."""

from typing import Any, Dict, List, Optional
from tiancode.domain.models import PlanStep, StepStatus, TaskPlan


class TaskPlanner:
    """Manages task decomposition, step state machines, and focused Worker prompts."""

    def __init__(self) -> None:
        self.active_plan: Optional[TaskPlan] = None

    def create_plan(self, task_title: str, steps_spec: List[Dict[str, Any]]) -> TaskPlan:
        """Create a new TaskPlan from a list of step specifications."""
        steps: List[PlanStep] = []
        for idx, item in enumerate(steps_spec, start=1):
            steps.append(
                PlanStep(
                    step_id=idx,
                    title=item.get("title", f"Step {idx}"),
                    description=item.get("description", ""),
                    verification=item.get("verification", ""),
                    status=StepStatus.TODO,
                )
            )

        plan = TaskPlan(task_title=task_title, steps=steps, current_step_index=0)
        if plan.steps:
            plan.steps[0].status = StepStatus.IN_PROGRESS

        self.active_plan = plan
        return plan

    def format_worker_prompt_context(self, plan: TaskPlan) -> str:
        """Format a focused prompt context for the Worker focusing exclusively on the active step."""
        lines = [
            f"🎯 【全局总任务目标】: {plan.task_title}",
            "━" * 50,
        ]

        # Completed steps summary
        completed_steps = [s for s in plan.steps if s.status == StepStatus.COMPLETED]
        if completed_steps:
            lines.append("✅ 【已完成步骤与产出摘要】:")
            for s in completed_steps:
                summary_text = f" -> {s.result_summary}" if s.result_summary else ""
                lines.append(f"  • 步骤 {s.step_id}: {s.title}{summary_text}")
            lines.append("")

        curr = plan.current_step()
        if curr:
            lines.extend([
                f"🔥 【当前聚焦步骤 [{curr.step_id}]】: {curr.title}",
                f"  • 详细任务说明: {curr.description}",
            ])
            if curr.verification:
                lines.append(f"  • 验收与验证标准: {curr.verification}")
            lines.append(
                "💡 提示：完成当前步骤的代码编写与验证后，请调用 'complete_step(step_id, summary)' 工具流转至下一步。"
            )
        else:
            lines.append("🎉 恭喜！当前计划中的所有步骤均已完成。")

        lines.append("━" * 50)
        return "\n".join(lines)
