"""Expand make, just, and tox recipes before the shell danger rules run.

`make test` is not itself a dangerous string. The recipe, the `$(shell ...)`
that runs while make reads the file, and the same idea for just and tox are
checked with the shell rules. `MAKEFILES` is read first, including a file
named by the command or the parent environment, but it does not become the
default goal. `MAKEFLAGS` and `GNUMAKEFLAGS` are extra arguments, so
`--eval=$(shell ...)` in either one runs while make starts. A command-line
`:=`, `::=`, or `!=` assignment runs then too, and so does `make -E`. A
symlink or hard link is not opened. A `-C`, `-f`, `--justfile`, or `-c`
path that leaves the workspace is refused.

The check is a bounded reader, not a full make implementation. It does expand
`$(call)`, `$(foreach)`, and `$(eval)` far enough to see a recipe those
functions write, and it applies a suffix rule when the prerequisite file is
already in the working directory. `$(wildcard)`, secondary expansion, and a
prerequisite that itself has to be built are not modeled. `$(shell)` inside a
recursive variable is checked when that variable is expanded into a recipe
that would run, and immediately when the assignment or `$(eval)` runs while
the file is read.
"""

from __future__ import annotations

import configparser
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, List, Optional, Sequence


_MAX_TEXT = 512_000
_MAX_FILES = 16
_MAKE_NAMES = ("GNUmakefile", "makefile", "Makefile")
_JUST_NAMES = ("justfile", "Justfile", ".justfile")
_MAKE_BINS = frozenset({"make", "gmake", "bmake"})
_ASSIGN_OP = ("::=", ":=", "?=", "+=", "!=")
_MAKE_RECURSE = re.compile(
    r"(?:\$\(\s*MAKE\s*\)|\$\{\s*MAKE\s*\}|\b(?:g?make|bmake)\b)\s+([A-Za-z0-9_./+-]+)"
)


@dataclass
class _Rule:
    prereqs: List[str] = field(default_factory=list)
    recipes: List[str] = field(default_factory=list)
    local: Dict[str, str] = field(default_factory=dict)


@dataclass
class _MakeFile:
    variables: Dict[str, str] = field(default_factory=dict)
    rules: Dict[str, _Rule] = field(default_factory=dict)
    patterns: List[tuple] = field(default_factory=list)
    includes: List[str] = field(default_factory=list)
    immediate_shells: List[str] = field(default_factory=list)
    suffix_ops: List[List[str]] = field(default_factory=list)
    default_goal: str = ""


# GNU make 4.3 built-in list. A makefile that never mentions `.SUFFIXES` still
# applies these. `.SUFFIXES:` with no prerequisites clears the list.
_DEFAULT_SUFFIXES = (
    ".out", ".a", ".ln", ".o", ".c", ".cc", ".C", ".cpp", ".p", ".f", ".F",
    ".m", ".r", ".y", ".l", ".ym", ".yl", ".s", ".S", ".mod", ".sym", ".def",
    ".h", ".info", ".dvi", ".tex", ".texinfo", ".texi", ".txinfo", ".w",
    ".ch", ".web", ".sh", ".elc", ".el",
)


def _owned_text(path: Path) -> Optional[str]:
    from tiancode.core.path_safety import is_shared_hardlink
    from tiancode.core.polyglot import _read_owned_text

    if path.is_symlink() or is_shared_hardlink(path):
        return None
    if not path.is_file():
        return None
    return _read_owned_text(path)


def _inside(root: Path, raw: str, base: Path) -> Optional[Path]:
    from tiancode.tools.execution import _directory_inside

    return _directory_inside(base, raw, root)


def _basename(token: str) -> str:
    name = Path(token).name.lower()
    if name.endswith(".exe"):
        name = name[:-4]
    return name


def _join_continuations(text: str) -> List[str]:
    logical: List[str] = []
    buf = ""
    for line in text.splitlines():
        if line.endswith("\\") and not line.endswith("\\\\"):
            buf += line[:-1] + " "
            continue
        logical.append(buf + line)
        buf = ""
    if buf:
        logical.append(buf)
    return logical


def _strip_make_comment(line: str) -> str:
    quote = ""
    index = 0
    while index < len(line):
        char = line[index]
        if quote:
            if char == quote:
                quote = ""
            index += 1
            continue
        if char in "'\"":
            quote = char
            index += 1
            continue
        if char == "\\" and index + 1 < len(line):
            index += 2
            continue
        if char == "#":
            return line[:index]
        index += 1
    return line


def _matching_closer(text: str, open_at: int, open_ch: str, close_ch: str) -> int:
    depth = 0
    for index in range(open_at, len(text)):
        if text[index] == open_ch:
            depth += 1
        elif text[index] == close_ch:
            depth -= 1
            if depth == 0:
                return index
    return -1


def _next_expansion(text: str, start: int) -> Optional[tuple]:
    """`(inner, end_index, dollar_index)` for the next `$(...)` or `${...}`."""
    index = start
    while True:
        dollar = text.find("$", index)
        if dollar < 0 or dollar + 1 >= len(text):
            return None
        opener = text[dollar + 1]
        if opener not in "({":
            index = dollar + 1
            continue
        closer = ")" if opener == "(" else "}"
        end = _matching_closer(text, dollar + 1, opener, closer)
        if end < 0:
            return None
        return text[dollar + 2 : end], end, dollar


def _split_func(inner: str) -> tuple:
    parts = inner.strip().split(None, 1)
    if not parts:
        return "", ""
    return parts[0], parts[1] if len(parts) > 1 else ""


def _split_make_args(text: str) -> List[str]:
    """Split make function arguments on commas that are not inside a nested call."""
    args: List[str] = []
    buf: List[str] = []
    paren = 0
    brace = 0
    for char in text:
        if char == "(":
            paren += 1
        elif char == ")":
            paren = max(0, paren - 1)
        elif char == "{":
            brace += 1
        elif char == "}":
            brace = max(0, brace - 1)
        elif char == "," and paren == 0 and brace == 0:
            args.append("".join(buf))
            buf = []
            continue
        buf.append(char)
    args.append("".join(buf))
    return args


def _shell_bodies(text: str) -> List[str]:
    """Every `$(shell ...)` / `${shell ...}`, including one nested inside another function."""
    bodies: List[str] = []
    index = 0
    while True:
        found = _next_expansion(text, index)
        if found is None:
            break
        inner, end, _dollar = found
        name, rest = _split_func(inner)
        if name == "shell":
            command = rest.strip()
            if command:
                bodies.append(command)
            bodies.extend(_shell_bodies(rest))
        else:
            bodies.extend(_shell_bodies(inner))
        index = end + 1
    return bodies


def _expand_make(
    text: str,
    variables: Dict[str, str],
    depth: int = 0,
    *,
    read: bool = False,
    parsed: Optional["_MakeFile"] = None,
    pending: Optional[List[str]] = None,
) -> str:
    """Expand variables, `call`, `foreach`, and `if`.

    `read` is for text make expands while reading the file. `$(shell)` is
    recorded and removed, and `$(eval)` is parsed as more makefile. A recipe
    leaves `$(shell)` in the text so the danger check can see the command
    without running it.
    """
    if depth > 12 or "$" not in text:
        return text
    text = text.replace("$$", "\0")
    out: List[str] = []
    index = 0
    while True:
        found = _next_expansion(text, index)
        if found is None:
            out.append(text[index:])
            break
        inner, end, dollar = found
        out.append(text[index:dollar])
        out.append(
            _expand_function(
                inner, variables, depth, read=read, parsed=parsed, pending=pending,
            )
        )
        index = end + 1
    return "".join(out).replace("\0", "$")


def _expand_function(
    inner: str,
    variables: Dict[str, str],
    depth: int,
    *,
    read: bool,
    parsed: Optional["_MakeFile"],
    pending: Optional[List[str]],
) -> str:
    name, rest = _split_func(inner)
    if not name:
        return ""
    if name == "shell":
        if read and parsed is not None:
            command = rest.strip()
            if command:
                parsed.immediate_shells.append(command)
            parsed.immediate_shells.extend(_shell_bodies(rest))
            return ""
        return "$(" + inner + ")"
    if name == "eval" and read and pending is not None:
        body = _expand_make(rest, variables, depth + 1, read=True, parsed=parsed, pending=pending)
        pending.extend(_join_continuations(body))
        return ""
    if name == "call":
        return _expand_call(rest, variables, depth, read=read, parsed=parsed, pending=pending)
    if name == "foreach":
        return _expand_foreach(rest, variables, depth, read=read, parsed=parsed, pending=pending)
    if name == "if":
        return _expand_if(rest, variables, depth, read=read, parsed=parsed, pending=pending)
    if name == "strip":
        expanded = _expand_make(rest, variables, depth + 1, read=read, parsed=parsed, pending=pending)
        return " ".join(expanded.split())
    if name == "value":
        return variables.get(rest.strip(), "")
    if name in {"origin", "wildcard", "eval", "file", "flavor", "error", "warning", "info"}:
        if read:
            return ""
        return "$(" + inner + ")"
    if name in variables:
        return _expand_make(variables[name], variables, depth + 1, read=read, parsed=parsed, pending=pending)
    if read:
        return ""
    return "$(" + inner + ")"


def _expand_call(
    rest: str,
    variables: Dict[str, str],
    depth: int,
    *,
    read: bool,
    parsed: Optional["_MakeFile"],
    pending: Optional[List[str]],
) -> str:
    parts = _split_make_args(rest)
    if not parts or not parts[0].strip():
        return ""
    name = _expand_make(parts[0], variables, depth + 1, read=read, parsed=parsed, pending=pending).strip()
    args = [
        _expand_make(part, variables, depth + 1, read=read, parsed=parsed, pending=pending)
        for part in parts[1:]
    ]
    template = variables.get(name)
    if template is None:
        return ""
    local = dict(variables)
    local["0"] = name
    for index, arg in enumerate(args, start=1):
        local[str(index)] = arg
    return _expand_make(template, local, depth + 1, read=read, parsed=parsed, pending=pending)


def _expand_foreach(
    rest: str,
    variables: Dict[str, str],
    depth: int,
    *,
    read: bool,
    parsed: Optional["_MakeFile"],
    pending: Optional[List[str]],
) -> str:
    parts = _split_make_args(rest)
    if len(parts) < 3:
        return ""
    var = parts[0].strip()
    words = _expand_make(parts[1], variables, depth + 1, read=read, parsed=parsed, pending=pending).split()
    chunks: List[str] = []
    for word in words:
        local = dict(variables)
        local[var] = word
        chunks.append(_expand_make(parts[2], local, depth + 1, read=read, parsed=parsed, pending=pending))
    return " ".join(chunks)


def _expand_if(
    rest: str,
    variables: Dict[str, str],
    depth: int,
    *,
    read: bool,
    parsed: Optional["_MakeFile"],
    pending: Optional[List[str]],
) -> str:
    parts = _split_make_args(rest)
    if not parts:
        return ""
    cond = _expand_make(parts[0], variables, depth + 1, read=read, parsed=parsed, pending=pending).strip()
    branch = parts[1] if cond and len(parts) > 1 else (parts[2] if not cond and len(parts) > 2 else "")
    return _expand_make(branch, variables, depth + 1, read=read, parsed=parsed, pending=pending)


def _only_expansions(line: str) -> bool:
    """True when the line is only make functions, the shape of a bare `$(eval ...)`."""
    text = line.strip()
    if not text.startswith("$"):
        return False
    index = 0
    saw = False
    while index < len(text):
        if text[index].isspace():
            index += 1
            continue
        found = _next_expansion(text, index)
        if found is None:
            return False
        _inner, end, dollar = found
        if dollar != index:
            return False
        saw = True
        index = end + 1
    return saw


def _assignment(line: str) -> Optional[tuple]:
    stripped = line.strip()
    for prefix in ("override ", "export "):
        if stripped.startswith(prefix):
            stripped = stripped[len(prefix) :].strip()
    for op in _ASSIGN_OP:
        slot = stripped.find(op)
        if slot <= 0:
            continue
        name = stripped[:slot].strip()
        if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name):
            return name, op, stripped[slot + len(op) :].strip()
    slot = stripped.find("=")
    if slot <= 0:
        return None
    name = stripped[:slot].strip()
    if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name):
        return name, "=", stripped[slot + 1 :].strip()
    return None


def _split_rule(line: str) -> Optional[tuple]:
    """Return targets, remainder after the first unquoted single colon."""
    quote = ""
    index = 0
    while index < len(line):
        char = line[index]
        if quote:
            if char == quote:
                quote = ""
            index += 1
            continue
        if char in "'\"":
            quote = char
        elif char == "\\" and index + 1 < len(line):
            index += 2
            continue
        elif char == ":":
            if line.startswith(":=", index) or line.startswith("::=", index):
                return None
            targets = line[:index].strip()
            rest = line[index + 1 :]
            if rest.startswith(":"):
                rest = rest[1:]
            if targets:
                return targets, rest.strip()
            return None
        index += 1
    return None


def _parse_make_lines(lines: Sequence[str], parsed: _MakeFile) -> None:
    pending: List[str] = list(lines)
    depth = 0
    while pending and depth < 4:
        depth += 1
        current = pending
        pending = []
        rule_targets: List[str] = []
        in_define = ""
        define_lines: List[str] = []
        define_op = "="
        for raw_line in current:
            if in_define:
                if raw_line.strip() == "endef":
                    parsed.variables[in_define] = "\n".join(define_lines)
                    if define_op in {":=", "::=", "!="}:
                        parsed.immediate_shells.extend(_shell_bodies(parsed.variables[in_define]))
                    in_define = ""
                    define_lines = []
                else:
                    define_lines.append(raw_line)
                continue
            if raw_line.startswith("\t") and rule_targets:
                recipe = raw_line[1:]
                for target in rule_targets:
                    parsed.rules.setdefault(target, _Rule()).recipes.append(recipe)
                continue
            line = _strip_make_comment(raw_line).strip()
            if not line:
                rule_targets = []
                continue
            # A bare `$(eval ...)` or `$(foreach ... $(eval ...))` runs while
            # make reads the file. The generated lines are parsed next.
            if _only_expansions(line):
                _expand_make(line, parsed.variables, read=True, parsed=parsed, pending=pending)
                rule_targets = []
                continue
            if line.startswith("define ") and line.endswith("endef") is False:
                header = line[len("define ") :].strip()
                define_op = "="
                name = header
                for op in _ASSIGN_OP + ("=",):
                    if header.endswith(op) or f" {op}" in header:
                        name = header.split(op, 1)[0].strip()
                        define_op = op if op != "=" or header.find("=") >= 0 else "="
                        break
                in_define = name.split()[0] if name else ""
                rule_targets = []
                continue
            included = False
            for prefix in ("include ", "-include ", "sinclude "):
                if line.startswith(prefix):
                    parsed.includes.extend(line[len(prefix) :].split())
                    included = True
                    break
            if included:
                rule_targets = []
                continue
            assigned = _assignment(line)
            rule = _split_rule(line) if assigned is None else None
            if rule is not None:
                target_text, rest = rule
                specific = _assignment(rest)
                targets = target_text.split()
                if specific is not None:
                    name, op, value = specific
                    if op == "!=":
                        value = _expand_make(value, parsed.variables)
                        parsed.immediate_shells.append(value.strip())
                        parsed.immediate_shells.extend(_shell_bodies(value))
                    elif op in {":=", "::="}:
                        value = _expand_make(
                            value, parsed.variables, read=True, parsed=parsed, pending=pending,
                        )
                    for target in targets:
                        parsed.rules.setdefault(target, _Rule()).local[name] = value
                    rule_targets = []
                    continue
                inline = ""
                prereq_text = rest
                if ";" in rest:
                    prereq_text, _, inline = rest.partition(";")
                prereqs = prereq_text.split()
                if ".SUFFIXES" in targets:
                    # Empty prerequisites clear the list, including the defaults.
                    # A later `.SUFFIXES: .foo` adds to whatever remains.
                    parsed.suffix_ops.append(prereqs)
                for target in targets:
                    item = parsed.rules.setdefault(target, _Rule())
                    item.prereqs = prereqs
                    if inline.strip():
                        item.recipes.append(inline.strip())
                    if "%" in target:
                        parsed.patterns.append((target, item))
                    elif not parsed.default_goal and not target.startswith("."):
                        parsed.default_goal = target
                rule_targets = [target for target in targets if "%" not in target]
                continue
            if assigned is not None:
                name, op, value = assigned
                if op == "!=":
                    value = _expand_make(value, parsed.variables)
                    parsed.immediate_shells.append(value.strip())
                    parsed.immediate_shells.extend(_shell_bodies(value))
                elif op in {":=", "::="}:
                    value = _expand_make(
                        value, parsed.variables, read=True, parsed=parsed, pending=pending,
                    )
                elif op == "+=":
                    value = (parsed.variables.get(name, "") + " " + value).strip()
                elif op == "?=" and name in parsed.variables:
                    rule_targets = []
                    continue
                parsed.variables[name] = value
                rule_targets = []
                continue
            rule_targets = []


def _load_make(
    path: Path, root: Path, seen: set, parsed: _MakeFile, *, keep_goal: bool = False,
) -> Optional[str]:
    """Read one makefile. `keep_goal` leaves the default target unchanged.

    GNU make reads `MAKEFILES` first and never takes the default goal from
    those files. Their rules still apply when a later target uses them.
    """
    goal = parsed.default_goal
    try:
        return _load_make_body(path, root, seen, parsed, keep_goal=keep_goal)
    finally:
        if keep_goal:
            parsed.default_goal = goal


def _load_make_body(
    path: Path, root: Path, seen: set, parsed: _MakeFile, *, keep_goal: bool,
) -> Optional[str]:
    if len(seen) >= _MAX_FILES:
        return "Makefile 引用过多，无法在执行前检查配方，已拒绝执行"
    key = str(path)
    if key in seen:
        return None
    seen.add(key)
    if path.is_symlink():
        return "Makefile 是符号链接或硬链接，已拒绝执行"
    from tiancode.core.path_safety import is_shared_hardlink

    if is_shared_hardlink(path):
        return "Makefile 是符号链接或硬链接，已拒绝执行"
    text = _owned_text(path)
    if text is None:
        return "Makefile 无法作为普通文件读取，已拒绝执行"
    if len(text) > _MAX_TEXT:
        return "Makefile 过大，无法在执行前检查配方，已拒绝执行"
    before = list(parsed.includes)
    _parse_make_lines(_join_continuations(text), parsed)
    base = path.parent
    for raw in parsed.includes[len(before) :]:
        dest = _inside(root, raw, base)
        if dest is None:
            return "Makefile 引入了工作区外的文件，已拒绝执行"
        if not dest.exists():
            continue
        reason = _load_make(dest, root, seen, parsed, keep_goal=keep_goal)
        if reason:
            return reason
    return None


def _stated_path(root: Path, raw: str, base: Path) -> tuple:
    """A makefile path that is not resolved through its final symlink."""
    text = raw.strip().strip("'\"")
    if not text or text.startswith("-"):
        return None, "make 的 MAKEFILES 路径无法确认，已拒绝执行"
    path = Path(text)
    dest = path if path.is_absolute() else base / path
    try:
        parent = dest.parent.resolve()
        parent.relative_to(root)
    except (OSError, ValueError):
        return None, "make 的 MAKEFILES 越出工作区，已拒绝执行"
    final = parent / dest.name
    if final.is_symlink():
        try:
            final.resolve().relative_to(root)
        except (OSError, ValueError):
            return None, "make 的 MAKEFILES 越出工作区，已拒绝执行"
    try:
        final.relative_to(root)
    except ValueError:
        return None, "make 的 MAKEFILES 越出工作区，已拒绝执行"
    return final, None


def _pattern_rule(parsed: _MakeFile, target: str) -> Optional[_Rule]:
    for pattern, rule in parsed.patterns:
        if "%" not in pattern or not _has_recipe(rule):
            continue
        prefix, _, suffix = pattern.partition("%")
        if target.startswith(prefix) and target.endswith(suffix) and len(target) >= len(prefix) + len(suffix):
            return rule
    return None


def _has_recipe(rule: Optional[_Rule]) -> bool:
    return rule is not None and any(_recipe_text(line) for line in rule.recipes)


def _active_suffixes(parsed: _MakeFile) -> List[str]:
    if not parsed.suffix_ops:
        return list(_DEFAULT_SUFFIXES)
    suffixes = list(_DEFAULT_SUFFIXES)
    for op in parsed.suffix_ops:
        if not op:
            suffixes = []
            continue
        for item in op:
            if item not in suffixes:
                suffixes.append(item)
    return suffixes


def _prereq_present(directory: Path, name: str) -> bool:
    """True when make would find this prerequisite. The file is not opened."""
    path = directory / name
    return path.is_symlink() or path.exists()


def _suffix_rule(parsed: _MakeFile, target: str, directory: Path) -> Optional[_Rule]:
    """A suffix rule make would run because its prerequisite is already on disk.

    A file that still has to be built is not chased. The longest target suffix
    wins, then prerequisite suffixes follow the suffix list. This is the part
    of GNU make's search that decides the cases covered by the tests.
    """
    suffixes = _active_suffixes(parsed)
    matching = [suffix for suffix in suffixes if suffix and target.endswith(suffix) and target != suffix]
    matching.sort(key=len, reverse=True)
    for target_suffix in matching:
        stem = target[: -len(target_suffix)]
        if not stem:
            continue
        for source_suffix in suffixes:
            rule = parsed.rules.get(source_suffix + target_suffix)
            if not _has_recipe(rule):
                continue
            if _prereq_present(directory, stem + source_suffix):
                return rule
    for source_suffix in suffixes:
        rule = parsed.rules.get(source_suffix)
        # The key is the suffix itself (`.sh:`), so `.c.o` is not selected here.
        if rule is None or not _has_recipe(rule):
            continue
        if _prereq_present(directory, target + source_suffix):
            return rule
    return None


def _recipe_text(line: str) -> str:
    text = line.strip()
    while text[:1] in "@+-":
        text = text[1:].lstrip()
    return text


def _dangerous_recipe(text: str) -> bool:
    from tiancode.tools.execution import is_dangerous_command

    command = _recipe_text(text)
    if not command:
        return False
    if is_dangerous_command(command):
        return True
    return any(is_dangerous_command(body) for body in _shell_bodies(command))


def _check_make_targets(
    parsed: _MakeFile,
    targets: Sequence[str],
    *,
    run_recipes: bool,
    directory: Path,
) -> Optional[str]:
    from tiancode.tools.execution import is_dangerous_command

    for body in parsed.immediate_shells:
        if is_dangerous_command(body):
            return "Makefile 在读取时会执行高危 $(shell)，已拒绝执行"
    if not run_recipes:
        return None
    pending = [target for target in targets if target]
    seen: set = set()
    steps = 0
    while pending and steps < 64:
        steps += 1
        target = pending.pop(0)
        if target in seen:
            continue
        seen.add(target)
        explicit = parsed.rules.get(target)
        recipe_rule = explicit if _has_recipe(explicit) else _pattern_rule(parsed, target)
        if recipe_rule is None:
            recipe_rule = _suffix_rule(parsed, target, directory)
        rule = explicit or recipe_rule
        if rule is None:
            continue
        if recipe_rule is not None:
            variables = {**parsed.variables, **rule.local, **recipe_rule.local}
            for recipe in recipe_rule.recipes:
                expanded = _expand_make(recipe, variables)
                if _dangerous_recipe(expanded):
                    return f"Makefile 目标 {target} 的配方含高危命令，已拒绝执行"
                for match in _MAKE_RECURSE.finditer(expanded):
                    pending.append(match.group(1))
        pending.extend(item for item in rule.prereqs if "%" not in item)
    return None


@dataclass
class _MakeInvoke:
    directory: Path
    files: List[str]
    targets: List[str]
    dry_run: bool
    evals: List[str]
    overrides: Dict[str, str]


def _parse_make_argv(words: Sequence[str], cwd: Path, root: Path) -> tuple:
    directory = cwd
    files: List[str] = []
    targets: List[str] = []
    dry_run = False
    evals: List[str] = []
    overrides: Dict[str, str] = {}
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            targets.extend(words[index + 1 :])
            break
        assigned = _command_assignment(word)
        if assigned is not None:
            name, op, value = assigned
            # `:=` and `!=` run while make reads the command line, before any
            # target. A recursive `=` waits until a recipe expands it.
            if op in {":=", "::=", "!="}:
                evals.append(f"{name}{op}{value}")
            else:
                overrides[name] = value
            index += 1
            continue
        if word.startswith("--"):
            name, eq, value = word.partition("=")
            arg = value if eq else ""
            takes = name in {
                "--directory", "--file", "--makefile", "--include-dir", "--eval",
                "--old-file", "--assume-old", "--what-if", "--new-file", "--assume-new",
            }
            if takes and not eq:
                index += 1
                arg = words[index] if index < len(words) else ""
            if name in {"--directory"}:
                dest = _inside(root, arg, directory)
                if dest is None:
                    return None, "make 的 -C 或 -f 越出工作区，已拒绝执行"
                directory = dest
            elif name in {"--file", "--makefile"}:
                if arg == "-":
                    return None, "Makefile 来自标准输入，已拒绝执行"
                files.append(arg)
            elif name == "--eval":
                evals.append(_unquote_make_word(arg))
            elif name in {"--just-print", "--dry-run", "--recon", "--question"}:
                dry_run = True
            index += 1
            continue
        if word.startswith("-") and word != "-":
            cluster = word[1:]
            cursor = 0
            while cursor < len(cluster):
                flag = cluster[cursor]
                rest = cluster[cursor + 1 :]
                if flag in "CfoWEIj":
                    arg = rest
                    if not arg and flag != "j":
                        index += 1
                        arg = words[index] if index < len(words) else ""
                    arg = _unquote_make_word(arg)
                    if flag == "C":
                        dest = _inside(root, arg, directory)
                        if dest is None:
                            return None, "make 的 -C 或 -f 越出工作区，已拒绝执行"
                        directory = dest
                    elif flag == "f":
                        if arg == "-":
                            return None, "Makefile 来自标准输入，已拒绝执行"
                        files.append(arg)
                    elif flag == "E":
                        evals.append(arg)
                    elif flag == "j" and arg and not arg.isdigit() and not rest:
                        pass
                    break
                if flag in "nqt":
                    dry_run = True
                cursor += 1
            index += 1
            continue
        targets.append(word)
        index += 1
    return _MakeInvoke(directory, files, targets, dry_run, evals, overrides), None


def _command_assignment(word: str) -> Optional[tuple]:
    """`(name, operator, value)` for `X=`, `X:=`, `X::=`, `X!=`, `X+=`, or `X?=`."""
    if word.startswith("-"):
        return None
    for op in ("::=", ":=", "!=", "?=", "+=", "="):
        slot = word.find(op)
        if slot <= 0:
            continue
        name = word[:slot]
        if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name):
            return name, op, word[slot + len(op) :]
    return None


def _unquote_make_word(word: str) -> str:
    if len(word) >= 2 and word[0] == word[-1] and word[0] in "'\"":
        return word[1:-1]
    return word


def _split_make_flag_words(text: str) -> List[str]:
    """Split MAKEFLAGS without cutting a `$(...)` that contains spaces."""
    words: List[str] = []
    buf: List[str] = []
    depth = 0
    quote = ""
    for char in text:
        if quote:
            buf.append(char)
            if char == quote:
                quote = ""
            continue
        if char in "'\"":
            quote = char
            buf.append(char)
            continue
        if char == "$" and not quote:
            buf.append(char)
            continue
        if char in "({" and buf and buf[-1] == "$":
            depth += 1
            buf.append(char)
            continue
        if char in ")}" and depth:
            depth -= 1
            buf.append(char)
            continue
        if char.isspace() and depth == 0:
            if buf:
                words.append(_unquote_make_word("".join(buf)))
                buf = []
            continue
        buf.append(char)
    if buf:
        words.append(_unquote_make_word("".join(buf)))
    return words


def _assignment_word(word: str) -> bool:
    return _command_assignment(word) is not None


def _make_env_assignment(word: str, literal) -> Optional[tuple]:
    """`(name, value)` for a prefix assignment make will inherit.

    A value with `$(...)` is not a shell assignment the rest of the command
    expands, but make still reads `MAKEFLAGS` and `MAKEFILES` from it.
    """
    parsed = literal(word)
    if parsed is not None:
        return parsed
    name, sep, raw = word.partition("=")
    if not sep or name not in {"MAKEFLAGS", "GNUMAKEFLAGS", "MAKEFILES"}:
        return None
    if len(raw) >= 2 and raw[0] == raw[-1] and raw[0] in "'\"":
        raw = raw[1:-1]
    return name, raw


def _env_workdir(words: Sequence[str], cwd: Path, root: Path) -> tuple:
    """Directory `env -C` / `--chdir` uses for the command that follows.

    `(cwd, None)` when env does not change directory. An outside directory
    is refused, the same way `make -C /tmp` is. `-u` and `-S` take a value,
    so `env -u FOO -C build make` still sees `build`.
    """
    from tiancode.tools.execution import _is_assignment_word

    index = 0
    while index < len(words) and _is_assignment_word(words[index]):
        index += 1
    if index >= len(words) or _basename(words[index]) != "env":
        return cwd, None
    index += 1
    found: Optional[str] = None
    while index < len(words):
        word = words[index]
        if word == "--":
            break
        name, eq, value = word.partition("=")
        glued_dir = word.startswith("-C") and not word.startswith("--") and word != "-C"
        if name in {"-C", "--chdir"} or glued_dir:
            if glued_dir:
                found = word[2:]
            elif eq:
                found = value
            elif index + 1 < len(words):
                index += 1
                found = words[index]
            else:
                found = ""
            index += 1
            continue
        glued_split = word.startswith("-S") and not word.startswith("--") and word != "-S"
        if name in {"-u", "--unset", "-S", "--split-string"} or glued_split:
            if not eq and not glued_split and index + 1 < len(words):
                index += 1
            index += 1
            continue
        if not word.startswith("-"):
            break
        index += 1
    if found is None:
        return cwd, None
    dest = _inside(root, _unquote_make_word(found), cwd)
    if dest is None:
        return cwd, "env -C 越出工作区，已拒绝执行"
    return dest, None


def _absorb_make_flags(text: str, invoke: _MakeInvoke, cwd: Path, root: Path) -> Optional[str]:
    """Merge options from MAKEFLAGS. A nested MAKEFLAGS value is not read again."""
    words = _split_make_flag_words(text)
    if not words:
        return None
    extra, reason = _parse_make_argv(["make", *words], cwd, root)
    if reason:
        return reason
    if extra is None:
        return None
    invoke.evals.extend(extra.evals)
    for name, value in extra.overrides.items():
        if name in {"MAKEFLAGS", "GNUMAKEFLAGS", "MAKEFILES"}:
            continue
        invoke.overrides.setdefault(name, value)
    for raw in extra.files:
        if raw not in invoke.files:
            invoke.files.append(raw)
    if extra.dry_run:
        invoke.dry_run = True
    if extra.targets:
        invoke.targets.extend(target for target in extra.targets if target not in invoke.targets)
    return None


def _make_block(
    words: Sequence[str],
    cwd: Path,
    root: Path,
    inherited: str = "",
    makeflags: str = "",
    gnumakeflags: str = "",
) -> Optional[str]:
    invoke, reason = _parse_make_argv(words, cwd, root)
    if reason:
        return reason
    if invoke is None:
        return None
    # GNU make reads GNUMAKEFLAGS and then MAKEFLAGS before the makefile.
    # `--eval=$(shell ...)` in either one runs even when no target exists.
    for text in (gnumakeflags, makeflags):
        reason = _absorb_make_flags(text, invoke, cwd, root)
        if reason:
            return reason
    # A command-line `make MAKEFILES=b.mk` replaces the environment value,
    # including when the command-line value is empty.
    extra = invoke.overrides["MAKEFILES"] if "MAKEFILES" in invoke.overrides else inherited
    chosen: List[Path] = []
    if invoke.files:
        for raw in invoke.files:
            dest = _inside(root, raw, invoke.directory)
            if dest is None:
                return "make 的 -C 或 -f 越出工作区，已拒绝执行"
            chosen.append(dest)
    else:
        for name in _MAKE_NAMES:
            candidate = invoke.directory / name
            if candidate.exists() or candidate.is_symlink():
                chosen.append(candidate)
                break
    parsed = _MakeFile()
    parsed.variables.update(invoke.overrides)
    if invoke.evals:
        _parse_make_lines(invoke.evals, parsed)
    # Not the same set as the main makefile: `MAKEFILES=Makefile` is read
    # again below, and that second read is what sets the default goal.
    extra_seen: set = set()
    for raw in extra.split():
        if not raw:
            continue
        path, error = _stated_path(root, raw, invoke.directory)
        if error:
            return error
        if path is None or (not path.exists() and not path.is_symlink()):
            continue
        reason = _load_make(path, root, extra_seen, parsed, keep_goal=True)
        if reason:
            return reason
    # A recipe in the main makefile replaces one from MAKEFILES. Make runs
    # the later recipe and warns about the earlier one.
    prior = {name: len(rule.recipes) for name, rule in parsed.rules.items()}
    seen: set = set()
    for path in chosen:
        reason = _load_make(path, root, seen, parsed)
        if reason:
            return reason
    for name, rule in parsed.rules.items():
        old = prior.get(name, 0)
        if old and len(rule.recipes) > old:
            rule.recipes = rule.recipes[old:]
    if not chosen and not parsed.immediate_shells and not any(rule.recipes for rule in parsed.rules.values()):
        return None
    targets = invoke.targets or ([parsed.default_goal] if parsed.default_goal else [])
    return _check_make_targets(
        parsed, targets, run_recipes=not invoke.dry_run, directory=invoke.directory,
    )


@dataclass
class _JustRecipe:
    deps: List[str]
    lines: List[str]


def _parse_just(text: str) -> tuple:
    recipes: Dict[str, _JustRecipe] = {}
    variables: Dict[str, str] = {}
    imports: List[str] = []
    default = ""
    lines = text.splitlines()
    index = 0
    while index < len(lines):
        raw = lines[index]
        stripped = raw.split("#", 1)[0].rstrip() if not raw.startswith((" ", "\t")) else raw.rstrip()
        if not stripped.strip() or stripped.strip().startswith("#") or stripped.strip().startswith("["):
            index += 1
            continue
        header = stripped.strip()
        if header.startswith("import ") or header.startswith("mod "):
            match = re.search(r"""['"]([^'"]+)['"]""", header)
            if match:
                imports.append(match.group(1))
            index += 1
            continue
        if ":=" in header and not header.split(":=")[0].strip().endswith(":"):
            name, _, value = header.partition(":=")
            variables[name.strip().split()[-1]] = value.strip().strip("'\"")
            index += 1
            continue
        if ":" not in header:
            index += 1
            continue
        left, _, right = header.partition(":")
        name = left.split()[0]
        deps = re.findall(r"[A-Za-z_][A-Za-z0-9_-]*", right)
        body: List[str] = []
        index += 1
        while index < len(lines) and (lines[index].startswith((" ", "\t")) or not lines[index].strip()):
            if lines[index].strip():
                body.append(lines[index].strip())
            index += 1
        recipes[name] = _JustRecipe(deps, body)
        if not default:
            default = name
    return recipes, variables, imports, default


def _expand_just(text: str, variables: Dict[str, str]) -> str:
    def repl(match: re.Match[str]) -> str:
        return variables.get(match.group(1), match.group(0))

    return re.sub(r"\{\{\s*([A-Za-z_][A-Za-z0-9_-]*)\s*\}\}", repl, text)


def _merge_just(
    path: Path,
    root: Path,
    seen: set,
    recipes: Dict[str, _JustRecipe],
    variables: Dict[str, str],
) -> tuple:
    """Load one justfile and its in-workspace imports. Returns (default, error)."""
    if len(seen) > 8:
        return "", "justfile 引用过多，无法在执行前检查配方，已拒绝执行"
    key = str(path.resolve()) if path.exists() else str(path)
    if key in seen:
        return "", None
    seen.add(key)
    from tiancode.core.path_safety import is_shared_hardlink

    if path.is_symlink() or is_shared_hardlink(path):
        return "", "justfile 是符号链接或硬链接，已拒绝执行"
    text = _owned_text(path)
    if text is None:
        return "", "justfile 无法作为普通文件读取，已拒绝执行"
    if len(text) > _MAX_TEXT:
        return "", "justfile 过大，无法在执行前检查配方，已拒绝执行"
    found, assigns, imports, default = _parse_just(text)
    for name, value in assigns.items():
        variables.setdefault(name, value)
    for name, item in found.items():
        recipes.setdefault(name, item)
    for raw in imports:
        dest = _inside(root, raw, path.parent)
        if dest is None:
            return "", "justfile 引入了工作区外的文件，已拒绝执行"
        if not dest.exists() and not dest.is_symlink():
            continue
        _imported_default, reason = _merge_just(dest, root, seen, recipes, variables)
        if reason:
            return "", reason
        if not default:
            default = _imported_default
    return default, None


def _just_block(words: Sequence[str], cwd: Path, root: Path) -> Optional[str]:
    from tiancode.tools.execution import is_dangerous_command

    directory = cwd
    chosen = ""
    recipe = ""
    query = False
    sets: Dict[str, str] = {}
    index = 1
    while index < len(words):
        word = words[index]
        if word in {"--list", "-l", "--summary", "--show", "--dump", "--edit", "--variables",
                    "--groups", "--completions", "--help", "-h", "--fmt", "--changelog", "--choose"}:
            query = True
        if word in {"-f", "--justfile"}:
            index += 1
            chosen = words[index] if index < len(words) else ""
        elif word.startswith("--justfile="):
            chosen = word.split("=", 1)[1]
        elif word in {"-d", "--working-directory"}:
            index += 1
            arg = words[index] if index < len(words) else ""
            dest = _inside(root, arg, directory)
            if dest is None:
                return "just 的工作目录越出工作区，已拒绝执行"
            directory = dest
        elif word.startswith("--working-directory="):
            dest = _inside(root, word.split("=", 1)[1], directory)
            if dest is None:
                return "just 的工作目录越出工作区，已拒绝执行"
            directory = dest
        elif word == "--set" and index + 2 < len(words):
            sets[words[index + 1]] = words[index + 2]
            index += 2
        elif word == "--":
            if not recipe and index + 1 < len(words):
                recipe = words[index + 1]
            break
        elif not word.startswith("-") and not recipe:
            recipe = word
        index += 1
    if query:
        return None
    path = directory / chosen if chosen else None
    if path is None:
        for name in _JUST_NAMES:
            candidate = directory / name
            if candidate.exists() or candidate.is_symlink():
                path = candidate
                break
    else:
        dest = _inside(root, chosen, directory)
        if dest is None:
            return "justfile 越出工作区，已拒绝执行"
        path = dest
    if path is None:
        return None
    recipes: Dict[str, _JustRecipe] = {}
    variables = dict(sets)
    default, reason = _merge_just(path, root, set(), recipes, variables)
    if reason:
        return reason
    names = [recipe] if recipe else ([default] if default else [])
    if recipe and recipe not in recipes:
        names = list(recipes)
    seen: set = set()
    pending = list(names)
    while pending:
        name = pending.pop(0)
        if name in seen:
            continue
        seen.add(name)
        item = recipes.get(name)
        if item is None:
            continue
        for line in item.lines:
            expanded = _expand_just(line, variables)
            if expanded.startswith("#!"):
                continue
            if is_dangerous_command(expanded):
                return f"just 配方 {name} 含高危命令，已拒绝执行"
        pending.extend(item.deps)
    return None


def _split_tox_commands(value: str) -> List[str]:
    commands = []
    for line in value.splitlines():
        text = line.strip()
        if not text or text.startswith("#"):
            continue
        if text.startswith("- "):
            text = text[2:].strip()
        elif text[:1] == "-" and not text.startswith("--"):
            text = text[1:].strip()
        if text:
            commands.append(text)
    return commands


def _tox_commands(parser: configparser.ConfigParser, env: str) -> List[str]:
    section = "testenv" if env in {"testenv", ""} else f"testenv:{env}"
    commands: List[str] = []
    for key in ("commands_pre", "commands", "commands_post"):
        source = section if parser.has_section(section) and parser.has_option(section, key) else "testenv"
        if parser.has_section(source) and parser.has_option(source, key):
            commands.extend(_split_tox_commands(parser.get(source, key)))
    return commands


def _read_tox_parser(path: Path) -> tuple:
    if path.suffix == ".toml" or path.name == "pyproject.toml":
        return _tox_from_toml(path)
    parser = configparser.ConfigParser(interpolation=None, strict=False)
    parser.optionxform = str
    try:
        parser.read_string(_owned_text(path) or "")
    except configparser.Error:
        return None, "tox 配置无法解析，已拒绝执行"
    return parser, None


def _tox_from_toml(path: Path) -> tuple:
    import tomllib

    raw = _owned_text(path)
    if raw is None:
        return None, "tox 配置无法作为普通文件读取，已拒绝执行"
    try:
        data = tomllib.loads(raw)
    except tomllib.TOMLDecodeError:
        return None, "tox 配置无法解析，已拒绝执行"
    tool = data.get("tool", {}).get("tox", {}) if isinstance(data, dict) else {}
    if not isinstance(tool, dict):
        return None, None
    legacy = tool.get("legacy_tox_ini")
    parser = configparser.ConfigParser(interpolation=None, strict=False)
    parser.optionxform = str
    if isinstance(legacy, str) and legacy.strip():
        try:
            parser.read_string(legacy)
        except configparser.Error:
            return None, "tox 配置无法解析，已拒绝执行"
        return parser, None
    parser.add_section("tox")
    env_list = tool.get("env_list") or tool.get("envlist") or []
    if isinstance(env_list, list):
        parser.set("tox", "envlist", " ".join(str(item) for item in env_list))
    envs = tool.get("env") if isinstance(tool.get("env"), dict) else {}
    base = tool.get("env_run_base") if isinstance(tool.get("env_run_base"), dict) else {}
    if base:
        parser.add_section("testenv")
        for key in ("commands", "commands_pre", "commands_post"):
            if key in base:
                parser.set("testenv", key, "\n".join(_toml_commands(base[key])))
    if isinstance(envs, dict):
        for name, table in envs.items():
            if not isinstance(table, dict):
                continue
            section = f"testenv:{name}"
            parser.add_section(section)
            for key in ("commands", "commands_pre", "commands_post"):
                if key in table:
                    parser.set(section, key, "\n".join(_toml_commands(table[key])))
    return parser, None


def _toml_commands(value: object) -> List[str]:
    if isinstance(value, str):
        return [value]
    if isinstance(value, list):
        lines = []
        for item in value:
            if isinstance(item, str):
                lines.append(item)
            elif isinstance(item, list):
                lines.append(" ".join(str(part) for part in item))
        return lines
    return []


def _tox_block(words: Sequence[str], cwd: Path, root: Path) -> Optional[str]:
    from tiancode.tools.execution import is_dangerous_command

    if any(word in {"-h", "--help", "--version", "--showconfig", "--help-ini"} for word in words):
        return None
    positionals = [word for word in words[1:] if not word.startswith("-")]
    if positionals and positionals[0] in {
        "exec", "list", "config", "depends", "quickstart", "devenv", "legacy", "schema",
    }:
        return None
    if "--notest" in words:
        return None
    config_raw = ""
    envs: List[str] = []
    index = 1
    while index < len(words):
        word = words[index]
        if word in {"-c", "--conf"}:
            index += 1
            config_raw = words[index] if index < len(words) else ""
        elif word.startswith("--conf="):
            config_raw = word.split("=", 1)[1]
        elif word in {"-e", "--env"}:
            index += 1
            if index < len(words):
                envs.extend(part for part in words[index].split(",") if part)
        elif word.startswith("--env=") or word.startswith("-e"):
            raw = word.split("=", 1)[1] if "=" in word else word[2:]
            envs.extend(part for part in raw.split(",") if part)
        index += 1
    if config_raw:
        path = _inside(root, config_raw, cwd)
        if path is None:
            return "tox 配置越出工作区，已拒绝执行"
    else:
        path = None
        for name in ("tox.ini", "setup.cfg", "pyproject.toml"):
            candidate = cwd / name
            if not candidate.exists() and not candidate.is_symlink():
                continue
            if name == "setup.cfg":
                text = _owned_text(candidate) if not candidate.is_symlink() else None
                if text is None or "[tox" not in text:
                    continue
            if name == "pyproject.toml":
                text = _owned_text(candidate) if not candidate.is_symlink() else None
                if text is None or "[tool.tox" not in text and "legacy_tox_ini" not in text:
                    continue
            path = candidate
            break
    if path is None:
        return None
    if path.is_symlink():
        return "tox 配置是符号链接或硬链接，已拒绝执行"
    from tiancode.core.path_safety import is_shared_hardlink

    if is_shared_hardlink(path):
        return "tox 配置是符号链接或硬链接，已拒绝执行"
    text = _owned_text(path)
    if text is None:
        return "tox 配置无法作为普通文件读取，已拒绝执行"
    if len(text) > _MAX_TEXT:
        return "tox 配置过大，无法在执行前检查命令，已拒绝执行"
    parser, reason = _read_tox_parser(path)
    if reason:
        return reason
    if parser is None or not any(
        parser.has_option(section, key)
        for section in parser.sections()
        for key in ("commands", "commands_pre", "commands_post")
        if parser.has_section(section)
    ):
        return None
    if not envs:
        listed = ""
        for section in ("tox", "tox:tox"):
            if parser.has_section(section) and parser.has_option(section, "envlist"):
                listed = parser.get(section, "envlist")
                break
        envs = [part for part in re.split(r"[\s,]+", listed) if part and part != "testenv"]
        if not envs:
            envs = ["testenv"]
            envs.extend(
                section.split(":", 1)[1]
                for section in parser.sections()
                if section.startswith("testenv:")
            )
    for env in envs:
        for command in _tox_commands(parser, env):
            if is_dangerous_command(command):
                return f"tox 环境 {env} 的命令含高危命令，已拒绝执行"
    return None


def recipe_block_reason(command: str, work_dir: Path) -> Optional[str]:
    """Why make, just, or tox must not start, or None when the recipes are safe.

    `echo make test` does not match: the program word has to be make, just, or
    tox. `make -n` still refuses a `$(shell ...)` that runs while the file is
    read, and does not refuse a recipe that dry-run would skip. A leading
    `MAKEFILES=evil.mk`, `export MAKEFILES=evil.mk`, or `env MAKEFILES=evil.mk`
    is the file make reads before the makefile. The parent environment is
    used when this command does not set that name. `MAKEFLAGS` and
    `GNUMAKEFLAGS` are read the same way. The command is split without
    stripping `$(...)`, so `make -E '$(shell ...)'` still has its shell text.
    """
    from tiancode.tools.execution import (
        _INVISIBLE_CHARS,
        _directory_inside,
        _expand_dollars,
        _literal_assignment,
        _peel_prefixes,
        _quote_aware_segments,
        _split_words,
        _unquote_shell_word,
        get_sanitized_env,
    )

    text = _INVISIBLE_CHARS.sub("", str(command)).replace("\x00", "")
    if not text.strip():
        return None
    root = work_dir.resolve()
    cwd = root
    parent = get_sanitized_env()
    values: Dict[str, str] = {}
    exported: set = set()
    for segment in _quote_aware_segments(text):
        words = [_unquote_shell_word(word) for word in _split_words(segment)]
        peeled = _peel_prefixes(words)
        if peeled is None:
            continue
        assignments, command_words = peeled
        local = {key: values[key] for key in exported}
        assigned: set = set()
        for word in assignments:
            parsed = _make_env_assignment(word, _literal_assignment)
            if parsed is None:
                continue
            name, value = parsed
            local[name] = value
            assigned.add(name)
        command_words = [_expand_dollars(word, {**values, **local}) for word in command_words]
        if not command_words:
            values.update(local)
            exported.update(assigned)
            continue
        program = _basename(command_words[0])
        if program == "export":
            for word in command_words[1:]:
                parsed = _make_env_assignment(word, _literal_assignment)
                if parsed is None:
                    continue
                name, value = parsed
                values[name] = value
                exported.add(name)
            continue
        if program == "cd":
            target = command_words[1] if len(command_words) > 1 else ""
            nxt = _directory_inside(cwd, target, root)
            cwd = nxt or cwd
            if nxt is None:
                return "配方命令在越出工作区的目录中运行，已拒绝执行"
            continue
        def _taken(name: str) -> str:
            if name in assigned or name in exported:
                return local.get(name, "")
            return parent.get(name, "")

        inherited = _taken("MAKEFILES")
        run_cwd = cwd
        if program in _MAKE_BINS or program in {"just", "tox"}:
            run_cwd, chdir_error = _env_workdir(words, cwd, root)
            if chdir_error:
                return chdir_error
        if program in _MAKE_BINS:
            reason = _make_block(
                command_words,
                run_cwd,
                root,
                inherited,
                _taken("MAKEFLAGS"),
                _taken("GNUMAKEFLAGS"),
            )
        elif program == "just":
            reason = _just_block(command_words, run_cwd, root)
        elif program == "tox":
            reason = _tox_block(command_words, run_cwd, root)
        else:
            reason = None
        if reason:
            return reason
    return None
