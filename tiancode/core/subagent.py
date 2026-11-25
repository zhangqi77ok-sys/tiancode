"""Subagent execution runner with strict read-only sandboxing and context firewalls."""

import os
from pathlib import Path
from typing import Any, List, Optional
from langchain_core.messages import HumanMessage, SystemMessage
from langchain_core.tools import BaseTool, tool
from tiancode.core.tool_dispatcher import SafeToolDispatcher
from tiancode.domain.models import SubagentReport
from tiancode.tools.file_ops import IGNORED_DIR_NAMES, resolve_safe_path

MAX_SUBAGENT_STEPS = 8

SUBAGENT_SYSTEM_PROMPT = """你是一个在独立沙箱中工作的只读型子智能体（TianCode Subagent: {role}）。
你的唯一职责是：深度检索代码、排查日志、分析架构并提炼事实。

【铁律】
1. 只读原则：你处于沙箱保护中，只拥有 grep_search、read_file、list_files 与 get_repo_map 权限，绝不修改任何生产代码。
2. 高效检索：排查跨文件调用或海量日志时，优先用 grep_search 精准定位行号，再用 read_file 指定 start_line/end_line 分页读取。
3. 极致精炼：主智能体正在等待你的结论。请专注于提取“根因、具体文件、精确行号与关键事实”，杜绝客套与冗余废话。
4. 总结格式：调研完毕后，请在最终回答中清晰给出：
   核心结论：...
   - 事实1：...
   - 事实2：...
   - 关联文件：...
"""


class SubagentRunner:
    """Manages the lifecycle and sandboxed execution of lightweight subagents."""

    def __init__(
        self,
        work_dir: Path,
        llm_client: Any,
        *,
        usage_tracker: Optional[Any] = None,
        model_name: str = "",
    ) -> None:
        self.work_dir = Path(work_dir).resolve()
        self.llm = llm_client
        self.usage_tracker = usage_tracker
        self.model_name = model_name

    def get_sandboxed_tools(self) -> List[BaseTool]:
        """Provide strictly read-only tools to prevent workspace mutation during research."""

        def _resolve(filename: str) -> Path:
            return resolve_safe_path(filename, self.work_dir)

        @tool
        def list_files(directory: str = ".") -> str:
            """列出指定目录下的代码和配置文件。"""
            try:
                target = _resolve(directory)
            except PermissionError as err:
                return f"❌ {str(err)}"

            if not target.exists():
                return f"❌ 目录 {directory} 不存在！"
            exts = (".py", ".json", ".txt", ".md", ".toml", ".yaml", ".yml", ".java", ".go", ".rs", ".js", ".ts", ".log")
            matched = []
            for root, dirs, files in os.walk(target):
                dirs[:] = [d for d in dirs if d.lower() not in IGNORED_DIR_NAMES and not d.startswith(".")]
                if len(Path(root).relative_to(target).parts) > 4:
                    dirs.clear()
                    continue
                for f in files:
                    if f.endswith(exts):
                        full_p = Path(root, f)
                        try:
                            real_p = full_p.resolve()
                            if not real_p.is_relative_to(self.work_dir) or not real_p.is_file():
                                continue
                        except Exception:
                            continue
                        rel = full_p.relative_to(target)
                        matched.append(str(rel))
                        if len(matched) >= 120:
                            break
                if len(matched) >= 120:
                    break
            if not matched:
                return "目录为空或未发现代码文件。"
            trunc_note = " (已显示前 120 个文件)" if len(matched) >= 120 else ""
            return f"当前项目文件{trunc_note}：{matched}"

        @tool
        def get_repo_map(directory: str = ".") -> str:
            """获取当前工程或指定子目录的代码拓扑全景图。"""
            from tiancode.core.repo_map import generate_repo_map
            return generate_repo_map(self.work_dir, directory=directory)

        from tiancode.core.search import create_search_tools
        return [list_files, get_repo_map, *create_search_tools(self.work_dir, IGNORED_DIR_NAMES)]

    def _invoke_tracked(self, runnable: Any, messages: List[Any]) -> Any:
        """Invoke LLM runnable and forward usage_metadata to master TokenUsageTracker."""
        ai_msg = runnable.invoke(messages)
        if self.usage_tracker and getattr(ai_msg, "usage_metadata", None):
            self.usage_tracker.record_turn(
                model=self.model_name,
                usage_metadata=ai_msg.usage_metadata,
            )
        return ai_msg

    @staticmethod
    def _parse_report(final_text: str, role: str, objective: str) -> SubagentReport:
        """Extract structured key findings, file references, and summary from synthesis text."""
        findings: List[str] = []
        files: List[str] = []
        summary = ""

        for line in final_text.splitlines():
            clean = line.strip()
            if not clean:
                continue
            if "核心结论" in clean:
                summary = clean.split("：", 1)[-1].strip() if "：" in clean else clean.split(":", 1)[-1].strip()
            elif clean.startswith(("-", "•", "*")):
                item = clean.lstrip("-•* ").strip()
                if "关联文件" in item:
                    f_part = item.split("：", 1)[-1].strip() if "：" in item else item.split(":", 1)[-1].strip()
                    files.extend([f.strip() for f in f_part.split(",") if f.strip()])
                else:
                    findings.append(item)

        if not summary:
            summary = final_text.splitlines()[0] if final_text.splitlines() else "完成调研排查。"

        return SubagentReport(
            role=role,
            objective=objective,
            key_findings=findings,
            referenced_files=files,
            summary_text=summary,
        )

    def run(self, objective: str, role: str = "researcher") -> SubagentReport:
        """Execute sandboxed subagent loop with SafeToolDispatcher and forced synthesis fallback."""
        tools = self.get_sandboxed_tools()
        dispatcher = SafeToolDispatcher(tools)

        messages: List[Any] = [
            SystemMessage(content=SUBAGENT_SYSTEM_PROMPT.format(role=role)),
            HumanMessage(content=f"调研任务目标: {objective}\n请调用只读工具深入排查并给出结论。"),
        ]
        llm_with_tools = self.llm.bind_tools(tools) if hasattr(self.llm, "bind_tools") else self.llm

        final_text = ""
        for _ in range(MAX_SUBAGENT_STEPS):
            ai_msg = self._invoke_tracked(llm_with_tools, messages)
            messages.append(ai_msg)
            if not getattr(ai_msg, "tool_calls", None):
                final_text = str(ai_msg.content or "")
                break
            messages.extend(dispatcher({"messages": messages})["messages"])

        if not final_text:
            messages.append(
                HumanMessage(
                    content="调研工具步数已达上限，请立即基于以上所有检索结果给出精炼的结构化总结（核心结论、关键事实、关联文件），不要再调用工具。"
                )
            )
            synth_msg = self._invoke_tracked(self.llm, messages)
            final_text = str(getattr(synth_msg, "content", "") or "")

        return self._parse_report(final_text, role, objective)
