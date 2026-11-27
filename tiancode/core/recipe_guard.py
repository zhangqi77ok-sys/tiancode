"""Expand make, just, and tox recipes before the shell danger rules run.

`make test` is not itself a dangerous string. The recipe, the `$(shell ...)`
that runs while make reads the file, and the same idea for just and tox are
checked with the shell rules. A symlink or hard link is not opened. A `-C`,
`-f`, `--justfile`, or `-c` path that leaves the workspace is refused.

The check is a bounded reader, not a full make implementation. Implicit suffix
rules are not expanded. `$(shell)` inside a recursive variable is checked when
that variable is expanded into a recipe that would run, and immediately when
the assignment itself is `:=` or `!=`.
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
    default_goal: str = ""


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


def _expand_make(text: str, variables: Dict[str, str], depth: int = 0) -> str:
    if depth > 6 or "$" not in text:
        return text

    def repl(match: re.Match[str]) -> str:
        body = match.group(1) if match.group(1) is not None else match.group(2)
        name = body.strip()
        if name.split(None, 1)[0] in {"shell", "eval", "call", "if", "foreach", "wildcard", "origin"}:
            return match.group(0)
        if name in variables:
            return _expand_make(variables[name], variables, depth + 1)
        return match.group(0)

    text = text.replace("$$", "\0")
    text = re.sub(r"\$\(([^)]*)\)|\$\{([^}]*)\}", repl, text)
    return text.replace("\0", "$")


def _shell_bodies(text: str) -> List[str]:
    bodies: List[str] = []
    index = 0
    while True:
        start = text.find("$(", index)
        if start < 0:
            break
        end = _matching_paren(text, start + 1)
        if end < 0:
            break
        inner = text[start + 2 : end].strip()
        if inner.startswith("shell"):
            command = inner[5:].strip()
            if command:
                bodies.append(command)
        index = end + 1
    return bodies


def _matching_paren(text: str, open_at: int) -> int:
    depth = 0
    for index in range(open_at, len(text)):
        if text[index] == "(":
            depth += 1
        elif text[index] == ")":
            depth -= 1
            if depth == 0:
                return index
    return -1


def _eval_chunks(text: str) -> List[str]:
    chunks: List[str] = []
    index = 0
    while True:
        start = text.find("$(", index)
        if start < 0:
            break
        end = _matching_paren(text, start + 1)
        if end < 0:
            break
        inner = text[start + 2 : end].strip()
        if inner.startswith("eval"):
            body = inner[4:].strip()
            if body:
                chunks.append(body)
        index = end + 1
    return chunks


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
            for chunk in _eval_chunks(raw_line):
                pending.extend(_join_continuations(chunk))
            if in_define:
                if raw_line.strip() == "endef":
                    parsed.variables[in_define] = "\n".join(define_lines)
                    if define_op in {":=", "!="}:
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
                    if op in {":=", "!="}:
                        value = _expand_make(value, parsed.variables)
                        parsed.immediate_shells.extend(_shell_bodies(value))
                        if op == "!=":
                            parsed.immediate_shells.append(value)
                    for target in targets:
                        parsed.rules.setdefault(target, _Rule()).local[name] = value
                    rule_targets = []
                    continue
                inline = ""
                prereq_text = rest
                if ";" in rest:
                    prereq_text, _, inline = rest.partition(";")
                prereqs = prereq_text.split()
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
                if op in {":=", "!="}:
                    value = _expand_make(value, {**parsed.variables, name: value})
                    parsed.immediate_shells.extend(_shell_bodies(value))
                    if op == "!=":
                        parsed.immediate_shells.append(value.strip())
                elif op == "+=":
                    value = (parsed.variables.get(name, "") + " " + value).strip()
                elif op == "?=" and name in parsed.variables:
                    rule_targets = []
                    continue
                parsed.variables[name] = value
                rule_targets = []
                continue
            rule_targets = []


def _load_make(path: Path, root: Path, seen: set, parsed: _MakeFile) -> Optional[str]:
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
        reason = _load_make(dest, root, seen, parsed)
        if reason:
            return reason
    return None


def _pattern_rule(parsed: _MakeFile, target: str) -> Optional[_Rule]:
    for pattern, rule in parsed.patterns:
        if "%" not in pattern:
            continue
        prefix, _, suffix = pattern.partition("%")
        if target.startswith(prefix) and target.endswith(suffix) and len(target) >= len(prefix) + len(suffix):
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


def _check_make_targets(parsed: _MakeFile, targets: Sequence[str], *, run_recipes: bool) -> Optional[str]:
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
        rule = parsed.rules.get(target) or _pattern_rule(parsed, target)
        if rule is None:
            continue
        variables = {**parsed.variables, **rule.local}
        for recipe in rule.recipes:
            expanded = _expand_make(recipe, variables)
            if _dangerous_recipe(expanded):
                return f"Makefile 目标 {target} 的配方含高危命令，已拒绝执行"
            for match in _MAKE_RECURSE.finditer(expanded):
                pending.append(match.group(1))
        pending.extend(rule.prereqs)
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
        if _assignment_word(word):
            name, _, value = word.partition("=")
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
                evals.append(arg)
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
                if flag in "CfoWIj":
                    arg = rest
                    if not arg and flag != "j":
                        index += 1
                        arg = words[index] if index < len(words) else ""
                    if flag == "C":
                        dest = _inside(root, arg, directory)
                        if dest is None:
                            return None, "make 的 -C 或 -f 越出工作区，已拒绝执行"
                        directory = dest
                    elif flag == "f":
                        if arg == "-":
                            return None, "Makefile 来自标准输入，已拒绝执行"
                        files.append(arg)
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


def _assignment_word(word: str) -> bool:
    name, sep, _value = word.partition("=")
    return bool(sep) and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name) is not None and not word.startswith("-")


def _make_block(words: Sequence[str], cwd: Path, root: Path) -> Optional[str]:
    invoke, reason = _parse_make_argv(words, cwd, root)
    if reason:
        return reason
    if invoke is None:
        return None
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
    if not chosen:
        return None
    parsed = _MakeFile()
    parsed.variables.update(invoke.overrides)
    if invoke.evals:
        _parse_make_lines(invoke.evals, parsed)
    for path in chosen:
        reason = _load_make(path, root, set(), parsed)
        if reason:
            return reason
    targets = invoke.targets or ([parsed.default_goal] if parsed.default_goal else [])
    return _check_make_targets(parsed, targets, run_recipes=not invoke.dry_run)


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
    read, and does not refuse a recipe that dry-run would skip.
    """
    from tiancode.tools.execution import (
        _SHELL_SPLIT,
        _directory_inside,
        _expand_literal_assignments,
        _normalize_command,
        _unwrap_leading_wrappers,
    )

    text = _normalize_command(_expand_literal_assignments(command))
    if not text:
        return None
    root = work_dir.resolve()
    cwd = root
    for segment in _SHELL_SPLIT.split(text):
        piece = _unwrap_leading_wrappers(segment.strip()).rstrip(")")
        if not piece:
            continue
        if piece == "cd" or piece.startswith("cd "):
            target = "" if piece == "cd" else piece[3:].strip()
            nxt = _directory_inside(cwd, target, root)
            cwd = nxt or cwd
            if nxt is None:
                return "配方命令在越出工作区的目录中运行，已拒绝执行"
            continue
        words = piece.split()
        if not words:
            continue
        program = _basename(words[0])
        if program in _MAKE_BINS:
            reason = _make_block(words, cwd, root)
        elif program == "just":
            reason = _just_block(words, cwd, root)
        elif program == "tox":
            reason = _tox_block(words, cwd, root)
        else:
            reason = None
        if reason:
            return reason
    return None
