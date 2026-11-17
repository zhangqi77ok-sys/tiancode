"""Plan operations tools for decomposing tasks and advancing step state machines."""

from typing import Any, Dict, List
from langchain_core.tools import BaseTool, tool
from tiancode.cli.formatter import TerminalFormatter
from tiancode.core.planner import TaskPlanner


def create_plan_tools(planner: TaskPlanner) -> List[BaseTool]:
    """Factory to create planning tools bound to a specific TaskPlanner instance."""

    @tool
    def create_task_plan(task_title: str, steps: List[Dict[str, str]]) -> str:
        """当面对多文件修改、复杂重构或大型功能开发时，优先调用此工具建立结构化分步任务计划并展示看板。

        参数:
          task_title: 总任务名称与目标
          steps: 步骤列表，每个步骤包含 title (简述), description (具体改动与细节), verification (验证标准)
        """
        plan = planner.create_plan(task_title, steps)
        TerminalFormatter.print_task_plan(plan)
        first_step = plan.current_step()
        next_hint = f"当前请优先聚焦执行步骤 1: '{first_step.title}'。" if first_step else ""
        return f"✅ 结构化任务计划已成功创建，共 {len(plan.steps)} 个步骤。{next_hint}"

    @tool
    def complete_step(step_id: int, summary: str) -> str:
        """当完成当前步骤的代码修改与单测验证后调用此工具，标记本步骤完成并自动推进到下一步。

        参数:
          step_id: 刚完成的步骤编号 (如 1, 2, ...)
          summary: 本步骤修改了哪些文件、产出或测试结果的简短摘要
        """
        if not planner.active_plan:
            return "ℹ️ 当前没有进行中的任务计划。"

        plan = planner.active_plan
        curr = plan.current_step()
        plan.complete_step(step_id, summary=summary)
        TerminalFormatter.print_task_plan(plan)

        if plan.is_finished:
            return f"🎉 恭喜！任务计划中的所有步骤 (共 {len(plan.steps)} 步) 已经全部圆满完成！请向用户简明汇报总体产出。"

        nxt = plan.current_step()
        if nxt:
            return (
                f"✅ 步骤 {step_id} 已标记完成！\n"
                f"👉 接下来请聚焦执行步骤 [{nxt.step_id}]: {nxt.title}\n"
                f"任务细节: {nxt.description}\n"
                f"验证标准: {nxt.verification}"
            )
        return f"✅ 步骤 {step_id} 已标记完成！"

    @tool
    def show_task_plan() -> str:
        """查看当前进行中的任务计划与各步骤状态。"""
        if not planner.active_plan:
            return "ℹ️ 当前没有活跃的任务计划。"
        TerminalFormatter.print_task_plan(planner.active_plan)
        curr = planner.active_plan.current_step()
        curr_info = f"当前聚焦步骤 [{curr.step_id}]: {curr.title}" if curr else "所有步骤已完成。"
        return f"当前任务: {planner.active_plan.task_title}\n{curr_info}"

    return [create_task_plan, complete_step, show_task_plan]
