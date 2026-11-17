"""Tool operations for spawning and delegating research tasks to subagents."""

from typing import List
from langchain_core.tools import BaseTool, tool
from tiancode.cli.formatter import TerminalFormatter
from tiancode.core.subagent import SubagentRunner


def create_subagent_tools(runner: SubagentRunner) -> List[BaseTool]:
    """Factory to create subagent delegation tools bound to a SubagentRunner."""

    @tool
    def invoke_subagent(objective: str, role: str = "researcher") -> str:
        """派发一个工作在独立沙箱中的只读型子智能体（Subagent）去深入调研大日志、大型代码拓扑或追踪调用链。

        适用场景:
          1. 需要阅读上千行日志、查找报错根因时；
          2. 需要在整个代码库中大范围检索或分析关联依赖时；
          3. 避免将大段脏日志/代码直接读入主会话撑爆上下文。

        参数:
          objective: 具体调研目标与排查问题（如：“排查 app.log 中 OOM 的具体类与行号”）
          role: 子智能体角色，默认为 "researcher"（只读研究员）
        """
        TerminalFormatter.print_info(f"🔍 正在派发只读子智能体 [{role}] 深入沙箱调研: '{objective}'...")
        report = runner.run(objective=objective, role=role)
        TerminalFormatter.print_success(f"子智能体 [{role}] 调研完毕，已生成结构化事实简报！")
        return report.format_for_master()

    return [invoke_subagent]
