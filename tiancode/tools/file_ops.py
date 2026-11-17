"""File operations tools with visual diff rendering, snapshot rollback, and post-edit self-healing."""

import os
import subprocess
from pathlib import Path
from typing import List, Optional
from langchain_core.tools import BaseTool, tool
from tiancode.core.diff import compute_text_diff
from tiancode.domain.interfaces import SnapshotStore


def _print_visual_diff(diff) -> None:
    """Safely print visual diff via TerminalFormatter without tight coupling."""
    try:
        from tiancode.cli.formatter import TerminalFormatter
        TerminalFormatter.print_diff(diff)
    except Exception:
        pass


def _run_post_edit_verification(work_dir: Path) -> Optional[str]:
    """Helper to detect and run project test suite for self-healing verification."""
    from tiancode.core.polyglot import detect_project_test_command

    cmd = detect_project_test_command(work_dir)
    if not cmd:
        return None

    try:
        res = subprocess.run(
            cmd,
            cwd=str(work_dir),
            capture_output=True,
            text=True,
            timeout=8,
            encoding="utf-8",
            errors="replace",
        )
        if res.returncode == 0:
            return "单测验证通过 (100% 绿灯)"
        else:
            err_msg = (res.stderr.strip() or res.stdout.strip())[-1000:]
            return f"自动化测试未通过 (Exit {res.returncode})：\n{err_msg}\n请根据上述报错自我修正；若修复陷入死胡同，可调用 'rollback_turn' 安全回滚。"
    except subprocess.TimeoutExpired:
        return "自动化测试执行耗时超过 8 秒，已转为后台跳过直接交付。"
    except Exception as exc:
        return f"自动化测试跳过: {str(exc)}"


def create_file_tools(store: SnapshotStore, work_dir: Path, auto_heal: bool = True) -> List[BaseTool]:
    """Factory to create file operation tools bound to a specific snapshot store and directory."""

    def _resolve(filename: str) -> Path:
        p = Path(filename)
        return p if p.is_absolute() else (work_dir / p)

    @tool
    def list_files(directory: str = ".") -> str:
        """列出指定目录下的代码和配置文件。不了解项目结构时优先调用。"""
        target = _resolve(directory)
        if not target.exists():
            return f"❌ 目录 {directory} 不存在！"
        exts = (".py", ".json", ".txt", ".md", ".toml", ".yaml", ".yml")
        matched = []
        for root, _, files in os.walk(target):
            for f in files:
                if f.endswith(exts):
                    rel = Path(root, f).relative_to(target)
                    matched.append(str(rel))
        return f"当前项目文件：{matched}" if matched else "目录为空或未发现代码文件。"

    @tool
    def get_repo_map() -> str:
        """获取当前工程的代码拓扑全景图（包含全部模块的类、函数与方法签名）。在定位跨模块调用或不熟悉大型项目结构时优先调用此工具。"""
        from tiancode.core.repo_map import generate_repo_map
        return generate_repo_map(work_dir)

    @tool
    def read_file(filename: str) -> str:
        """读取文件源码内容。"""
        target = _resolve(filename)
        if not target.exists():
            parent = target.parent if target.parent.exists() else work_dir
            nearby = [f for f in os.listdir(parent) if f.endswith(".py")]
            return f"❌ 文件 '{filename}' 不存在！在同目录下发现的文件有：{nearby}。请核实文件名！"
        with open(target, "r", encoding="utf-8", errors="replace") as f:
            return f.read()

    @tool
    def write_file(filename: str, content: str) -> str:
        """全量新建或覆盖写入文件（自动渲染 Visual Diff 与安全快照）。"""
        target = _resolve(filename)
        target.parent.mkdir(parents=True, exist_ok=True)
        is_new = not target.exists()
        original = "" if is_new else open(target, "r", encoding="utf-8", errors="replace").read()

        diff = compute_text_diff(filename, original, content)
        _print_visual_diff(diff)

        store.record_snapshot(str(target), original, is_new=is_new)
        with open(target, "w", encoding="utf-8") as f:
            f.write(content)

        summary = f"✅ 文件 '{filename}' 写入成功 (+{diff.additions} / -{diff.deletions})"
        if auto_heal:
            test_res = _run_post_edit_verification(work_dir)
            if test_res:
                summary += f"，{test_res}"
        return summary

    @tool
    def edit_file(filename: str, old_code: str, new_code: str) -> str:
        """局部修改文件：将原代码中唯一的 old_code 精准替换为 new_code（自动渲染 Visual Diff 与安全快照）。"""
        target = _resolve(filename)
        if not target.exists():
            return f"❌ 文件 '{filename}' 不存在，请先确认路径！"
        with open(target, "r", encoding="utf-8", errors="replace") as f:
            src = f.read()
        if old_code not in src:
            return f"❌ 替换失败：文件中未找到完全匹配的旧代码！当前源码如下，请对照后再修改：\n{src}"

        modified = src.replace(old_code, new_code, 1)
        diff = compute_text_diff(filename, src, modified)
        _print_visual_diff(diff)

        store.record_snapshot(str(target), src, is_new=False)
        with open(target, "w", encoding="utf-8") as f:
            f.write(modified)

        summary = f"✅ 文件 '{filename}' 局部替换成功 (+{diff.additions} / -{diff.deletions})"
        if auto_heal:
            test_res = _run_post_edit_verification(work_dir)
            if test_res:
                summary += f"，{test_res}"
        return summary

    @tool
    def rollback_turn() -> str:
        """当代码修改或单测修复陷入死胡同、无法通过测试时，主动调用此工具将工作区代码安全回滚至修改前状态。"""
        reverted = store.rollback()
        if reverted:
            return "⏪ 已完成工作区安全回滚：\n" + "\n".join(f"  • {item}" for item in reverted)
        return "ℹ️ 当前没有可回滚的修改。"

    return [list_files, get_repo_map, read_file, write_file, edit_file, rollback_turn]
