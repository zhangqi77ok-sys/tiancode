"""Subagent execution runner with strict read-only sandboxing and context firewalls."""

import os
from pathlib import Path
from typing import Any, List
from langchain_core.messages import HumanMessage, SystemMessage, ToolMessage
from langchain_core.tools import BaseTool, tool
from tiancode.domain.models import SubagentReport

SUBAGENT_SYSTEM_PROMPT = """你是一个在独立沙箱中工作的只读型子智能体（TianCode Subagent: {role}）。
你的唯一职责是：深度检索代码、排查日志、分析架构并提炼事实。

【铁律】
1. 只读原则：你处于沙箱保护中，只拥有 read_file、list_files 与 get_repo_map 权限，绝不修改任何生产代码。
2. 极致精炼：主智能体正在等待你的结论。请专注于提取“根因、具体文件、精确行号与关键事实”，杜绝客套与冗余废话。
3. 总结格式：调研完毕后，请在最终回答中清晰给出：
   核心结论：...
   - 事实1：...
   - 事实2：...
   - 关联文件：...
"""


class SubagentRunner:
    """Manages the lifecycle and sandboxed execution of lightweight subagents."""

    def __init__(self, work_dir: Path, llm_client: Any) -> None:
        self.work_dir = Path(work_dir).resolve()
        self.llm = llm_client

    def get_sandboxed_tools(self) -> List[BaseTool]:
        """Provide strictly read-only tools to prevent workspace mutation during research."""

        def _resolve(filename: str) -> Path:
            p = Path(filename)
            return p if p.is_absolute() else (self.work_dir / p)

        @tool
        def list_files(directory: str = ".") -> str:
            """列出指定目录下的代码和配置文件。"""
            target = _resolve(directory)
            if not target.exists():
                return f"❌ 目录 {directory} 不存在！"
            exts = (".py", ".json", ".txt", ".md", ".toml", ".yaml", ".yml", ".java", ".go", ".rs", ".js", ".ts", ".log")
            matched = []
            for root, _, files in os.walk(target):
                for f in files:
                    if f.endswith(exts):
                        rel = Path(root, f).relative_to(target)
                        matched.append(str(rel))
            return f"当前项目文件：{matched}" if matched else "目录为空或未发现代码文件。"

        @tool
        def get_repo_map() -> str:
            """获取当前工程的代码拓扑全景图。"""
            from tiancode.core.repo_map import generate_repo_map
            return generate_repo_map(self.work_dir)

        @tool
        def read_file(filename: str) -> str:
            """读取文件源码或日志内容。"""
            target = _resolve(filename)
            if not target.exists():
                return f"❌ 文件 '{filename}' 不存在！"
            with open(target, "r", encoding="utf-8", errors="replace") as f:
                return f.read()

        return [list_files, get_repo_map, read_file]

    def run(self, objective: str, role: str = "researcher") -> SubagentReport:
        """Execute sandboxed subagent loop and synthesize a SubagentReport."""
        tools = self.get_sandboxed_tools()
        tool_map = {t.name: t for t in tools}

        sys_prompt = SUBAGENT_SYSTEM_PROMPT.format(role=role)
        messages = [
            SystemMessage(content=sys_prompt),
            HumanMessage(content=f"调研任务目标: {objective}\n请调用只读工具深入排查并给出结论。"),
        ]

        llm_with_tools = self.llm.bind_tools(tools) if hasattr(self.llm, "bind_tools") else self.llm

        # Run up to 4 autonomous research iterations
        final_text = ""
        for _ in range(4):
            ai_msg = llm_with_tools.invoke(messages)
            messages.append(ai_msg)

            if not getattr(ai_msg, "tool_calls", None):
                final_text = ai_msg.content or ""
                break

            for tc in ai_msg.tool_calls:
                t_name = tc.get("name")
                t_args = tc.get("args", {})
                t_id = tc.get("id", "call_dummy")
                tool_fn = tool_map.get(t_name)
                obs = tool_fn.invoke(t_args) if tool_fn else f"未知工具 {t_name}"
                messages.append(ToolMessage(tool_call_id=t_id, content=str(obs)))

        if not final_text and messages:
            final_text = messages[-1].content or ""

        # Parse findings and referenced files from output
        findings = []
        files = []
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
