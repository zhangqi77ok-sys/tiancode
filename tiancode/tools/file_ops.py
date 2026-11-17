"""File operations tools with dependency injection for SnapshotStore."""

import os
from pathlib import Path
from typing import List
from langchain_core.tools import BaseTool, tool
from tiancode.domain.interfaces import SnapshotStore


def create_file_tools(store: SnapshotStore, work_dir: Path) -> List[BaseTool]:
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
        """全量新建或覆盖写入文件（自动纳入快照安全管理）。"""
        target = _resolve(filename)
        target.parent.mkdir(parents=True, exist_ok=True)
        is_new = not target.exists()
        original = "" if is_new else open(target, "r", encoding="utf-8", errors="replace").read()

        store.record_snapshot(str(target), original, is_new=is_new)
        with open(target, "w", encoding="utf-8") as f:
            f.write(content)
        return f"✅ 文件 '{filename}' 写入成功！"

    @tool
    def edit_file(filename: str, old_code: str, new_code: str) -> str:
        """局部修改文件：将原代码中唯一的 old_code 精准替换为 new_code。"""
        target = _resolve(filename)
        if not target.exists():
            return f"❌ 文件 '{filename}' 不存在，请先确认路径！"
        with open(target, "r", encoding="utf-8", errors="replace") as f:
            src = f.read()
        if old_code not in src:
            return f"❌ 替换失败：文件中未找到完全匹配的旧代码！当前源码如下，请对照后再修改：\n{src}"

        store.record_snapshot(str(target), src, is_new=False)
        modified = src.replace(old_code, new_code, 1)
        with open(target, "w", encoding="utf-8") as f:
            f.write(modified)
        return f"✅ 文件 '{filename}' 局部替换成功！"

    return [list_files, get_repo_map, read_file, write_file, edit_file]
