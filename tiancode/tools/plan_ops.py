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
        if curr is None:
            return "ℹ️ 当前任务计划已无待处理步骤。"
        if curr.step_id != step_id:
            return f"❌ 拒绝跨步骤跳转：当前正在执行步骤 [{curr.step_id}]: '{curr.title}'，禁止直接标记步骤 {step_id} 完成！请严格按序执行。"

        success = plan.complete_step(step_id, summary=summary)
        if not success:
            return f"❌ 标记步骤 {step_id} 完成失败。"

        TerminalFormatter.print_task_plan(plan)

        if plan.is_finished:
            return f"🎉 恭喜！任务计划中的所有步骤 (共 {len(plan.steps)} 步) 已经全部圆满完成！请向用户简明汇报总体产出。"
        if plan.is_terminated:
            return f"🏁 任务计划已达到终态（部分步骤成功/失败，共 {len(plan.steps)} 步）。请向用户汇报最终交付与未达成项。"

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
    def fail_step(step_id: int, reason: str) -> str:
        """当当前步骤执行受阻、单测失败且无法修复、或前置条件不满足时调用此工具，如实记录失败原因并推进到下一步或终态。

        参数:
          step_id: 当前失败的步骤编号 (如 1, 2, ...)
          reason: 导致此步骤失败的具体技术原因、异常日志或无法满足的条件
        """
        if not planner.active_plan:
            return "ℹ️ 当前没有进行中的任务计划。"

        plan = planner.active_plan
        curr = plan.current_step()
        if curr is None:
            return "ℹ️ 当前任务计划已无待处理步骤。"
        if curr.step_id != step_id:
            return f"❌ 拒绝跨步骤跳转：当前正在执行步骤 [{curr.step_id}]: '{curr.title}'，禁止直接标记步骤 {step_id} 失败！"

        success = plan.fail_step(step_id, reason=reason)
        if not success:
            return f"❌ 标记步骤 {step_id} 失败操作未执行成功。"

        TerminalFormatter.print_task_plan(plan)

        if plan.is_terminated:
            return (
                f"⚠️ 步骤 {step_id} 已标记为失败 (原因: {reason})。\n"
                f"🏁 任务计划所有步骤已全部处理完毕，达到终态。请向用户汇报整体完成情况与失败原因。"
            )

        nxt = plan.current_step()
        if nxt:
            return (
                f"⚠️ 步骤 {step_id} 已标记为失败 (原因: {reason})。\n"
                f"👉 自动流转推进到后续步骤 [{nxt.step_id}]: {nxt.title}\n"
                f"任务细节: {nxt.description}\n"
                f"验证标准: {nxt.verification}"
            )
        return f"⚠️ 步骤 {step_id} 已标记为失败 (原因: {reason})。"

    @tool
    def show_task_plan() -> str:
        """查看当前进行中的任务计划与各步骤状态。"""
        if not planner.active_plan:
            return "ℹ️ 当前没有活跃的任务计划。"
        TerminalFormatter.print_task_plan(planner.active_plan)
        curr = planner.active_plan.current_step()
        curr_info = f"当前聚焦步骤 [{curr.step_id}]: {curr.title}" if curr else "所有步骤已完成。"
        return f"当前任务: {planner.active_plan.task_title}\n{curr_info}"

    return [create_task_plan, complete_step, fail_step, show_task_plan]
