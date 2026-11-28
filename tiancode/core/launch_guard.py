"""Refuse a build tool when a file it would run hides a dangerous command.

`git status` and `cmake -E echo` stay allowed. The checks below read the
file the tool would execute: a git config or hook, a `//go:generate` line,
a CMake `execute_process`, a Cargo `rustc-wrapper` or `runner`, a
`PERL5OPT` module under a relative `-I`, and an ssh config `Match exec` or
`ProxyCommand`. A symlink is not followed. This is not a language parser,
and it does not start the tool to ask it.
"""

from __future__ import annotations

import os
import re
import stat
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional, Sequence, Tuple


_MAX_TEXT = 512_000
_MAX_INCLUDES = 8
_MAX_GO_FILES = 500
_GENERATE = re.compile(r"^[ \t]*//go:generate[ \t]+(.+?)\s*$")
_SECTION = re.compile(r"^\s*\[([A-Za-z0-9.-]+)(?:\s+\"([^\"]*)\")?\]\s*$")
_CMAKE_SKIP = frozenset({
    "-E", "--build", "--install", "--open", "--find-package", "--help", "-help",
    "--version", "-version", "--system-information", "--list-presets",
})
_CMAKE_VALUES = frozenset({
    "-P", "-C", "-S", "-B", "-G", "-A", "-T", "-D", "--toolchain",
    "--install-prefix", "--preset",
})
_CMAKE_END = frozenset({
    "COMMAND", "WORKING_DIRECTORY", "TIMEOUT", "RESULT_VARIABLE", "RESULTS_VARIABLE",
    "OUTPUT_VARIABLE", "ERROR_VARIABLE", "INPUT_FILE", "OUTPUT_FILE", "ERROR_FILE",
    "OUTPUT_QUIET", "ERROR_QUIET", "COMMAND_ECHO", "OUTPUT_STRIP_TRAILING_WHITESPACE",
    "ERROR_STRIP_TRAILING_WHITESPACE", "ENCODING", "ECHO_OUTPUT_VARIABLE",
    "ECHO_ERROR_VARIABLE", "COMMAND_ERROR_IS_FATAL",
})
_CARGO_WRAPPER = frozenset({
    "build", "check", "test", "run", "bench", "install", "rustc", "clippy", "fix", "doc",
})
_CARGO_RUNNER = frozenset({"run", "test", "bench"})
_CARGO_VALUES = frozenset({
    "--config", "--manifest-path", "--target-dir", "--color", "-Z", "--message-format",
    "--target", "--features", "--profile", "-j", "--jobs", "--out-dir", "--artifact-dir",
    "--lockfile-path", "--ignore-rust-version", "--timings", "--explain",
})
_GO_VALUES = frozenset({"-C", "-modfile", "-overlay", "-run"})
_INSPECTED = frozenset({"git", "go", "cmake", "cargo", "perl"})
_HOOKS = {
    "commit": ("pre-commit", "prepare-commit-msg", "commit-msg", "post-commit"),
    "merge": ("pre-merge-commit", "commit-msg", "post-merge"),
    "checkout": ("post-checkout",),
    "switch": ("post-checkout",),
    "rebase": ("pre-rebase", "post-checkout", "post-rewrite"),
    "push": ("pre-push",),
    "am": ("applypatch-msg", "pre-applypatch", "post-applypatch"),
}
_WRAPPER_VALUE = re.compile(
    r"build\.rustc-wrapper\s*=\s*(?:\"([^\"]*)\"|'([^']*)'|(\S+))",
    re.IGNORECASE,
)
_RUNNER_VALUE = re.compile(
    r"(?:^|\.)runner\s*=\s*(?:\"([^\"]*)\"|'([^']*)'|(\S+))",
    re.IGNORECASE,
)
_PERL_SUB = re.compile(r"\bsub\b\s*(?:[A-Za-z_][A-Za-z0-9_]*\s*)?\{")


def launch_block_reason(command: str, work_dir: Path) -> Optional[str]:
    """Why this command must not start, or None when no on-disk launcher is unsafe."""
    from tiancode.tools.execution import (
        _directory_inside,
        _peel_prefixes,
        _quote_aware_segments,
        _shell_words,
        _split_words,
        _unquote_shell_word,
        get_sanitized_env,
    )

    root = work_dir.resolve()
    cwd: Optional[Path] = root
    parent = get_sanitized_env()
    # `export GIT_EXTERNAL_DIFF=...; git diff` is one shell. The export is not
    # a prefix of the git segment, but git still inherits it.
    exported_diff: Optional[str] = None
    for segment in _quote_aware_segments(command):
        peeled = _peel_prefixes(_split_words(segment.strip()))
        if peeled is None:
            continue
        assignments, words = peeled
        words = _shell_words(words)
        if not words:
            continue
        program = Path(_unquote_shell_word(words[0])).name.lower()
        plain = [_unquote_shell_word(word) for word in words]
        if program == "cd" or program.startswith("cd"):
            if program != "cd":
                cwd = None
                continue
            target = plain[1] if len(plain) > 1 else ""
            nxt = None if cwd is None else _directory_inside(cwd, target, root)
            cwd = nxt
            continue
        if program == "export":
            for word in plain[1:]:
                name, sep, value = word.partition("=")
                if sep and name == "GIT_EXTERNAL_DIFF":
                    exported_diff = value.strip("'\"")
            continue
        parent_here = dict(parent)
        if exported_diff is not None:
            parent_here["GIT_EXTERNAL_DIFF"] = exported_diff
        if cwd is None and program in {"ssh", "scp", "sftp"}:
            reason = _ssh_reason(plain, root, root)
            if reason:
                return reason
            continue
        if cwd is None and program in _INSPECTED:
            return "命令在越出工作区的目录中运行，已拒绝执行"
        if cwd is None:
            continue
        reason = _reason_for(program, plain, assignments, cwd, root, parent_here)
        if reason:
            return reason
    return None


def _reason_for(
    program: str,
    words: Sequence[str],
    assignments: Sequence[str],
    cwd: Path,
    root: Path,
    parent: Dict[str, str],
) -> Optional[str]:
    if program == "git":
        return _git_reason(words, assignments, cwd, root, parent)
    if program == "go":
        return _go_reason(words, cwd, root)
    if program == "cmake":
        return _cmake_reason(words, cwd, root)
    if program == "cargo":
        return _cargo_reason(words, cwd, root)
    if program == "perl":
        return _perl_reason(words, assignments, cwd, root, parent)
    if program in {"ssh", "scp", "sftp"}:
        return _ssh_reason(words, cwd, root)
    return None


def _git_reason(
    words: Sequence[str],
    assignments: Sequence[str],
    cwd: Path,
    root: Path,
    parent: Dict[str, str],
) -> Optional[str]:
    from tiancode.tools.execution import is_dangerous_command
    from tiancode.tools.hidden_exec import _git_subcommand, commands_git_will_run

    loaded, helpers, include_error = _git_file_config(cwd)
    sub = _git_subcommand(words)
    if include_error and sub and sub not in {"version", "help"}:
        return include_error
    for extra in commands_git_will_run(
        words,
        assignments,
        file_config=loaded,
        parent_env=parent,
        credential_helpers=helpers,
    ):
        if is_dangerous_command(extra):
            return "git 的配置会执行高危命令，已拒绝执行"
    return _hook_reason(words, cwd, root, loaded, assignments, parent)


def _git_file_config(cwd: Path) -> Tuple[Dict[str, str], List[str], Optional[str]]:
    git_dir, dir_error = _git_dir(cwd)
    if dir_error:
        return {}, [], dir_error
    if git_dir is None:
        return {}, [], None
    path = git_dir / "config"
    if not path.exists() and not path.is_symlink():
        return {}, [], None
    return _load_git_config(path, depth=0, seen=set())


def _git_dir(cwd: Path) -> Tuple[Optional[Path], Optional[str]]:
    entry = cwd / ".git"
    if entry.is_symlink():
        return None, ".git 是符号链接，已拒绝执行"
    if entry.is_dir():
        return entry, None
    if not entry.is_file():
        return None, None
    text, error = _read_trusted(entry)
    if error or text is None:
        return None, ".git 无法作为普通文件读取，已拒绝执行"
    for line in text.splitlines():
        if line.startswith("gitdir:"):
            raw = line.split(":", 1)[1].strip()
            path = Path(raw)
            if not path.is_absolute():
                path = entry.parent / path
            if path.is_symlink() or not path.is_dir():
                return None, "gitdir 指向符号链接或不存在的目录，已拒绝执行"
            return path, None
    return None, None


def _load_git_config(
    path: Path, *, depth: int, seen: set
) -> Tuple[Dict[str, str], List[str], Optional[str]]:
    key = str(path)
    if key in seen or depth > 4 or len(seen) >= _MAX_INCLUDES:
        return {}, [], "git include 层数过多，无法确认配置，已拒绝执行"
    if path.is_symlink():
        return {}, [], "git 配置是符号链接，已拒绝执行"
    text, error = _read_trusted(path)
    if error:
        return {}, [], "git 配置无法作为普通文件读取，已拒绝执行"
    if text is None:
        return {}, [], None
    seen.add(key)
    config, includes, helpers = _parse_git_config(text)
    base = path.parent
    for raw in includes:
        target = Path(os.path.expanduser(raw))
        if not target.is_absolute():
            target = base / target
        if not target.exists() and not target.is_symlink():
            continue
        nested, nested_helpers, nested_error = _load_git_config(target, depth=depth + 1, seen=seen)
        if nested_error:
            return config, helpers, nested_error
        merged = dict(nested)
        merged.update(config)
        config = merged
        helpers = nested_helpers + helpers
    return config, helpers, None


def _parse_git_config(text: str) -> Tuple[Dict[str, str], List[str], List[str]]:
    config: Dict[str, str] = {}
    includes: List[str] = []
    helpers: List[str] = []
    section = ""
    for line in _logical_lines(text):
        stripped = line.strip()
        if not stripped or stripped.startswith("#") or stripped.startswith(";"):
            continue
        header = _SECTION.match(stripped)
        if header:
            name = header.group(1).lower()
            sub = header.group(2)
            section = f"{name}.{sub.lower()}" if sub else name
            continue
        if "=" not in stripped or not section:
            continue
        key, raw_value = stripped.split("=", 1)
        key = key.strip().lower()
        value = _git_value(raw_value)
        full = f"{section}.{key}"
        if section == "include" and key == "path":
            includes.append(value)
            continue
        if section.startswith("includeif"):
            continue
        if full == "credential.helper":
            # An empty value clears helpers already seen in this file.
            # Git runs every remaining `!` helper, not only the last one.
            if value == "":
                helpers = []
            else:
                helpers.append(value)
        config[full] = value
    return config, includes, helpers


def _logical_lines(text: str) -> List[str]:
    lines: List[str] = []
    pending = ""
    for line in text.splitlines():
        if pending:
            line = pending + line.lstrip()
            pending = ""
        if line.endswith("\\"):
            pending = line[:-1]
            continue
        lines.append(line)
    if pending:
        lines.append(pending)
    return lines


def _git_value(raw: str) -> str:
    text = raw.strip()
    if text[:1] in "'\"":
        quote = text[0]
        end = text.find(quote, 1)
        if end > 0:
            return text[1:end]
    for mark in (" #", " ;", "\t#", "\t;"):
        at = text.find(mark)
        if at >= 0:
            text = text[:at]
    return text.strip()


def _hook_reason(
    words: Sequence[str],
    cwd: Path,
    root: Path,
    file_config: Dict[str, str],
    assignments: Sequence[str],
    parent: Dict[str, str],
) -> Optional[str]:
    from tiancode.tools.hidden_exec import _git_subcommand, _inline_git_config, _slots_from_env

    names = _HOOKS.get(_git_subcommand(words))
    if not names:
        return None

    env = dict(parent)
    for word in assignments:
        name, sep, value = word.partition("=")
        if sep and name.startswith("GIT_CONFIG_"):
            env[name] = value.strip("'\"")
    config = dict(file_config)
    config.update(_slots_from_env(env))
    config.update(_inline_git_config(words))
    directory, error = _hooks_directory(config.get("core.hookspath", ""), cwd, root)
    if error:
        return error
    if directory is None:
        return None
    for name in names:
        path = directory / name
        if not path.exists() and not path.is_symlink():
            continue
        if path.is_symlink():
            return f"git 钩子 {name} 是符号链接，已拒绝执行"
        from tiancode.core.path_safety import is_shared_hardlink

        if is_shared_hardlink(path):
            return f"git 钩子 {name} 是硬链接，已拒绝执行"
        if not _executable(path):
            continue
        text, read_error = _read_trusted(path)
        if read_error or text is None:
            return f"git 钩子 {name} 无法作为普通文件读取，已拒绝执行"
        if _text_runs_danger(text):
            return f"git 钩子 {name} 含高危命令，已拒绝执行"
    return None


def _hooks_directory(
    hooks_path: str, cwd: Path, root: Path
) -> Tuple[Optional[Path], Optional[str]]:
    git_dir, dir_error = _git_dir(cwd)
    if not hooks_path:
        if dir_error:
            return None, dir_error
        if git_dir is None:
            return None, None
        return git_dir / "hooks", None
    path = Path(os.path.expanduser(hooks_path))
    if not path.is_absolute():
        path = cwd / path
    if path.is_symlink():
        return None, "core.hooksPath 是符号链接，已拒绝执行"
    if not _within(root, path):
        return None, "core.hooksPath 越出工作区，已拒绝执行"
    return path, None


def _go_reason(words: Sequence[str], cwd: Path, root: Path) -> Optional[str]:
    from tiancode.tools.execution import is_dangerous_command

    sub, args, base = _go_invocation(words, cwd, root)
    if sub != "generate":
        return None
    if base is None:
        return "go generate 的目录越出工作区，已拒绝执行"
    if any(word in {"-n", "--dry-run"} for word in args):
        return None
    pattern = _flag_value(args, {"-run"})
    matcher = None
    if pattern:
        try:
            matcher = re.compile(pattern)
        except re.error:
            return "go generate 的 -run 无法确认，已拒绝执行"
    files, error = _go_files(args, base, root)
    if error:
        return error
    for path in files:
        text, read_error = _read_trusted(path)
        if read_error:
            return f"go:generate 文件无法安全读取，已拒绝执行"
        if text is None:
            continue
        for line in text.splitlines():
            match = _GENERATE.match(line)
            if match is None:
                continue
            body = match.group(1).strip()
            if matcher is not None and matcher.search(body) is None:
                continue
            if is_dangerous_command(body):
                return "go:generate 含高危命令，已拒绝执行"
    return None


def _go_invocation(
    words: Sequence[str], cwd: Path, root: Path
) -> Tuple[str, List[str], Optional[Path]]:
    index = 1
    base: Optional[Path] = cwd
    while index < len(words):
        word = words[index]
        name, eq, _value = word.partition("=")
        if word == "--":
            rest = list(words[index + 1 :])
            if not rest:
                return "", [], base
            return rest[0], rest[1:], base
        if word.startswith("-"):
            if name in {"-C"} and not eq:
                if index + 1 >= len(words):
                    return "", [], None
                target = _resolve(cwd, words[index + 1])
                if target.is_symlink() or not _within(root, target):
                    base = None
                else:
                    base = target
                index += 2
                continue
            if name in _GO_VALUES and not eq:
                index += 2
                continue
            index += 1
            continue
        return word, list(words[index + 1 :]), base
    return "", [], base


def _go_files(args: Sequence[str], cwd: Path, root: Path) -> Tuple[List[Path], Optional[str]]:
    positionals = _positionals(args, {"-run"})
    if not positionals:
        positionals = ["."]
    found: List[Path] = []
    for raw in positionals:
        if raw.startswith("-"):
            continue
        recursive = raw.endswith("...")
        stem = raw[:-3].rstrip("/") if recursive else raw
        if stem in {"", "."}:
            base = cwd
        else:
            base = _resolve(cwd, stem)
        if base.is_symlink() or (base.exists() and not _within(root, base)):
            return [], "go generate 的路径越出工作区或是符号链接，已拒绝执行"
        if raw.endswith(".go") and not recursive:
            if not _within(root, base):
                return [], "go generate 的路径越出工作区，已拒绝执行"
            found.append(base)
            continue
        walker = base.rglob("*.go") if recursive else base.glob("*.go")
        if not base.exists():
            continue
        for path in walker:
            if any(part in {"vendor", ".git"} for part in path.parts):
                continue
            if path.is_symlink():
                return [], "go:generate 文件是符号链接，已拒绝执行"
            if not _within(root, path):
                return [], "go generate 的路径越出工作区，已拒绝执行"
            found.append(path)
            if len(found) > _MAX_GO_FILES:
                return [], "go:generate 文件过多，无法确认每一条指令，已拒绝执行"
    return found, None


def _cmake_reason(words: Sequence[str], cwd: Path, root: Path) -> Optional[str]:
    from tiancode.tools.execution import is_dangerous_command

    paths, error = _cmake_files(words, cwd, root)
    if error:
        return error
    for path in paths:
        if path.is_symlink():
            return "CMake 脚本是符号链接，已拒绝执行"
        if path.exists() and not _within(root, path):
            return "CMake 脚本越出工作区，已拒绝执行"
        text, read_error = _read_trusted(path)
        if read_error:
            return "CMake 脚本无法作为普通文件读取，已拒绝执行"
        if text is None:
            continue
        for argv in _cmake_execute_commands(text):
            rendered = " ".join(_cmake_expand(part) for part in argv)
            if is_dangerous_command(rendered):
                return "CMake execute_process 含高危命令，已拒绝执行"
    return None


def _cmake_files(
    words: Sequence[str], cwd: Path, root: Path
) -> Tuple[List[Path], Optional[str]]:
    if any(word.split("=", 1)[0] in _CMAKE_SKIP for word in words[1:]):
        return [], None
    scripts: List[str] = []
    preloads: List[str] = []
    source = ""
    positionals: List[str] = []
    index = 1
    while index < len(words):
        word = words[index]
        name, eq, value = word.partition("=")
        if word == "--":
            positionals.extend(words[index + 1 :])
            break
        if word.startswith("-"):
            attached = value if eq else ""
            takes = name in _CMAKE_VALUES and not eq
            nxt = words[index + 1] if takes and index + 1 < len(words) else ""
            payload = attached or (nxt if takes else "")
            if name in {"-P"}:
                scripts.append(payload)
            elif name in {"-C"}:
                preloads.append(payload)
            elif name in {"-S"}:
                source = payload
            if takes and nxt:
                index += 2
                continue
            index += 1
            continue
        positionals.append(word)
        index += 1
    if scripts:
        raw_paths = [(raw, False) for raw in scripts]
    else:
        raw_paths = [(raw, False) for raw in preloads]
        raw_paths.append((source or (positionals[0] if positionals else "."), True))
    paths: List[Path] = []
    for raw, is_source in raw_paths:
        if not raw:
            return [], "CMake 脚本路径无法确认，已拒绝执行"
        path = _resolve(cwd, raw)
        paths.append(path / "CMakeLists.txt" if is_source else path)
    return paths, None


def _cmake_execute_commands(text: str) -> List[List[str]]:
    cleaned = _strip_cmake_comments(text)
    found: List[List[str]] = []
    for match in re.finditer(r"\bexecute_process\s*\(", cleaned, re.IGNORECASE):
        body = _balanced(cleaned, match.end())
        if body is None:
            continue
        found.extend(_cmake_command_lists(body))
    return found


def _cmake_command_lists(body: str) -> List[List[str]]:
    tokens = _cmake_tokens(body)
    lists: List[List[str]] = []
    index = 0
    while index < len(tokens):
        if tokens[index].upper() != "COMMAND":
            index += 1
            continue
        index += 1
        argv: List[str] = []
        while index < len(tokens) and tokens[index].upper() not in _CMAKE_END:
            argv.append(tokens[index])
            index += 1
        if argv:
            lists.append(argv)
    return lists


def _cmake_tokens(body: str) -> List[str]:
    tokens: List[str] = []
    index = 0
    limit = len(body)
    while index < limit:
        if body[index].isspace():
            index += 1
            continue
        if body[index] == '"':
            end = index + 1
            buf: List[str] = []
            while end < limit:
                if body[end] == "\\":
                    buf.append(body[end + 1] if end + 1 < limit else "")
                    end += 2
                    continue
                if body[end] == '"':
                    break
                buf.append(body[end])
                end += 1
            tokens.append("".join(buf))
            index = end + 1
            continue
        start = index
        while index < limit and not body[index].isspace():
            index += 1
        tokens.append(body[start:index])
    return tokens


def _cmake_expand(token: str) -> str:
    if token.upper() in {"${CMAKE_COMMAND}", "$CACHE{CMAKE_COMMAND}"}:
        return "cmake"
    return token


def _strip_cmake_comments(text: str) -> str:
    out: List[str] = []
    index = 0
    limit = len(text)
    while index < limit:
        if text[index] == "#":
            end = _cmake_bracket(text, index)
            if end is not None:
                index = end
                out.append(" ")
                continue
            newline = text.find("\n", index)
            index = limit if newline < 0 else newline
            continue
        if text[index] == '"':
            end = index + 1
            out.append('"')
            while end < limit:
                out.append(text[end])
                if text[end] == "\\":
                    if end + 1 < limit:
                        out.append(text[end + 1])
                    end += 2
                    continue
                if text[end] == '"':
                    end += 1
                    break
                end += 1
            index = end
            continue
        out.append(text[index])
        index += 1
    return "".join(out)


def _cmake_bracket(text: str, index: int) -> Optional[int]:
    """End index of a `#[=*[ ... ]=*]`, or None when this `#` is a line comment."""
    if index + 1 >= len(text) or text[index + 1] != "[":
        return None
    cursor = index + 2
    equals = 0
    while cursor < len(text) and text[cursor] == "=":
        equals += 1
        cursor += 1
    if cursor >= len(text) or text[cursor] != "[":
        return None
    closer = "]" + ("=" * equals) + "]"
    end = text.find(closer, cursor + 1)
    if end < 0:
        return len(text)
    return end + len(closer)


def _cargo_reason(words: Sequence[str], cwd: Path, root: Path) -> Optional[str]:
    sub, manifest = _cargo_subcommand(words)
    if sub not in _CARGO_WRAPPER and sub not in _CARGO_RUNNER:
        return None
    bases = [cwd]
    if manifest:
        manifest_path = _resolve(cwd, manifest)
        if manifest_path.is_symlink() or not _within(root, manifest_path):
            return "Cargo 清单越出工作区或是符号链接，已拒绝执行"
        bases.insert(0, manifest_path.parent)
    wrappers: List[object] = []
    runners: List[object] = []
    for base in bases:
        for path in _cargo_config_files(base, root):
            if not path.exists() and not path.is_symlink():
                continue
            if path.is_symlink():
                return "Cargo 配置是符号链接，已拒绝执行"
            text, error = _read_trusted(path)
            if error or text is None:
                return "Cargo 配置无法作为普通文件读取，已拒绝执行"
            parsed = _toml(text)
            if parsed is None:
                continue
            build = parsed.get("build") if isinstance(parsed, dict) else None
            if isinstance(build, dict) and "rustc-wrapper" in build:
                wrappers.append(build.get("rustc-wrapper"))
            target = parsed.get("target") if isinstance(parsed, dict) else None
            if isinstance(target, dict):
                for table in target.values():
                    if isinstance(table, dict) and "runner" in table:
                        runners.append(table.get("runner"))
    for raw in _flag_values(words, "--config"):
        wrapper = _first_group(_WRAPPER_VALUE.search(raw))
        runner = _first_group(_RUNNER_VALUE.search(raw))
        if wrapper is not None:
            wrappers.append(wrapper)
        if runner is not None:
            runners.append(runner)
    if sub in _CARGO_WRAPPER:
        for value in wrappers:
            reason = _cargo_value_reason(value, cwd, root, "rustc-wrapper")
            if reason:
                return reason
    if sub in _CARGO_RUNNER:
        for value in runners:
            reason = _cargo_value_reason(value, cwd, root, "runner")
            if reason:
                return reason
    return None


def _cargo_value_reason(value: object, cwd: Path, root: Path, kind: str) -> Optional[str]:
    from tiancode.tools.execution import is_dangerous_command

    if isinstance(value, list):
        text = " ".join(str(part) for part in value)
        program = str(value[0]) if value else ""
    else:
        text = str(value or "").strip()
        program = text.split()[0] if text else ""
    if text and is_dangerous_command(text):
        return f"Cargo {kind} 含高危命令，已拒绝执行"
    if not program or not (program.startswith(("./", "/", "../")) or "/" in program):
        return None
    path = _resolve(cwd, program)
    if path.is_symlink():
        return f"Cargo {kind} 是符号链接，已拒绝执行"
    from tiancode.core.path_safety import is_shared_hardlink

    if is_shared_hardlink(path):
        return f"Cargo {kind} 是硬链接，已拒绝执行"
    if not _within(root, path):
        return f"Cargo {kind} 越出工作区，已拒绝执行"
    if not path.is_file():
        return None
    body, error = _read_trusted(path)
    if error:
        return f"Cargo {kind} 无法作为普通文件读取，已拒绝执行"
    if body and "\x00" not in body[:1024] and _text_runs_danger(body):
        return f"Cargo {kind} 脚本含高危命令，已拒绝执行"
    return None


def _cargo_subcommand(words: Sequence[str]) -> Tuple[str, str]:
    manifest = ""
    index = 1
    while index < len(words):
        word = words[index]
        name, eq, value = word.partition("=")
        if word == "--":
            rest = words[index + 1 :]
            return (rest[0], manifest) if rest else ("", manifest)
        if word.startswith("-"):
            if name == "--manifest-path":
                manifest = value if eq else (words[index + 1] if index + 1 < len(words) else "")
            if name in _CARGO_VALUES and not eq:
                index += 2
                continue
            index += 1
            continue
        return word, manifest
    return "", manifest


def _cargo_config_files(start: Path, root: Path) -> List[Path]:
    found: List[Path] = []
    current = start if start.is_dir() else start.parent
    while True:
        try:
            current.resolve().relative_to(root)
        except (OSError, ValueError):
            break
        for name in (".cargo/config.toml", ".cargo/config"):
            found.append(current / name)
        if current.resolve() == root:
            break
        parent = current.parent
        if parent == current:
            break
        current = parent
    return found


def _perl_reason(
    words: Sequence[str],
    assignments: Sequence[str],
    cwd: Path,
    root: Path,
    parent: Dict[str, str],
) -> Optional[str]:
    if any(word in {"-h", "--help"} for word in words[1:]):
        return None
    env = dict(parent)
    for word in assignments:
        name, sep, value = word.partition("=")
        if sep and name == "PERL5OPT":
            env["PERL5OPT"] = value.strip("'\"")
    option = env.get("PERL5OPT", "")
    if not option.strip():
        return None
    includes, modules = _perl5opt_parts(option)
    if not modules:
        return None
    roots = []
    for raw in includes:
        path = _resolve(cwd, raw)
        if path.is_symlink() or not _within(root, path):
            continue
        roots.append(path)
    if not roots:
        return None
    for module in modules:
        relative = Path(*module.split("::")).with_suffix(".pm")
        for base in roots:
            candidate = base / relative
            if not candidate.exists() and not candidate.is_symlink():
                continue
            if candidate.is_symlink():
                return "PERL5OPT 加载的模块是符号链接，已拒绝执行"
            from tiancode.core.path_safety import is_shared_hardlink

            if is_shared_hardlink(candidate):
                return "PERL5OPT 加载的模块是硬链接，已拒绝执行"
            if not _within(root, candidate):
                continue
            text, error = _read_trusted(candidate)
            if error or text is None:
                return "PERL5OPT 加载的模块无法作为普通文件读取，已拒绝执行"
            reason = _perl_module_reason(text)
            if reason:
                return reason
            break
    return None


def _perl5opt_parts(value: str) -> Tuple[List[str], List[str]]:
    includes: List[str] = []
    modules: List[str] = []
    words = value.split()
    index = 0
    while index < len(words):
        word = words[index]
        if word == "-I" and index + 1 < len(words):
            includes.append(words[index + 1])
            index += 2
            continue
        if word.startswith("-I") and len(word) > 2:
            includes.append(word[2:])
            index += 1
            continue
        if word.startswith("-M") or word.startswith("-m"):
            name = word[2:].split("=", 1)[0]
            if name:
                modules.append(name)
            index += 1
            continue
        index += 1
    return includes, modules


def _perl_module_reason(text: str) -> Optional[str]:
    from tiancode.tools.command_scan import executed_literals
    from tiancode.tools.execution import is_dangerous_command

    visible = _perl_toplevel(text)
    if is_dangerous_command(visible):
        return "PERL5OPT 加载的模块含高危命令，已拒绝执行"
    for piece in executed_literals("perl", visible):
        if piece.strip() and is_dangerous_command(piece):
            return "PERL5OPT 加载的模块含高危命令，已拒绝执行"
    return None


def _perl_toplevel(text: str) -> str:
    return _strip_perl_subs(_strip_perl_comments(text))


def _strip_perl_comments(text: str) -> str:
    out: List[str] = []
    index = 0
    quote = ""
    while index < len(text):
        char = text[index]
        if quote:
            out.append(char)
            if char == "\\":
                if index + 1 < len(text):
                    out.append(text[index + 1])
                index += 2
                continue
            if char == quote:
                quote = ""
            index += 1
            continue
        if char in "'\"":
            quote = char
            out.append(char)
            index += 1
            continue
        if char == "#":
            newline = text.find("\n", index)
            index = len(text) if newline < 0 else newline
            continue
        out.append(char)
        index += 1
    return "".join(out)


def _strip_perl_subs(text: str) -> str:
    out: List[str] = []
    cursor = 0
    for match in _PERL_SUB.finditer(text):
        brace = match.end() - 1
        end = _brace_end(text, brace)
        out.append(text[cursor:match.start()])
        out.append(" ")
        cursor = end
    out.append(text[cursor:])
    return "".join(out)


def _brace_end(text: str, brace: int) -> int:
    depth = 0
    index = brace
    quote = ""
    while index < len(text):
        char = text[index]
        if quote:
            if char == "\\":
                index += 2
                continue
            if char == quote:
                quote = ""
            index += 1
            continue
        if char in "'\"":
            quote = char
            index += 1
            continue
        if char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return index + 1
        index += 1
    return len(text)


def _text_runs_danger(text: str) -> bool:
    """True when the file, or one of its commands, matches the shell rules.

    A hook starts with `#!/bin/sh`, so `chmod` is not at the start of the
    whole file. Each command line is checked on its own. A comment is not.
    """
    from tiancode.tools.execution import is_dangerous_command

    if is_dangerous_command(text):
        return True
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if is_dangerous_command(stripped):
            return True
    return False


def _read_trusted(path: Path) -> Tuple[Optional[str], Optional[str]]:
    """`(text, error)`. A missing file is `(None, None)`. A link is an error."""
    from tiancode.core.path_safety import is_shared_hardlink

    if path.is_symlink() or is_shared_hardlink(path):
        return None, "link"
    if not path.is_file():
        return None, None
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(path, flags)
    except OSError:
        return None, "unreadable"
    try:
        with os.fdopen(fd, "r", encoding="utf-8", errors="replace") as handle:
            text = handle.read(_MAX_TEXT + 1)
    except OSError:
        return None, "unreadable"
    if len(text) > _MAX_TEXT:
        return None, "too-large"
    return text, None


def _executable(path: Path) -> bool:
    try:
        mode = path.stat().st_mode
    except OSError:
        return False
    return stat.S_ISREG(mode) and bool(mode & 0o111)


def _within(root: Path, path: Path) -> bool:
    try:
        path.resolve().relative_to(root)
    except (OSError, ValueError):
        return False
    return True


def _resolve(cwd: Path, raw: str) -> Path:
    text = os.path.expanduser(raw.strip())
    path = Path(text)
    if not path.is_absolute():
        path = cwd / path
    return path


def _positionals(args: Sequence[str], value_flags: set) -> List[str]:
    found: List[str] = []
    index = 0
    while index < len(args):
        word = args[index]
        name, eq, _value = word.partition("=")
        if word == "--":
            return found + list(args[index + 1 :])
        if word.startswith("-") and word != "-":
            index += 2 if name in value_flags and not eq and index + 1 < len(args) else 1
            continue
        found.append(word)
        index += 1
    return found


def _flag_value(args: Sequence[str], names: set) -> str:
    index = 0
    while index < len(args):
        word = args[index]
        name, eq, value = word.partition("=")
        if name in names:
            if eq:
                return value
            if index + 1 < len(args):
                return args[index + 1]
        index += 1
    return ""


def _flag_values(words: Sequence[str], name: str) -> List[str]:
    found: List[str] = []
    index = 1
    while index < len(words):
        word = words[index]
        key, eq, value = word.partition("=")
        if key == name:
            if eq:
                found.append(value)
            elif index + 1 < len(words):
                found.append(words[index + 1])
                index += 1
        index += 1
    return found


def _first_group(match: Optional[re.Match[str]]) -> Optional[str]:
    if match is None:
        return None
    for group in match.groups():
        if group is not None:
            return group
    return None


def _toml(text: str) -> Optional[dict]:
    try:
        import tomllib
    except ImportError:
        try:
            import tomli as tomllib
        except ImportError:
            return None
    try:
        data = tomllib.loads(text)
    except Exception:
        return None
    return data if isinstance(data, dict) else None


def _balanced(text: str, start: int) -> Optional[str]:
    depth = 1
    index = start
    quote = ""
    while index < len(text):
        char = text[index]
        if quote:
            if char == "\\":
                index += 2
                continue
            if char == quote:
                quote = ""
            index += 1
            continue
        if char == '"':
            quote = char
            index += 1
            continue
        if char == "(":
            depth += 1
        elif char == ")":
            depth -= 1
            if depth == 0:
                return text[start:index]
        index += 1
    return None


_SSH_VALUE_FLAGS = {
    "ssh": frozenset({
        "-B", "-b", "-c", "-D", "-E", "-e", "-F", "-I", "-i", "-J", "-L", "-l",
        "-m", "-O", "-o", "-p", "-Q", "-R", "-S", "-W", "-w",
    }),
    "scp": frozenset({"-c", "-D", "-F", "-i", "-J", "-l", "-o", "-P", "-S", "-X"}),
    "sftp": frozenset({
        "-B", "-b", "-c", "-D", "-F", "-i", "-J", "-l", "-o", "-P", "-R", "-S", "-s",
    }),
}
_SSH_MATCH_ARG = frozenset({
    "exec", "host", "originalhost", "user", "localuser", "localnetwork",
    "rdomain", "address", "tagged",
})
_SSH_MAX_FILES = 16


def _ssh_reason(words: Sequence[str], cwd: Path, root: Path) -> Optional[str]:
    """Block Match exec and a file ProxyCommand that would run a dangerous shell.

    OpenSSH reads the config before it connects. `ssh -V` exits first.
    A local `scp a b` never starts ssh. `Match host other exec "..."` does
    not run when this host fails the earlier test.
    """
    from tiancode.tools.execution import is_dangerous_command

    if not words:
        return None
    kind = Path(words[0]).name.lower()
    if kind not in _SSH_VALUE_FLAGS or _ssh_skips_config(words):
        return None
    if kind == "scp" and not _scp_uses_ssh(words):
        return None
    sources, error = _ssh_sources(words, cwd)
    if error:
        return error
    host, user = _ssh_endpoint(words, kind)
    command_sets_proxy = _ssh_command_sets_proxy(words)
    bodies, proxy, read_error = _ssh_walk(sources, host, user, command_sets_proxy)
    if read_error:
        return read_error
    for body in bodies:
        if is_dangerous_command(body):
            return "ssh 的 Match exec 会执行高危命令，已拒绝执行"
    if proxy and proxy.lower() not in {"none", "false"} and is_dangerous_command(proxy):
        return "ssh 的 ProxyCommand 会执行高危命令，已拒绝执行"
    return None


def _ssh_skips_config(words: Sequence[str]) -> bool:
    """`ssh -V` prints the version and does not open a config file."""
    return any(word in {"-V", "--version"} for word in words[1:])


def _ssh_sources(words: Sequence[str], cwd: Path) -> Tuple[List[Path], Optional[str]]:
    """`-F` replaces both the user and the system file. Otherwise both are read."""
    forced = _ssh_flag_value(words, "-F")
    if forced is not None:
        if not forced:
            return [], "ssh 的 -F 没有配置文件，已拒绝执行"
        path = Path(os.path.expanduser(forced))
        if not path.is_absolute():
            path = cwd / path
        return [path], None
    found: List[Path] = []
    user_file = _ssh_user_config()
    if user_file is not None:
        found.append(user_file)
    system = Path("/etc/ssh/ssh_config")
    if system.exists() or system.is_symlink():
        found.append(system)
    return found, None


def _ssh_user_config() -> Optional[Path]:
    """`~/.ssh/config` from the passwd home. A loose mode makes ssh ignore it.

    A symlink is returned so the reader can refuse it. OpenSSH uses the
    account home, not `$HOME`.
    """
    import pwd

    try:
        home = Path(pwd.getpwuid(os.getuid()).pw_dir)
    except (KeyError, OSError):
        return None
    directory = home / ".ssh"
    path = directory / "config"
    if path.is_symlink() or directory.is_symlink():
        return path
    if not path.exists():
        return None
    try:
        loose = stat.S_IWGRP | stat.S_IWOTH
        if directory.stat().st_mode & loose or path.stat().st_mode & loose:
            return None
    except OSError:
        return path
    return path


def _ssh_flag_value(words: Sequence[str], flag: str) -> Optional[str]:
    """The last `-F file` or `-Ffile`. None when the flag is absent."""
    _positionals, options = _ssh_options(words)
    chosen = None
    for name, value in options:
        if name == flag:
            chosen = value or ""
    return chosen


def _ssh_command_sets_proxy(words: Sequence[str]) -> bool:
    """True when argv already has ProxyCommand, including `=none`."""
    _positionals, options = _ssh_options(words)
    for name, value in options:
        if name != "-o" or not value:
            continue
        key, sep, _rest = value.partition("=")
        if sep and key.strip().lower() == "proxycommand":
            return True
    return False


def _scp_uses_ssh(words: Sequence[str]) -> bool:
    """A colon in a path makes scp treat it as a remote name and start ssh."""
    positionals, _options = _ssh_options(words)
    return any(":" in item for item in positionals)


def _ssh_endpoint(words: Sequence[str], kind: str) -> Tuple[Optional[str], Optional[str]]:
    """`(host, user)` from the destination. User is None when it was not written."""
    positionals, _options = _ssh_options(words)
    spec = ""
    if kind == "ssh":
        spec = positionals[0] if positionals else ""
    elif kind == "sftp":
        spec = positionals[0].split(":", 1)[0] if positionals else ""
    else:
        for item in positionals:
            if ":" in item:
                spec = item.split(":", 1)[0]
                break
    if not spec:
        return None, None
    if spec.startswith("[") and "]" in spec:
        spec = spec[1 : spec.index("]")]
        return spec, None
    if "@" in spec:
        user, host = spec.rsplit("@", 1)
        return host, user or None
    return spec, None


def _ssh_options(words: Sequence[str]) -> Tuple[List[str], List[Tuple[str, Optional[str]]]]:
    kind = Path(words[0]).name.lower() if words else ""
    values = _SSH_VALUE_FLAGS.get(kind, frozenset())
    positionals: List[str] = []
    options: List[Tuple[str, Optional[str]]] = []
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            positionals.extend(words[index + 1 :])
            break
        if word.startswith("--"):
            name, eq, value = word.partition("=")
            options.append((name, value if eq else None))
            index += 1
            continue
        if word.startswith("-") and word != "-":
            flag = f"-{word[1]}"
            rest = word[2:]
            if flag in values:
                if rest:
                    options.append((flag, rest))
                    index += 1
                elif index + 1 < len(words):
                    options.append((flag, words[index + 1]))
                    index += 2
                else:
                    options.append((flag, ""))
                    index += 1
                continue
            options.append((word, None))
            index += 1
            continue
        positionals.append(word)
        index += 1
    return positionals, options


def _ssh_walk(
    paths: Sequence[Path],
    host: Optional[str],
    user: Optional[str],
    command_sets_proxy: bool,
) -> Tuple[List[str], str, Optional[str]]:
    state = _SshScan(command_sets_proxy=command_sets_proxy)
    seen: set = set()
    for path in paths:
        _ssh_read(path, host, user, state, depth=0, seen=seen)
        if state.error:
            return [], "", state.error
    return state.execs, state.proxy, None


@dataclass
class _SshScan:
    command_sets_proxy: bool
    execs: List[str] = field(default_factory=list)
    proxy: str = ""
    proxy_set: bool = False
    error: str = ""
    files: int = 0


def _ssh_read(
    path: Path,
    host: Optional[str],
    user: Optional[str],
    state: _SshScan,
    *,
    depth: int,
    seen: set,
) -> None:
    if state.error:
        return
    key = os.path.normpath(str(path))
    if key in seen:
        return
    if depth > 4 or state.files >= _SSH_MAX_FILES:
        state.error = "ssh 配置 Include 层数过多，已拒绝执行"
        return
    text, err = _read_trusted(path)
    if err == "link":
        state.error = "ssh 配置是符号链接或硬链接，已拒绝执行"
        return
    if err:
        state.error = "ssh 配置无法作为普通文件读取，已拒绝执行"
        return
    if text is None:
        return
    seen.add(key)
    state.files += 1
    applies: Optional[bool] = True
    for line in _logical_lines(text):
        tokens = _ssh_tokens(line)
        if not tokens:
            continue
        keyword = tokens[0].lower()
        if keyword == "host":
            applies = _ssh_host_applies(tokens[1:], host)
            continue
        if keyword == "match":
            applies, ran = _ssh_match_line(tokens[1:], host, user)
            state.execs.extend(ran)
            continue
        if applies is False:
            continue
        if keyword == "include":
            _ssh_include(tokens[1:], path.parent, host, user, state, depth=depth, seen=seen)
            continue
        if keyword == "proxycommand" and not state.command_sets_proxy and not state.proxy_set:
            # The first obtained value wins. An unknown block might be that one.
            state.proxy_set = True
            state.proxy = " ".join(tokens[1:]).strip()


def _ssh_include(
    tokens: Sequence[str],
    base: Path,
    host: Optional[str],
    user: Optional[str],
    state: _SshScan,
    *,
    depth: int,
    seen: set,
) -> None:
    import glob

    if not tokens:
        return
    raw = os.path.expanduser(" ".join(tokens))
    pattern = raw if os.path.isabs(raw) else str(base / raw)
    matches = sorted(glob.glob(pattern))
    if matches:
        for item in matches:
            _ssh_read(Path(item), host, user, state, depth=depth + 1, seen=seen)
        return
    if any(mark in raw for mark in "*?["):
        return
    _ssh_read(Path(pattern), host, user, state, depth=depth + 1, seen=seen)


def _ssh_host_applies(patterns: Sequence[str], host: Optional[str]) -> Optional[bool]:
    if host is None:
        return None
    return _ssh_patterns_match(patterns, host)


def _ssh_patterns_match(patterns: Sequence[str], host: str) -> bool:
    import fnmatch

    positives: List[str] = []
    negatives: List[str] = []
    for token in patterns:
        for part in token.split(","):
            part = part.strip()
            if not part:
                continue
            if part.startswith("!"):
                negatives.append(part[1:])
            else:
                positives.append(part)
    host_name = host.lower()
    if any(fnmatch.fnmatch(host_name, item.lower()) for item in negatives):
        return False
    if not positives:
        return bool(negatives)
    return any(fnmatch.fnmatch(host_name, item.lower()) for item in positives)


def _ssh_match_line(
    tokens: Sequence[str], host: Optional[str], user: Optional[str],
) -> Tuple[Optional[bool], List[str]]:
    """Criteria left to right. A false test skips the rest of the line.

    `exec` runs when it is reached. Its exit status is not known here, so
    the block might still apply. An unknown criterion does not hide a later
    `exec`.
    """
    import getpass

    ran: List[str] = []
    matched: Optional[bool] = True
    index = 0
    while index < len(tokens):
        key = tokens[index].lower()
        if key == "all":
            index += 1
            continue
        if key in {"canonical", "final"}:
            index += 1
            if matched is not False:
                matched = None
            continue
        if index + 1 >= len(tokens):
            if matched is not False:
                matched = None
            break
        arg = tokens[index + 1]
        index += 2
        if matched is False:
            break
        if key == "exec":
            if arg.strip():
                ran.append(arg.strip())
            matched = None
            continue
        if key in {"host", "originalhost"}:
            if host is None:
                matched = None
            elif not _ssh_patterns_match([arg], host):
                matched = False
            continue
        if key == "user":
            if user is None:
                matched = None
            elif user.lower() != arg.lower():
                matched = False
            continue
        if key == "localuser":
            if arg.lower() != getpass.getuser().lower():
                matched = False
            continue
        if key not in _SSH_MATCH_ARG:
            matched = None
    return matched, ran


def _ssh_tokens(line: str) -> List[str]:
    """Words of one ssh_config line. `#` starts a comment outside quotes."""
    tokens: List[str] = []
    buf: List[str] = []
    quote = ""
    index = 0
    while index < len(line):
        char = line[index]
        if quote:
            if char == quote:
                quote = ""
            elif char == "\\" and index + 1 < len(line):
                buf.append(line[index + 1])
                index += 2
                continue
            else:
                buf.append(char)
            index += 1
            continue
        if char == "#":
            break
        if char in "'\"":
            quote = char
            index += 1
            continue
        if char.isspace():
            if buf:
                tokens.append("".join(buf))
                buf = []
            index += 1
            continue
        if char == "\\" and index + 1 < len(line):
            buf.append(line[index + 1])
            index += 2
            continue
        buf.append(char)
        index += 1
    if buf:
        tokens.append("".join(buf))
    return tokens

