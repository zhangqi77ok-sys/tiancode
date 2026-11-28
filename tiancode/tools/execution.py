"""Subprocess execution tools with timeout, safety guardrails, env scrubbing, and output capture."""

import base64
import os
import re
import subprocess
import sys
import time
from pathlib import Path
from typing import Any, Dict, List
from langchain_core.tools import BaseTool, tool
from tiancode.domain.models import ExecutionResult

DANGEROUS_PATTERNS = [
    # rm with recursive/force flags targeting whole-repo, root, home, or current directory
    r"\brm\s+-[a-zA-Z]*[rRfF][a-zA-Z]*\s+(--\s+)?(\.|\.\/|\*|\/\*|\/|~|~\/)(\s|$)",
    # rm with recursive/force flags targeting traversal
    r"\brm\s+-[a-zA-Z]*[rRfF][a-zA-Z]*\s+(--\s+)?\.\.(\/.*|\s|$)",
    # PowerShell recursive/force Remove-Item targeting root, workspace dot, wildcard, or parent traversal
    r"\b(remove-item|ri)\b(?=.*-(r|recurse|f|force|fo)\b).*(\s|['\"])(\.|\.[\\/]\*?|\*|[/\\]|~|\.\.([\\/].*)?)['\"]?(\s|$)",
    r"\bdel\s+/[sSqQ]*\s+([cC]:\\|\\|\.|\*)(\s|$)",
    r"\bformat\s+[a-zA-Z]:",                                 # format C:
    r"\bmkfs(\.[a-z0-9]+)?\b",                               # mkfs / mkfs.ext4
    r"\bdd\b.*\bof=/dev/(sd[a-z]|nvme\d|hd[a-z]|vd[a-z]|mmcblk\d|disk)",  # raw disk overwrite
    r"\bdiskpart\b",                                         # Windows diskpart
    r"\bgit\s+reset\s+--hard\b",                             # git reset --hard
    r"\bgit\s+clean\s+-[a-zA-Z]*[fF]",                       # git clean -xdf, -fd, -f
    r"\bgit\s+(checkout|restore)\s+(\.|--\s*\.|\*)(\s|$)",   # git restore ., git checkout -- ., git restore *
    r"\b(curl|wget)\b.*\|\s*(/usr/bin/|/bin/)?(ba)?sh\b",    # curl | /bin/sh or bash
    r"\bchmod\s+-R\s+777\b",                                 # chmod -R 777
    r"\bfind\b.*\s+-delete\b",                               # find . -delete
    r":\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:",            # fork bomb
]

# Applied after quotes and newlines are stripped. Each pattern must be a command,
# not a mention of the word inside echo or a commit message.
_CMD = r"(?:^|&&|\|\||;|\|)\s*"
# Characters a shell ignores and a regex would otherwise treat as part of the token.
_INVISIBLE_CHARS = re.compile(
    "[\u200b\u200c\u200d\u2060\ufeff\u180e"
    "\u202a\u202b\u202c\u202d\u202e"
    "\u2066\u2067\u2068\u2069]"
)
_SHELL_SPLIT = re.compile(r"\s*(?:&&|\|\||;)\s*")
_PM_HEAD = re.compile(r"^(npm|pnpm|yarn|bun)\b(.*)$", re.IGNORECASE)
_PM_PREFIX = re.compile(r"(?:--prefix|--dir|--cwd|-C)(?:=|\s+)(\S+)", re.IGNORECASE)
_PM_PREFIX_BARE = re.compile(r"(?:--prefix|--dir|--cwd|-C)\s*$", re.IGNORECASE)
_PM_UNSCOPED = re.compile(
    r"(?:^|\s)(?:--filter|--workspace|--recursive)(?:\s|=|$)|(?:^|\s)(?:-r|-w)(?:\s|$)",
    re.IGNORECASE,
)
_EXTRA_DANGEROUS_PATTERNS = [
    _CMD + r"(?:sudo|doas)\b",
    _CMD + r"(?:shutdown|reboot|poweroff|halt)\b",
    r"\bsystemctl\s+(?:poweroff|reboot|halt|suspend|hibernate|kexec)\b",
    r"\binit\s+[06]\b",
    _CMD + r"git\s+push\b.*(?:\s--force(?:-with-lease)?\b|\s-f\b)",
    _CMD + r"rm\b[^|;]*\s/(?:etc|usr|bin|boot|home|root|sys|proc|dev|sbin|lib|lib64|opt|var|run)(?:/|\s|$)",
    r"\b(?:curl|wget)\b.*\|\s*(?:/usr/bin/|/bin/)?(?:python3?|perl|ruby|node|php|zsh|ksh)\b",
    r"\bbase64\s+(?:-d|--decode)\b.*\|\s*(?:/usr/bin/|/bin/)?(?:python3?|perl|ruby|node|bash|sh|zsh|ksh)\b",
    _CMD + r"shred\b",
    _CMD + r"chmod\b.*\b777\b",
    _CMD + r"find\b.*\s-exec\s+rm\b",
    r">\s*/dev/(?:sd[a-z]|nvme|hd[a-z]|vd[a-z]|mmcblk|disk)\b",
    r"shutil\.rmtree\(\s*/",
    _CMD + r"(?:nc|ncat|netcat)\b.*\s-e\s",
    # Interactive shells and alias expansion run rc-file aliases this check cannot see.
    r"\b(?:bash|zsh|ksh|fish)\s+-[A-Za-z]*i[A-Za-z]*\b",
    r"\b(?:bash|zsh|ksh|fish)\s+--interactive\b",
    r"\b(?:bash|zsh)\s+--(?:rcfile|init-file)\b",
    r"\bshopt\s+-s\b[^|;]*\bexpand_aliases\b",
    r"\bset\s+-o\s+expand_aliases\b",
    r"(?:^|&&|\|\||;|\|)\s*(?:export\s+)?BASH_ENV=",
]


# Leading wrappers hide a command from patterns that require the start of a segment.
# sudo/doas stay in place so the privilege pattern still sees them.
_WRAPPER_NAMES = frozenset({"env", "nice", "nohup", "stdbuf", "time", "command"})
_WRAPPER_VALUE_FLAGS = frozenset({"-n", "-o", "-e", "-u", "--unset"})
# These run a later argv, or a shell string, so `timeout 5 sudo` is still sudo.
# A pid query (`ionice -p 1`, `timeout --help`) does not run a command.
# `setarch` only runs a command after a known architecture. `cmake` is here for
# `cmake -E env`, `chdir`, `time`, and `remove_directory`; other cmake forms stay.
_LAUNCHERS = frozenset({
    "timeout", "flock", "setsid", "ionice", "taskset", "watch", "chrt", "prlimit", "setpriv",
    "unshare", "setarch", "linux32", "linux64", "i386", "i486", "i586", "i686", "athlon",
    "x86_64", "uname26", "capsh", "script", "choom", "cmake",
})
_SETARCH_ARCH = frozenset({
    "uname26", "linux32", "linux64", "i386", "i486", "i586", "i686", "athlon", "x86_64",
})
_DURATION = re.compile(r"^[0-9]+(?:\.[0-9]+)?[smhd]?$", re.IGNORECASE)
_NODE_LOAD = re.compile(
    r"(?:^|\s)(--(?:require|import|loader|experimental-loader|experimental-require-module))"
    r"(?:(=)|[ \t]+)(\S+)"
)
_ANSI_C_QUOTE = re.compile(r"\$'((?:\\.|[^'])*)'")
_PRINTF_SUB = re.compile(
    r"""\$\(\s*printf\s+(?:'((?:\\.|[^'])*)'|"((?:\\.|[^"])*)")\s*\)"""
    r"""|`\s*printf\s+(?:'((?:\\.|[^'])*)'|"((?:\\.|[^"])*)")\s*`"""
)
_ASSIGN_NAME = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
_DOLLAR_NAME = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)")
_DECODER = re.compile(
    r"\b(?:base64\s+(?:-[A-Za-z]*[dD][A-Za-z]*|--decode)"
    r"|xxd\b[^;|&]*\s-[A-Za-z]*r[A-Za-z]*"
    r"|uudecode"
    r"|openssl\s+base64\s+-d"
    r"|openssl\s+enc\s+-d\b)",
    re.IGNORECASE,
)
_DECODE_REDIRECT = re.compile(
    r"\b(?:base64\s+(?:-[A-Za-z]*[dD][A-Za-z]*|--decode)"
    r"|xxd\b[^;|&]*\s-[A-Za-z]*r[A-Za-z]*"
    r"|uudecode)\b[^;|&]*?>\s*([^\s;|&]+)",
    re.IGNORECASE,
)
_EXECUTOR = re.compile(
    r"\b(?:bash|sh|zsh|dash|ksh|fish|eval|source|exec|python3?|perl|ruby|node|php)\b",
    re.IGNORECASE,
)
_B64_TOKEN = re.compile(r"[A-Za-z0-9+/]{8,}={0,2}")
_ANSI_SIMPLE = {
    "a": "\a",
    "b": "\b",
    "e": "\x1b",
    "E": "\x1b",
    "f": "\f",
    "n": "\n",
    "r": "\r",
    "t": "\t",
    "v": "\v",
    "\\": "\\",
    "'": "'",
    '"': '"',
    "?": "?",
}


def _decode_ansi_c_body(body: str) -> str:
    """Decode one `$'...'` or `printf` body. This does not run the shell."""
    out: List[str] = []
    index = 0
    limit = len(body)
    while index < limit:
        if body[index] != "\\" or index + 1 >= limit:
            out.append(body[index])
            index += 1
            continue
        nxt = body[index + 1]
        if nxt in _ANSI_SIMPLE:
            out.append(_ANSI_SIMPLE[nxt])
            index += 2
            continue
        if nxt == "x":
            cursor = index + 2
            digits: List[str] = []
            while cursor < limit and len(digits) < 2 and body[cursor] in "0123456789abcdefABCDEF":
                digits.append(body[cursor])
                cursor += 1
            if digits:
                out.append(chr(int("".join(digits), 16)))
                index = cursor
                continue
        if nxt in ("u", "U"):
            width = 4 if nxt == "u" else 8
            digits = body[index + 2 : index + 2 + width]
            if len(digits) == width and all(char in "0123456789abcdefABCDEF" for char in digits):
                code = int(digits, 16)
                if 0 <= code <= 0x10FFFF:
                    out.append(chr(code))
                    index += 2 + width
                    continue
        if nxt in "01234567":
            cursor = index + 1
            digits = []
            while cursor < limit and len(digits) < 3 and body[cursor] in "01234567":
                digits.append(body[cursor])
                cursor += 1
            out.append(chr(int("".join(digits), 8)))
            index = cursor
            continue
        out.append("\\")
        out.append(nxt)
        index += 2
    return "".join(out)


def _decode_ansi_c_quotes(text: str) -> str:
    return _ANSI_C_QUOTE.sub(lambda match: _decode_ansi_c_body(match.group(1)), text)


def _expand_printf_literals(text: str) -> str:
    """Replace a literal `$(printf '...')` with the characters printf would print."""

    def repl(match: re.Match[str]) -> str:
        body = next(group for group in match.groups() if group is not None)
        return _decode_ansi_c_body(body)

    return _PRINTF_SUB.sub(repl, text)


def _quote_aware_segments(text: str) -> List[str]:
    parts: List[str] = []
    buf: List[str] = []
    quote = ""
    index = 0
    limit = len(text)
    while index < limit:
        char = text[index]
        if quote:
            buf.append(char)
            if char == quote:
                quote = ""
            index += 1
            continue
        if char in "'\"":
            quote = char
            buf.append(char)
            index += 1
            continue
        if text.startswith("&&", index) or text.startswith("||", index):
            parts.append("".join(buf))
            buf = []
            index += 2
            continue
        if char == ";":
            parts.append("".join(buf))
            buf = []
            index += 1
            continue
        buf.append(char)
        index += 1
    parts.append("".join(buf))
    return [part for part in parts if part.strip()]


def _split_words(segment: str) -> List[str]:
    words: List[str] = []
    index = 0
    limit = len(segment)
    while index < limit:
        while index < limit and segment[index].isspace():
            index += 1
        if index >= limit:
            break
        buf: List[str] = []
        quote = ""
        while index < limit:
            char = segment[index]
            if quote:
                buf.append(char)
                if char == quote:
                    quote = ""
                index += 1
                continue
            if char.isspace():
                break
            if char in "'\"":
                quote = char
            buf.append(char)
            index += 1
        if buf:
            words.append("".join(buf))
    return words


def _unquote_literal(raw: str) -> str | None:
    if "$" in raw or "`" in raw:
        return None
    if len(raw) >= 2 and raw[0] == raw[-1] and raw[0] in "'\"":
        return raw[1:-1]
    return raw


def _literal_assignment(word: str) -> tuple[str, str] | None:
    if "=" not in word:
        return None
    name, _, raw = word.partition("=")
    if _ASSIGN_NAME.fullmatch(name) is None:
        return None
    value = _unquote_literal(raw)
    if value is None:
        return None
    return name, value


def _expand_dollars(word: str, values: Dict[str, str]) -> str:
    if len(word) >= 2 and word[0] == "'" and word[-1] == "'":
        return word

    def repl(match: re.Match[str]) -> str:
        name = match.group(1) or match.group(2)
        return values.get(name, match.group(0))

    return _DOLLAR_NAME.sub(repl, word)


def _expand_literal_assignments(text: str) -> str:
    """Substitute literal `name=value` bindings from this same command.

    `a=rm; $a -rf /` is visible after substitution. A value produced by
    another program is left untouched, because this pass does not run it.
    """
    values: Dict[str, str] = {}
    rendered: List[str] = []
    for segment in _quote_aware_segments(text):
        words = _split_words(segment)
        index = 0
        while index < len(words):
            parsed = _literal_assignment(words[index])
            if parsed is None:
                break
            name, value = parsed
            values[name] = value
            index += 1
        rest = " ".join(_expand_dollars(word, values) for word in words[index:])
        if rest:
            rendered.append(rest)
    return " ; ".join(rendered)


def _option_name(word: str) -> str:
    return word.split("=", 1)[0]


def _consume_flag(words: List[str], index: int, value_names: frozenset) -> int:
    """Index just after this option. A value flag also consumes the next word."""
    word = words[index]
    name, eq, _value = word.partition("=")
    index += 1
    if name in value_names and not eq and index < len(words) and not words[index].startswith("-"):
        index += 1
    return index


def _positionals_after(words: List[str], index: int, value_names: frozenset) -> List[str]:
    found: List[str] = []
    while index < len(words):
        word = words[index]
        if word == "--":
            return found + words[index + 1 :]
        if word.startswith("-") and word != "-":
            index = _consume_flag(words, index, value_names)
            continue
        found.append(word)
        index += 1
    return found


def _has_flag(words: List[str], names: frozenset) -> bool:
    for word in words[1:]:
        if word == "--":
            return False
        if _option_name(word) in names:
            return True
    return False


def _unshare_command(words: List[str]) -> List[str]:
    """The program `unshare` runs. Empty when this invocation only prints help.

    Optional arguments (`--user`, `--kill-child`) apply only with `=`. A
    following word is the command, which `unshare --user touch` proved.
    """
    if _has_flag(words, frozenset({"--help", "--version", "-h", "-V"})):
        return []
    values = frozenset({
        "--map-user", "--map-group", "--map-users", "--map-groups",
        "--propagation", "--setgroups", "--root", "-R", "--wd", "-w",
        "--setuid", "-S", "--setgid", "-G", "--monotonic", "--boottime",
    })
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            return words[index + 1 :]
        if word.startswith("--"):
            name, eq, _value = word.partition("=")
            index += 1
            if name in values and not eq:
                index += 1
            continue
        if word.startswith("-") and word != "-":
            rest = word[1:]
            take_next = False
            while rest:
                flag = rest[0]
                rest = rest[1:]
                if flag in "wRSG":
                    take_next = not rest
                    break
            index += 2 if take_next else 1
            continue
        return words[index:]
    return []


def _setarch_command(words: List[str]) -> List[str]:
    """The program after a real architecture. `setarch sudo` runs nothing.

    `setarch` without a name from `setarch --list` treats the next word as
    the architecture and exits. `linux64` already is that architecture.
    """
    if _has_flag(words, frozenset({"--help", "--version", "-h", "-V", "--list", "--show"})):
        return []
    index = 1
    while index < len(words) and words[index].startswith("-") and words[index] != "-":
        if words[index] == "--":
            index += 1
            break
        index += 1
    if _command_basename(words[0]) == "setarch":
        if index >= len(words) or words[index] not in _SETARCH_ARCH:
            return []
        index += 1
    return words[index:]


def _script_body(words: List[str], index: int, *, script_is_rest: bool) -> str:
    """The shell string after `-c` / `--command`. Quotes already removed join the tail."""
    if index >= len(words):
        return ""
    if script_is_rest:
        return " ".join(_unquote_shell_word(part) for part in words[index:])
    return _unquote_shell_word(words[index])


def _script_command(words: List[str], *, script_is_rest: bool) -> List[str] | None:
    """`sh -c` for `script -c`. None when script would start an interactive session."""
    if _has_flag(words, frozenset({"--help", "--version", "-h", "-V"})):
        return []
    long_values = frozenset({
        "--log-in", "--log-out", "--log-io", "--log-timing", "--timing",
        "--logging-format", "--echo", "--output-limit",
    })
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            return None
        name, eq, value = word.partition("=")
        if name == "--command":
            if eq:
                body = value if not script_is_rest else " ".join([value, *words[index + 1 :]])
            else:
                body = _script_body(words, index + 1, script_is_rest=script_is_rest)
            body = _unquote_shell_word(body).strip()
            return ["sh", "-c", body] if body else []
        if name in long_values and not eq:
            index += 2
            continue
        if word.startswith("--"):
            index += 1
            continue
        if word.startswith("-") and word != "-":
            cluster = word[1:]
            cursor = 0
            while cursor < len(cluster):
                flag = cluster[cursor]
                rest = cluster[cursor + 1 :]
                if flag == "c":
                    if rest and not script_is_rest:
                        body = _unquote_shell_word(rest)
                    elif script_is_rest:
                        pieces = ([rest] if rest else []) + words[index + 1 :]
                        body = " ".join(_unquote_shell_word(part) for part in pieces if part)
                    elif index + 1 < len(words):
                        body = _unquote_shell_word(words[index + 1])
                    else:
                        body = ""
                    body = body.strip()
                    return ["sh", "-c", body] if body else []
                if flag in "IOTBtmEo" and not rest:
                    index += 1
                    break
                cursor += 1
            index += 1
            continue
        index += 1
    return None


def _capsh_command(words: List[str], *, script_is_rest: bool) -> List[str]:
    """Arguments after `capsh --` or `-+` are a shell. Without them capsh exits."""
    shell = "bash"
    index = 1
    while index < len(words):
        word = words[index]
        name, eq, value = word.partition("=")
        if name in {"--help", "-h", "--license"}:
            return []
        if name == "--shell" and eq and value:
            shell = _command_basename(value) or "bash"
        if word in {"--", "-+"}:
            rest = words[index + 1 :]
            if not rest:
                return []
            head = rest[0]
            if head in {"-c", "--command"} or (
                head.startswith("-") and not head.startswith("--") and "c" in head[1:]
            ):
                attached = head[2:] if head.startswith("-c") and len(head) > 2 else ""
                if attached and not script_is_rest:
                    body = _unquote_shell_word(attached)
                else:
                    start = rest[1:] if not attached else [attached, *rest[1:]]
                    body = _script_body(start, 0, script_is_rest=script_is_rest)
                body = body.strip()
                return ["sh", "-c", body] if body else []
            return [shell, *[_unquote_shell_word(part) for part in rest]]
        index += 1
    return []


def _skip_glued_or_next(index: int, word: str, eq: bool, bare: set) -> int:
    """Index after this flag. A glued or `=` value does not consume another word."""
    if eq or word not in bare:
        return index + 1
    return index + 2


def _choom_flag(word: str, name: str, letter: str, long_name: str) -> bool:
    """True for `-n`, `-n0`, or `--adjust`, including a value glued to the flag."""
    if name in {f"-{letter}", long_name}:
        return True
    return word.startswith(f"-{letter}") and not word.startswith("--") and len(word) > 2


def _choom_command(words: List[str]) -> List[str]:
    """The command after `choom -n`. `-p` adjusts a pid and does not run one."""
    if _has_flag(words, frozenset({"--help", "--version", "-h", "-V"})):
        return []
    has_pid = False
    has_adjust = False
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            index += 1
            break
        name, eq, _value = word.partition("=")
        # `-n0` and `--adjust=0` keep the number in this word. `-n 0` uses the next one.
        if _choom_flag(word, name, "p", "--pid"):
            has_pid = True
            index = _skip_glued_or_next(index, word, eq, {"-p", "--pid"})
            continue
        if _choom_flag(word, name, "n", "--adjust"):
            has_adjust = True
            index = _skip_glued_or_next(index, word, eq, {"-n", "--adjust"})
            continue
        if word.startswith("-"):
            index += 1
            continue
        break
    if has_pid or not has_adjust or index >= len(words):
        return []
    return words[index:]


def _cmake_e_command(words: List[str]) -> List[str] | None:
    """The command `cmake -E env|chdir|time` runs, or `rm -rf` for remove_directory.

    None means this is not one of those modes, so cmake itself stays in place.
    `cmake -E echo` and `cmake -P` are not commands.
    """
    if len(words) < 3 or words[1] != "-E":
        return None
    mode = words[2]
    if mode == "env":
        index = 3
        while index < len(words):
            word = words[index]
            if word == "--":
                return words[index + 1 :]
            if word.startswith("--unset="):
                index += 1
                continue
            if _is_assignment_word(word):
                index += 1
                continue
            if word.startswith("-"):
                return []
            return words[index:]
        return []
    if mode == "chdir":
        return words[4:] if len(words) >= 5 else []
    if mode == "time":
        return words[3:]
    if mode == "remove_directory":
        paths = [word for word in words[3:] if word != "--"]
        if not paths:
            return []
        return ["rm", "-rf", *paths]
    return None


def _launcher_replacement(words: List[str], *, script_is_rest: bool) -> List[str] | None:
    """The command a launcher runs, or None when `words` does not start with one.

    An empty list means the tool only queries a pid or prints help. It does not
    run a command. `script_is_rest` is for text whose quotes were already
    removed: `flock . -c 'sudo reboot'` is then four words, and the script is
    everything after `-c`.
    """
    if not words or _command_basename(words[0]) not in _LAUNCHERS:
        return None
    program = _command_basename(words[0])

    if program == "timeout":
        if _has_flag(words, frozenset({"--help", "--version"})):
            return []
        index = 1
        values = frozenset({"-k", "--kill-after", "-s", "--signal"})
        while index < len(words) and words[index].startswith("-"):
            index = _consume_flag(words, index, values)
        if index >= len(words) or _DURATION.fullmatch(words[index]) is None:
            return []
        return words[index + 1 :]

    if program == "setsid":
        if _has_flag(words, frozenset({"--help", "--version", "-h", "-V"})):
            return []
        rest = _positionals_after(words, 1, frozenset())
        return rest

    if program == "watch":
        if _has_flag(words, frozenset({"--help", "--version", "-h", "-v"})):
            return []
        exec_argv = _has_flag(words, frozenset({"-x", "--exec"}))
        # `-d` does not consume the command. `--differences=permanent` already holds its value.
        rest = _positionals_after(words, 1, frozenset({"-n", "--interval", "-q", "--equexit"}))
        if not rest:
            return []
        if exec_argv:
            return rest
        return ["sh", "-c", " ".join(_unquote_shell_word(word) for word in rest)]

    if program == "flock":
        script: str | None = None
        positionals: List[str] = []
        index = 1
        values = frozenset({"-w", "--timeout", "-E", "--conflict-exit-code"})
        while index < len(words):
            word = words[index]
            name, eq, value = word.partition("=")
            if name in {"-c", "--command"}:
                if eq:
                    script = value
                    index += 1
                    continue
                if script_is_rest:
                    script = " ".join(_unquote_shell_word(part) for part in words[index + 1 :])
                    index = len(words)
                    continue
                if index + 1 < len(words):
                    script = _unquote_shell_word(words[index + 1])
                    index += 2
                    continue
                return []
            if word.startswith("-") and word != "-":
                index = _consume_flag(words, index, values)
                continue
            positionals.append(word)
            index += 1
        if script is not None:
            return ["sh", "-c", script] if script else []
        if len(positionals) >= 2:
            return positionals[1:]
        return []

    if program == "ionice":
        if _has_flag(words, frozenset({"-p", "--pid", "-P", "--pgid", "-u", "--uid"})):
            rest = _positionals_after(words, 1, frozenset({"-c", "--class", "-n", "--classdata", "-p", "--pid", "-P", "--pgid", "-u", "--uid"}))
            if not rest:
                return []
            return rest
        return _positionals_after(words, 1, frozenset({"-c", "--class", "-n", "--classdata"}))

    if program == "taskset":
        if _has_flag(words, frozenset({"--help", "--version", "-h", "-V"})):
            return []
        if _has_flag(words, frozenset({"-p", "--pid"})):
            return []
        rest = _positionals_after(words, 1, frozenset())
        # The first positional is the mask or cpu list. The rest is the command.
        return rest[1:] if len(rest) >= 2 else []

    if program == "chrt":
        if _has_flag(words, frozenset({"--help", "--version", "-h", "-V", "-m", "--max", "-p", "--pid"})):
            return []
        rest = _positionals_after(
            words,
            1,
            frozenset({"-T", "--sched-runtime", "-P", "--sched-period", "-D", "--sched-deadline"}),
        )
        return rest[1:] if len(rest) >= 2 else []

    if program == "prlimit":
        if _has_flag(words, frozenset({"--help", "--version", "-h", "-V"})):
            return []
        values = frozenset({
            "-p", "--pid", "-o", "--output",
            "-c", "--core", "-d", "--data", "-e", "--nice", "-f", "--fsize",
            "-i", "--sigpending", "-l", "--memlock", "-m", "--rss", "-n", "--nofile",
            "-q", "--msgqueue", "-r", "--rtprio", "-s", "--stack", "-t", "--cpu",
            "-u", "--nproc", "-v", "--as", "-x", "--locks", "-y", "--rttime",
        })
        rest = _positionals_after(words, 1, values)
        if _has_flag(words, frozenset({"-p", "--pid"})) and not rest:
            return []
        return rest

    if program == "setpriv":
        if _has_flag(words, frozenset({"--help", "--version", "-h", "-V", "--dump", "-d"})):
            return []
        values = frozenset({
            "--ambient-caps", "--inh-caps", "--bounding-set", "--ruid", "--euid",
            "--rgid", "--egid", "--reuid", "--regid", "--groups", "--securebits",
            "--pdeathsig", "--selinux-label", "--apparmor-profile",
        })
        return _positionals_after(words, 1, values)

    if program == "unshare":
        return _unshare_command(words)
    if program == "setarch" or program in _SETARCH_ARCH:
        return _setarch_command(words)
    if program == "script":
        return _script_command(words, script_is_rest=script_is_rest)
    if program == "capsh":
        return _capsh_command(words, script_is_rest=script_is_rest)
    if program == "choom":
        return _choom_command(words)
    if program == "cmake":
        return _cmake_e_command(words)

    return None


def _drop_launchers(words: List[str], *, script_is_rest: bool) -> List[str]:
    """Replace timeout/flock/watch/... with the command they actually run."""
    current = list(words)
    for _ in range(8):
        if not current:
            return current
        inner = _launcher_replacement(current, script_is_rest=script_is_rest)
        if inner is None:
            return current
        current = inner
    return current


def _unwrap_leading_wrappers(text: str) -> str:
    """Drop leading `env`/`nice`/`nohup` so a later `sudo` or `reboot` is visible.

    `command -v sudo` prints a path; it does not run sudo, so a query flag
    keeps the original text. `timeout 5 sudo` is sudo: the duration is not
    the command.
    """
    words = text.split()
    changed = False
    while words and Path(words[0]).name.lower() in _WRAPPER_NAMES:
        wrapper = Path(words.pop(0)).name.lower()
        flags: List[str] = []
        while words and words[0].startswith("-"):
            flag = words.pop(0)
            flags.append(flag)
            if flag in _WRAPPER_VALUE_FLAGS and words and not words[0].startswith("-"):
                words.pop(0)
        if wrapper == "command" and {"-v", "-V"} & set(flags):
            return text
        if wrapper == "env":
            while words and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*=.*", words[0]):
                words.pop(0)
        changed = True
    launched = _drop_launchers(words, script_is_rest=True)
    if launched != words:
        if len(launched) >= 3 and launched[0] == "sh" and launched[1] == "-c":
            return launched[2]
        return " ".join(launched)
    if not changed:
        return text
    return " ".join(words)


def _basename_words(text: str) -> str:
    """Match `/usr/bin/sudo` with the same rule as `sudo`."""
    rebuilt: List[str] = []
    changed = False
    for word in text.split():
        if "/" in word or "\\" in word:
            name = Path(word).name
            if name and name != word:
                changed = True
                rebuilt.append(name)
                continue
        rebuilt.append(word)
    if not changed:
        return text
    return " ".join(rebuilt)


def _normalize_command(command: str) -> str:
    """Collapse quotes, invisible characters, `$IFS`, and literal shell escapes.

    A shell runs `r\\m` as `rm`, treats `$IFS` as a space, and decodes
    `$'...'` and `printf '\\xNN'`. This pass does the same before matching.
    Literal assignments in the same command are substituted by the caller.
    Functions and `BASH_ENV` inherited from the parent are removed before the
    process starts. `export -f`, a login shell, and zsh without `-f` are
    refused separately: they run code this function never sees. A file this
    command writes and then executes is refused separately too.
    """
    text = _expand_printf_literals(_decode_ansi_c_quotes(str(command)))
    text = text.replace("\x00", " ")
    text = _INVISIBLE_CHARS.sub("", text)
    text = re.sub(r"[\r\n\t]+", " ", text)
    text = text.replace("$(", " ").replace("`", " ")
    text = re.sub(r"\$\{IFS\}", " ", text)
    text = re.sub(r"\$IFS\b", " ", text)
    text = text.replace("'", "").replace('"', "")
    text = re.sub(r"\\(.)", r"\1", text)
    return re.sub(r"\s+", " ", text).strip()


def _match_candidates(raw: str) -> List[str]:
    prepared = _expand_literal_assignments(_expand_printf_literals(_decode_ansi_c_quotes(raw)))
    texts: List[str] = []
    for text in (raw, prepared):
        normal = _normalize_command(text)
        unwrapped = _unwrap_leading_wrappers(normal)
        for candidate in (text, normal, unwrapped, _basename_words(normal), _basename_words(unwrapped)):
            if candidate and candidate not in texts:
                texts.append(candidate)
    return texts


def _b64_text(token: str) -> str | None:
    padded = token + "=" * ((4 - len(token) % 4) % 4)
    if len(padded) % 4 == 1:
        return None
    try:
        raw = base64.b64decode(padded, validate=True)
    except ValueError:
        return None
    if not raw or len(raw) > 4096 or b"\x00" in raw:
        return None
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        return None
    if any(ord(char) < 9 or (13 < ord(char) < 32) for char in text):
        return None
    return text


def _redirect_then_execute(text: str) -> bool:
    """True when a decoder writes a file and a later word executes that path."""
    targets: List[str] = []
    for segment in _SHELL_SPLIT.split(text):
        match = _DECODE_REDIRECT.search(segment)
        if match:
            target = match.group(1).strip("'\"")
            if target:
                targets.append(target)
        if not targets:
            continue
        words = {word.strip("'\"") for word in segment.split()}
        if any(target in words for target in targets) and _EXECUTOR.search(segment):
            return True
    return False


def _segment_executes_decoder(segment: str) -> bool:
    if _DECODER.search(segment) is None:
        return False
    if re.search(r"(?:^|&&|\|\||;|\|)\s*\$\([^)]*(?:base64|xxd|uudecode|openssl)\b", segment, re.IGNORECASE):
        return True
    if re.search(r"\b(?:eval|source|exec)\b", segment, re.IGNORECASE):
        return True
    if re.search(r"\b(?:bash|sh|zsh|dash|ksh|fish)\s+-c\b", segment, re.IGNORECASE):
        return True
    if "<(" in segment:
        return True
    return bool(re.search(r"\|\s*(?:/usr/bin/|/bin/)?(?:ba)?sh\b", segment, re.IGNORECASE))


def _executed_decoder(text: str) -> bool:
    """True when decoded bytes are executed and were not already a plain command.

    Printing `base64 -d file` stays allowed. `bash -c "$(... | base64 -d)"`
    and `base64 -d x > run.sh; bash run.sh` do not: the bytes actually run,
    and a static pattern never sees them.
    """
    if _redirect_then_execute(text):
        return True
    for segment in _SHELL_SPLIT.split(text):
        if not _segment_executes_decoder(segment):
            continue
        saw_payload = False
        for token in _B64_TOKEN.findall(segment):
            decoded = _b64_text(token)
            if decoded is None:
                continue
            saw_payload = True
            if is_dangerous_command(decoded, _depth=1):
                return True
        if not saw_payload:
            return True
    return False

SENSITIVE_ENV_KEYS = {
    "TIANCODE_API_KEY",
    "OPENAI_API_KEY",
    "ANTHROPIC_API_KEY",
    "GEMINI_API_KEY",
    "DEEPSEEK_API_KEY",
    "AWS_SECRET_ACCESS_KEY",
    "GITHUB_TOKEN",
    "GIT_TOKEN",
}

# Names the fixed list misses, such as AWS_ACCESS_KEY_ID and GH_TOKEN.
# Match a secret word as its own env-name token so PATH and LESSKEY stay.
_SENSITIVE_ENV_NAME = re.compile(
    r"(^|_)(KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|PWD)($|_)",
    re.IGNORECASE,
)
# Git injects config as GIT_CONFIG_KEY_<n>=core.quotePath. The slot name is not a secret.
_GIT_CONFIG_KEY_SLOT = re.compile(r"^GIT_CONFIG_KEY_\d+$")


_STARTUP_SHELLS = frozenset({"bash", "zsh", "ksh", "mksh", "fish", "sh", "dash", "ash"})
_XARGS_VALUE_FLAGS = frozenset({
    "-I", "-J", "-L", "-n", "-P", "-s", "-a", "-E", "-d", "-e",
    "--max-args", "--max-chars", "--max-procs", "--max-lines",
    "--delimiter", "--replace", "--arg-file", "--eof", "--process-slot-var",
})


def _drop_shell_inheritance(env: Dict[str, str]) -> Dict[str, str]:
    """Drop parent startup hooks that are not part of the command text.

    Non-interactive bash imports `BASH_FUNC_*` and reads `BASH_ENV` before the
    command. `expand_aliases` in `SHELLOPTS` then expands aliases from that
    file. `pipefail` and every other option stay. `ENV=production` stays too:
    programs use that name for their own configuration, and a non-interactive
    `sh -c` does not source it.
    """
    cleaned: Dict[str, str] = {}
    for key, value in env.items():
        if key == "BASH_ENV" or key.startswith("BASH_FUNC_"):
            continue
        if key == "NODE_OPTIONS":
            kept = _scrub_node_options(value)
            if kept:
                cleaned[key] = kept
            continue
        if key == "SHELLOPTS":
            kept = ":".join(part for part in value.split(":") if part and part != "expand_aliases")
            if kept:
                cleaned[key] = kept
            continue
        cleaned[key] = value
    return cleaned


def _command_basename(token: str) -> str:
    name = Path(token).name.lower()
    if name.endswith(".exe"):
        name = name[:-4]
    return name


def _unquote_shell_word(raw: str) -> str:
    if len(raw) >= 2 and raw[0] == raw[-1] and raw[0] in "'\"":
        return raw[1:-1]
    return raw


def _is_assignment_word(word: str) -> bool:
    name, sep, _value = word.partition("=")
    return bool(sep) and _ASSIGN_NAME.fullmatch(name) is not None


def _node_options_load_a_file(value: str) -> bool:
    """True when NODE_OPTIONS would load a file before the visible program.

    `--require dotenv` names a package. `--require ./hook.js` and
    `--require=/tmp/hook.js` run that file, the same way BASH_ENV does.
    """
    for match in _NODE_LOAD.finditer(value):
        target = match.group(3).strip("'\"")
        if not target or target.startswith("-"):
            continue
        if target.startswith(".") or target.startswith("/") or "/" in target or "\\" in target:
            return True
        if Path(target).suffix.lower() in {".js", ".mjs", ".cjs", ".ts", ".tsx", ".node"}:
            return True
    return False


def _scrub_node_options(value: str) -> str | None:
    """Drop NODE_OPTIONS when it loads a file. A memory flag on its own stays."""
    if _node_options_load_a_file(value):
        return None
    return value


def _dangerous_shell_assignment(word: str) -> bool:
    name, sep, value = word.partition("=")
    if not sep or _ASSIGN_NAME.fullmatch(name) is None:
        return False
    if name == "BASH_ENV" or name.startswith("BASH_FUNC_"):
        return True
    if name == "NODE_OPTIONS" and _node_options_load_a_file(_unquote_shell_word(value)):
        return True
    return name == "SHELLOPTS" and "expand_aliases" in value


def _short_flag_letters(flag: str) -> str:
    if not flag.startswith("-") or flag.startswith("--"):
        return ""
    return flag[1:]


def _shell_reads_outside_files(program: str, words: List[str]) -> bool:
    """True when this invocation loads rc files the command text does not contain."""
    flags = [word for word in words[1:] if word.startswith("-")]
    for flag in flags:
        if flag in {"--login", "--interactive", "--rcfile", "--init-file"}:
            return True
        letters = _short_flag_letters(flag)
        if "i" in letters or "l" in letters:
            return True
    if program != "zsh":
        return False
    # zsh reads `.zshenv` for every invocation, including `zsh -c`.
    return not any(flag in {"-f", "--no-rcs"} or "f" in _short_flag_letters(flag) for flag in flags)


def _dash_c_body(words: List[str]) -> str | None:
    for index, word in enumerate(words[1:], start=1):
        if word == "--":
            return None
        if word in {"-c", "--command"} or "c" in _short_flag_letters(word):
            if index + 1 >= len(words):
                return None
            return _unquote_shell_word(words[index + 1])
        if not word.startswith("-"):
            return None
    return None


def _exports_function(words: List[str]) -> bool:
    """True for `export -f` / `declare -fx`. The child then runs a function body."""
    if not words:
        return False
    program = _command_basename(words[0])
    if program == "export":
        return any(
            "f" in _short_flag_letters(word) and "n" not in _short_flag_letters(word)
            for word in words[1:]
        )
    if program not in {"declare", "typeset"}:
        return False
    return any(
        "f" in _short_flag_letters(word) and "x" in _short_flag_letters(word) and "n" not in _short_flag_letters(word)
        for word in words[1:]
    )


def _peel_prefixes(words: List[str]) -> tuple[List[str], List[str]] | None:
    """Split leading assignments and env/nice wrappers off a command.

    None means the wrapper only queries a name (`command -v`) and does not run it.
    """
    assignments: List[str] = []
    index = 0
    while index < len(words):
        while index < len(words) and _is_assignment_word(words[index]):
            assignments.append(words[index])
            index += 1
        if index >= len(words) or _command_basename(words[index]) not in _WRAPPER_NAMES:
            break
        wrapper = _command_basename(words[index])
        index += 1
        flags: List[str] = []
        while index < len(words) and words[index].startswith("-") and not _is_assignment_word(words[index]):
            flag = words[index]
            flags.append(flag)
            index += 1
            if flag in _WRAPPER_VALUE_FLAGS and index < len(words) and not words[index].startswith("-"):
                index += 1
        if wrapper == "command" and {"-v", "-V"} & set(flags):
            return None
        if wrapper == "env":
            while index < len(words) and _is_assignment_word(words[index]):
                assignments.append(words[index])
                index += 1
    return assignments, _drop_launchers(words[index:], script_is_rest=False)


def _xargs_utility(words: List[str]) -> str:
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            return words[index + 1] if index + 1 < len(words) else ""
        if word.startswith("-"):
            flag = word.split("=", 1)[0]
            index += 1
            if flag in _XARGS_VALUE_FLAGS and "=" not in word:
                index += 1
            continue
        return word
    return ""


def _find_exec_shell(words: List[str]) -> bool:
    for index, word in enumerate(words):
        if word not in {"-exec", "-execdir"} or index + 1 >= len(words):
            continue
        if _command_basename(words[index + 1]) in _STARTUP_SHELLS:
            return True
    return False


def _pipe_parts(segment: str) -> List[str]:
    parts: List[str] = []
    buf: List[str] = []
    quote = ""
    for char in segment:
        if quote:
            buf.append(char)
            if char == quote:
                quote = ""
            continue
        if char in "'\"":
            quote = char
            buf.append(char)
            continue
        if char == "|":
            parts.append("".join(buf))
            buf = []
            continue
        buf.append(char)
    parts.append("".join(buf))
    return parts


def _shell_words(words: List[str]) -> List[str]:
    if words and words[0] == "&":
        return words[1:]
    return words


def _command_hides_its_body(raw: str, depth: int = 0) -> bool:
    """True when the shell would run text that is not in this command.

    Login and interactive shells, zsh without `-f`, `export -f`, and
    `BASH_ENV` / `BASH_FUNC_*` / `SHELLOPTS=expand_aliases` pull in code defined
    outside the text. A pipe into a shell, `find -exec sh`, and `xargs bash`
    run whatever another program prints. `echo 'export -f helper'` does not.
    """
    if depth > 4:
        return False
    setters = {"export", "declare", "typeset", "env"}
    for segment in _quote_aware_segments(raw):
        for part_index, part in enumerate(_pipe_parts(segment)):
            peeled = _peel_prefixes(_split_words(part.strip()))
            if peeled is None:
                continue
            assignments, command = peeled
            if any(_dangerous_shell_assignment(word) for word in assignments):
                return True
            command = _shell_words(command)
            if not command:
                continue
            program = _command_basename(command[0])
            if part_index > 0 and program in _STARTUP_SHELLS:
                return True
            if part_index > 0:
                from tiancode.tools.command_scan import reads_program_on_stdin

                if reads_program_on_stdin(program, command):
                    return True
            if _exports_function(command):
                return True
            if program in setters and any(_dangerous_shell_assignment(word) for word in command[1:]):
                return True
            if program in _STARTUP_SHELLS and _shell_reads_outside_files(program, command):
                return True
            if program == "find" and _find_exec_shell(command):
                return True
            if program == "xargs" and _command_basename(_xargs_utility(command)) in _STARTUP_SHELLS:
                return True
            if program == "busybox" and len(command) > 1 and _command_basename(command[1]) in _STARTUP_SHELLS:
                applet = command[1:]
                applet_name = _command_basename(applet[0])
                if _shell_reads_outside_files(applet_name, applet):
                    return True
                body = _dash_c_body(applet)
                if body and _command_hides_its_body(body, depth + 1):
                    return True
            if program in _STARTUP_SHELLS:
                body = _dash_c_body(command)
                if body and _command_hides_its_body(body, depth + 1):
                    return True
    return False


def is_dangerous_command(command: str | List[str], *, _depth: int = 0) -> bool:
    """Check if command matches high-risk destructive signatures.

    The raw text is checked first so patterns that look for quotes still match.
    Later passes decode `$'...'` and `printf` escapes, substitute literal
    assignments from the same command, and drop leading `env`/`nice` wrappers
    and absolute paths. A decoded payload is refused when it would be executed.
    Shell startup that would run code from outside this command is refused too.
    `bash -c`, `eval`, and a shell heredoc are checked again as their own
    command, so `bash -c 'sudo reboot'` does not hide behind the outer word.
    `python -c`, `node -e`, `ruby -e`, `perl -e`, and `php -r`/`-R` are checked
    the same way: the code string is scanned, and so is the command passed to
    `os.system`, `execSync`, a backtick, Ruby `%x`, or Perl `qx`. Perl and Ruby
    lines from adjacent `-e` flags are scanned as one program.
    A command substitution is refused when its body is dangerous, because the
    shell runs that body. `eval "$(pyenv init -)"` stays allowed.
    `python -c "print('hello')"` and `pytest -q` stay allowed.
    `timeout 5 sudo` is sudo. `unshare`, `setarch`/`linux64`, `capsh -- -c`,
    `script -c`, `choom -n`, and `cmake -E env`/`chdir`/`time` are the same
    kind of launcher. A pipe into `python` or `node` with no script runs that
    pipe as the program. `awk` `system`, `sed` `e`, `sqlite3 .shell`,
    `find -exec`, `xargs`, an ansible ad-hoc command, a git `-c` value that
    runs a shell, `ssh -o ProxyCommand`, and `tar --to-command` / `-I` are
    scanned as the command they actually run.
    """
    if _depth > 6:
        return False
    raw = " ".join(command) if isinstance(command, (list, tuple)) else str(command)
    if _command_hides_its_body(raw):
        return True
    from tiancode.tools.command_scan import (
        executed_literals,
        inline_scripts,
        interpreter_payloads,
        runs_written_file,
        substitution_bodies,
        unseen_execution,
    )
    from tiancode.tools.hidden_exec import concealed_commands, conceals_unseen_input

    if conceals_unseen_input(raw):
        return True
    if unseen_execution(raw) or runs_written_file(raw):
        return True
    if _depth == 0 and _executed_decoder(raw):
        return True
    for body in substitution_bodies(raw):
        cleaned = body.strip()
        if cleaned and cleaned != raw.strip() and is_dangerous_command(cleaned, _depth=_depth + 1):
            return True
    structural = [raw]
    if _depth == 0:
        prepared = _expand_literal_assignments(_expand_printf_literals(_decode_ansi_c_quotes(raw)))
        if prepared not in structural:
            structural.append(prepared)
    seen_scripts = set()
    for text in structural:
        for script in inline_scripts(text):
            cleaned = script.strip()
            if not cleaned or cleaned == raw.strip() or cleaned in seen_scripts:
                continue
            seen_scripts.add(cleaned)
            if is_dangerous_command(cleaned, _depth=_depth + 1):
                return True
        for program, payload in interpreter_payloads(text):
            cleaned = payload.strip()
            if not cleaned or cleaned == raw.strip():
                continue
            if is_dangerous_command(cleaned, _depth=_depth + 1):
                return True
            for piece in executed_literals(program, cleaned):
                piece = piece.strip()
                if piece and piece != raw.strip() and is_dangerous_command(piece, _depth=_depth + 1):
                    return True
    for extra in concealed_commands(raw):
        cleaned = extra.strip()
        if cleaned and cleaned != raw.strip() and is_dangerous_command(cleaned, _depth=_depth + 1):
            return True
    patterns = (*DANGEROUS_PATTERNS, *_EXTRA_DANGEROUS_PATTERNS)
    texts = _match_candidates(raw) if _depth == 0 else [raw]
    for text in texts:
        for pattern in patterns:
            if re.search(pattern, text, re.IGNORECASE):
                return True
    return False


def is_sensitive_env_name(name: str) -> bool:
    """Return whether a child process should not inherit this environment variable."""
    if _GIT_CONFIG_KEY_SLOT.fullmatch(name):
        return False
    return name in SENSITIVE_ENV_KEYS or _SENSITIVE_ENV_NAME.search(name) is not None


def get_sanitized_env() -> Dict[str, str]:
    """Scrub credentials and parent shell hooks from the subprocess environment."""
    cleaned = {key: value for key, value in os.environ.items() if not is_sensitive_env_name(key)}
    return _drop_shell_inheritance(cleaned)


_ERROR_SIGNAL_MARKERS = (
    "ERROR", "FAILED", "Traceback", "Exception", "AssertionError",
    "SyntaxError", "error TS", "FATAL", "Caused by:",
)


def _decode_process_bytes(raw: bytes | str | None) -> str:
    """Decode subprocess bytes cleanly, falling back from UTF-8 to Windows GBK/CP936."""
    if raw is None:
        return ""
    if isinstance(raw, str):
        return raw
    for enc in ("utf-8", "gb18030", "cp936") if sys.platform == "win32" else ("utf-8",):
        try:
            return raw.decode(enc)
        except UnicodeDecodeError:
            continue
    return raw.decode("utf-8", errors="replace")


def truncate_output(output: str, max_length: int = 8000) -> str:
    """Keep head, tail, and high-signal middle error lines when exceeding length."""
    if len(output) <= max_length:
        return output
    head_len = max_length // 4
    tail_len = max_length // 2
    middle = output[head_len : len(output) - tail_len]
    signals: List[str] = []
    for ln in middle.splitlines():
        clean = ln.strip()
        if clean and any(tok in clean for tok in _ERROR_SIGNAL_MARKERS):
            signals.append(clean[:180])
            if len(signals) >= 12:
                break
    mid_block = (
        "\n\n... [输出过长已截断，抽取中段关键报错行] ...\n" + "\n".join(signals) + "\n...\n\n"
        if signals
        else "\n\n... [输出过长已截断，保留头部与尾部关键信息] ...\n\n"
    )
    return f"{output[:head_len]}{mid_block}{output[-tail_len:]}"


def kill_process_tree(proc: subprocess.Popen) -> None:
    """Recursively kill the entire process tree to eliminate orphan background processes."""
    if proc.poll() is not None:
        return

    if sys.platform == "win32":
        try:
            subprocess.run(
                ["taskkill", "/F", "/T", "/PID", str(proc.pid)],
                capture_output=True,
                check=False,
                creationflags=0x08000000,
            )
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass
    else:
        try:
            import signal
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except Exception:
            try:
                proc.kill()
            except Exception:
                pass


def _run_managed_process(cmd: Any, work_dir: Path, *, use_shell: bool, timeout: int) -> ExecutionResult:
    """Spawn subprocess in work_dir with sanitized env, process-group timeout kill, and error capture."""
    try:
        creationflags = 0x08000000 if sys.platform == "win32" else 0
        proc = subprocess.Popen(
            cmd,
            cwd=str(work_dir),
            shell=use_shell,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=get_sanitized_env(),
            creationflags=creationflags,
            start_new_session=(sys.platform != "win32"),
        )
        try:
            raw_out, raw_err = proc.communicate(timeout=timeout)
            return ExecutionResult(
                exit_code=proc.returncode if proc.returncode is not None else 0,
                stdout=_decode_process_bytes(raw_out),
                stderr=_decode_process_bytes(raw_err),
                timed_out=False,
            )
        except subprocess.TimeoutExpired:
            kill_process_tree(proc)
            try:
                raw_out, raw_err = proc.communicate(timeout=2)
            except Exception:
                raw_out, raw_err = b"", b""
            note = f"执行超时（超过 {timeout} 秒被强制终止）"
            stderr = _decode_process_bytes(raw_err).strip()
            return ExecutionResult(
                exit_code=-1,
                stdout=_decode_process_bytes(raw_out),
                stderr=f"{stderr}\n{note}" if stderr else note,
                timed_out=True,
            )
    except Exception as exc:
        return ExecutionResult(
            exit_code=-1,
            stdout="",
            stderr=f"进程启动失败: {str(exc)}",
            timed_out=False,
        )


def _blocked_result(detail: str) -> ExecutionResult:
    return ExecutionResult(
        exit_code=126,
        stdout="",
        stderr=f"⚠️ 安全拦截 (Security guardrail)：{detail}",
        timed_out=False,
    )


def _manager_args(rest: str) -> str:
    """Drop arguments after `--`. Those belong to the script, not the package manager."""
    return re.split(r"\s--(?:\s|$)", rest, maxsplit=1)[0]


def _hook_names(name: str) -> List[str]:
    return [f"pre{name}", name, f"post{name}"]


_INSTALL_LIFECYCLE = [
    "preinstall", "install", "postinstall", "preprepare", "prepare", "postprepare",
]
_UNINSTALL_LIFECYCLE = ["preuninstall", "uninstall", "postuninstall"]


def _args_after_nth_positional(program: str, args: List[str], nth: int) -> List[str] | None:
    """Arguments after the nth positional. Flag values are not positionals."""
    from tiancode.core.mcp_client import (
        _NPM_SHORT_VALUES,
        _NPM_VALUE_FLAGS,
        _PNPM_LONG_VALUES,
        _PNPM_SHORT_VALUES,
        _YARN_VALUE_FLAGS,
    )

    if program == "npm":
        longs, shorts = _NPM_VALUE_FLAGS, _NPM_SHORT_VALUES
    elif program == "pnpm":
        longs, shorts = _PNPM_LONG_VALUES, _PNPM_SHORT_VALUES
    elif program == "yarn":
        longs, shorts = _YARN_VALUE_FLAGS, frozenset()
    else:
        longs, shorts = frozenset(), frozenset()
    seen = 0
    index = 0
    while index < len(args):
        arg = args[index]
        if arg == "--":
            rest = args[index + 1 :]
            target = nth - seen
            if target < 0 or target >= len(rest):
                return None
            return rest[target + 1 :]
        if arg.startswith("-") and arg != "-":
            name, eq, _value = arg.partition("=")
            if eq:
                index += 1
                continue
            short = arg[1] if not arg.startswith("--") and len(arg) >= 2 else ""
            if short in shorts:
                attached = len(arg) > 2
                index += 1 if attached or index + 1 >= len(args) or args[index + 1] == "--" else 2
                continue
            takes = name in longs or (program == "pnpm" and name.startswith("--config"))
            if takes and index + 1 < len(args) and args[index + 1] != "--":
                index += 2
                continue
            index += 1
            continue
        if seen == nth:
            return args[index + 1 :]
        seen += 1
        index += 1
    return None


def _script_names_for_manager(program: str, args: List[str], _depth: int = 0) -> List[str] | None:
    """package.json names this invocation runs, or None when it runs none.

    An empty list means the command is an install that `--ignore-scripts`
    will not run. `bun test` is Bun's runner. `bun run test` reads the script.
    Yarn 1 has no `t` alias, so `yarn t` is a script named t.
    """
    from tiancode.core.mcp_client import (
        _PNPM_SAFE_COMMANDS,
        _YARN_SAFE_COMMANDS,
        _canonicalize_manager,
        _manager_positionals,
    )

    if _depth > 4:
        return ["*"]
    ignore = any(arg == "--ignore-scripts" or arg.startswith("--ignore-scripts=") for arg in args)
    positionals = _manager_positionals(program, args)
    if program == "bun":
        if len(positionals) >= 2 and positionals[0] == "run" and positionals[1] == "test":
            return _hook_names("test")
        return None
    if not positionals:
        return None
    command = _canonicalize_manager(program, positionals[0])
    if program == "yarn" and command in {"workspace", "workspaces"}:
        if command == "workspace" and len(positionals) >= 3:
            return ["*"]
        if command == "workspaces" and len(positionals) >= 2 and positionals[1] in {"run", "foreach"}:
            return ["*"]
        return None
    if program == "pnpm" and command in {"recursive", "m", "multi"}:
        tail = _args_after_nth_positional(program, args, 0)
        if not tail:
            return None
        if ignore and not any(
            arg == "--ignore-scripts" or arg.startswith("--ignore-scripts=") for arg in tail
        ):
            tail = ["--ignore-scripts", *tail]
        inner = _script_names_for_manager(program, tail, _depth=_depth + 1)
        if not inner:
            return None
        return ["*"]
    if command == "run":
        if len(positionals) < 2 or not positionals[1] or positionals[1].startswith("-"):
            return None
        return _hook_names(positionals[1])
    if command in {"test", "start", "stop", "restart"}:
        return _hook_names(command)
    if command in {"install", "ci", "add", "update", "upgrade", "install-test", "install-ci-test"}:
        names = [] if ignore else list(_INSTALL_LIFECYCLE)
        if command in {"install-test", "install-ci-test"}:
            names.extend(_hook_names("test"))
        return names or None
    if command in {"uninstall", "remove"} and not ignore:
        return list(_UNINSTALL_LIFECYCLE)
    if program == "npm" or command == "exec":
        return None
    if program == "yarn" and command == "node":
        return None
    safe = _PNPM_SAFE_COMMANDS if program == "pnpm" else _YARN_SAFE_COMMANDS
    if command in safe:
        return None
    return _hook_names(command)


def _parse_package_scripts(segment: str) -> tuple[str | None, bool, List[str]] | None:
    """Return `(prefix, unscoped, script names)` when this segment runs package scripts.

    `prefix` is None in the current directory, or "" when a directory flag has
    no usable path. `unscoped` means another package.json would run.
    `script names` of `["*"]` is that same unseen package.
    """
    from tiancode.core.mcp_client import _unwrap_corepack

    words = _unwrap_corepack(segment.split())
    if not words:
        return None
    program = _command_basename(words[0])
    if program not in {"npm", "pnpm", "yarn", "bun"}:
        return None
    names = _script_names_for_manager(program, words[1:])
    if not names:
        return None
    manager_args = _manager_args(" ".join(words[1:]))
    unscoped = names == ["*"] or _PM_UNSCOPED.search(manager_args) is not None
    if unscoped:
        return None, True, names
    scoped = _PM_UNSCOPED.sub(" ", manager_args)
    prefixes = _PM_PREFIX.findall(scoped)
    if prefixes:
        return prefixes[-1], False, names
    if _PM_PREFIX_BARE.search(scoped):
        return "", False, names
    return None, False, names


def _directory_inside(cwd: Path, raw_target: str, root: Path) -> Path | None:
    """Resolve a cd or --prefix target. None when it leaves the workspace."""
    target = raw_target.strip()
    if not target or target.startswith("-"):
        return None
    path = Path(target)
    dest = path if path.is_absolute() else cwd / path
    try:
        resolved = dest.resolve()
        resolved.relative_to(root)
    except (OSError, ValueError):
        return None
    return resolved


def package_script_block_reason(command: str, work_dir: Path) -> str | None:
    """Refuse a package script whose body is unsafe.

    `npm test` and `npm start` are not themselves dangerous strings. The
    script, its pre/post hooks, and the `npm run` chain they reach are read
    from package.json. `npm install` runs the install lifecycle the same way.
    `--ignore-scripts` skips that lifecycle. `npm t` and pnpm's `t` are
    `test`. Yarn 1 has no `t` alias. A link is not followed. `--prefix` must
    stay inside the workspace. `--filter`, `--recursive`, and `yarn workspace`
    are refused because they run a package.json this check does not open.
    `bun test` is the native runner; `bun run test` reads the script.
    A leading `FOO=1`, `env`, or `timeout` does not hide the package manager.
    """
    from tiancode.tools.command_scan import substitution_bodies

    pending = [command]
    seen: set[str] = set()
    while pending and len(seen) < 8:
        current = pending.pop()
        if current in seen:
            continue
        seen.add(current)
        reason = _package_script_in_text(current, work_dir)
        if reason:
            return reason
        for body in substitution_bodies(current):
            cleaned = body.strip()
            if cleaned and cleaned not in seen:
                pending.append(cleaned)
    return None


def _package_script_in_text(command: str, work_dir: Path) -> str | None:
    text = _normalize_command(_expand_literal_assignments(command))
    if not text:
        return None
    root = work_dir.resolve()
    cwd = root
    cd_escaped = False
    from tiancode.core.polyglot import package_scripts_block_reason

    for segment in _SHELL_SPLIT.split(text):
        piece = _unwrap_leading_wrappers(segment.strip())
        if not piece:
            continue
        if piece == "cd" or piece.startswith("cd "):
            target = "" if piece == "cd" else piece[3:].strip()
            nxt = _directory_inside(cwd, target, root)
            if nxt is None:
                cd_escaped = True
            else:
                cwd = nxt
                cd_escaped = False
            continue
        parsed = _parse_package_scripts(piece)
        if parsed is None:
            continue
        prefix, unscoped, names = parsed
        if cd_escaped:
            return "包脚本命令在越出工作区的目录中运行，已拒绝执行"
        if unscoped:
            return "包管理器的 --filter、--workspace、--recursive 或 workspace 会运行未经检查的脚本，已拒绝执行"
        if prefix == "":
            return "包脚本命令的 --prefix/--cwd 无法确认仍在工作区内，已拒绝执行"
        base = cwd
        if prefix:
            dest = _directory_inside(cwd, prefix, root)
            if dest is None:
                return "包脚本命令的 --prefix/--cwd 越出工作区，已拒绝执行"
            base = dest
        reason = package_scripts_block_reason(base, names)
        if reason:
            return reason
    return None


def execute_shell_command(command: str, work_dir: Path, timeout: int = 30) -> ExecutionResult:
    """Execute arbitrary shell command in work_dir with timeout, safety checks, and env scrubbing."""
    if is_dangerous_command(command):
        return _blocked_result("检测到高危系统破坏性命令，执行已被阻止！")
    reason = package_script_block_reason(command, work_dir)
    if reason:
        return _blocked_result(reason)
    from tiancode.core.recipe_guard import recipe_block_reason

    reason = recipe_block_reason(command, work_dir)
    if reason:
        return _blocked_result(reason)
    from tiancode.core.launch_guard import launch_block_reason

    reason = launch_block_reason(command, work_dir)
    if reason:
        return _blocked_result(reason)
    return _run_managed_process(command, work_dir, use_shell=True, timeout=timeout)


def execute_python_script(script_path: Path, work_dir: Path, timeout: int = 15) -> ExecutionResult:
    """Execute python script using current interpreter with env scrubbing."""
    return _run_managed_process([sys.executable, str(script_path)], work_dir, use_shell=False, timeout=timeout)


def create_execution_tools(work_dir: Path, timeout: int = 30) -> List[BaseTool]:
    """Factory to create execution tools bound to work directory and timeout."""

    @tool
    def shell(command: str) -> str:
        """在当前项目工作区执行系统终端命令（如 pytest、python、git、pip、npm 等），返回 Exit Code、耗时和标准输出/错误。"""
        t0 = time.perf_counter()
        res = execute_shell_command(command, work_dir=work_dir, timeout=timeout)
        elapsed = time.perf_counter() - t0
        combined_output = ""
        if res.stdout.strip():
            combined_output += f"[STDOUT]\n{res.stdout.strip()}\n"
        if res.stderr.strip():
            combined_output += f"[STDERR]\n{res.stderr.strip()}\n"
        if not combined_output.strip():
            combined_output = "(无终端输出)"

        truncated = truncate_output(combined_output)
        status_label = "成功" if res.is_success else "失败/异常"
        return f"终端执行{status_label} (Exit Code: {res.exit_code}, 耗时: {elapsed:.2f}s):\n{truncated}"

    @tool
    def run_python(filename: str) -> str:
        """在当前工程目录运行 Python 脚本，并捕获运行结果、print 打印或报错 Traceback。"""
        from tiancode.tools.file_ops import resolve_safe_path
        try:
            resolved = resolve_safe_path(filename, work_dir)
        except PermissionError as err:
            return f"❌ {str(err)}"

        from tiancode.core.path_safety import is_shared_hardlink, names_credential_file

        if is_shared_hardlink(resolved):
            return f"❌ 文件 '{filename}' 是硬链接，未读取或运行其内容。"
        if names_credential_file(filename, resolved, work_dir):
            return f"❌ 文件 '{filename}' 已跳过：疑似密钥或 Git 内部文件，未读取或运行其内容。"
        if not resolved.exists():
            return f"❌ 运行失败：文件 '{filename}' 不存在！"

        res = execute_python_script(resolved, work_dir=work_dir, timeout=timeout)
        if res.is_success:
            return f"【运行成功】输出内容：\n{res.stdout.strip() if res.stdout.strip() else '(无任何控制台输出)'}"
        else:
            err_line = res.stderr.strip().splitlines()[-1] if res.stderr.strip() else "未知异常"
            return f"【运行报错！请分析下方 Traceback 并修复代码】：\n{res.stderr}\n核心错误: {err_line}"

    return [shell, run_python]
