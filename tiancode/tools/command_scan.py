"""Extra shell scans for code the danger patterns cannot see as a command.

The pattern list matches a command word at a separator. These checks cover the
cases that still run:

- a shell `-c` script, `eval` argument, or heredoc whose body is itself dangerous
- a command substitution whose body is dangerous, because the shell runs it
- `eval` / `source` / `bash -c` of a substitution, except a plain environment
  initializer such as `pyenv init`
- a file this command writes and a later word executes
- a variable assigned from a substitution and then used as the command or as
  `eval` input

`eval "$(pyenv init -)"` stays allowed. `bash -c 'pytest -q'` stays allowed.
`echo hello | sh` was already refused; writing that output to a file and
running the file is refused for the same reason.
"""

from __future__ import annotations

import re
from typing import List, Optional, Sequence, Set, Tuple


_ENV_INIT = {
    "pyenv": ("init",),
    "rbenv": ("init",),
    "nodenv": ("init",),
    "jenv": ("init",),
    "goenv": ("init",),
    "plenv": ("init",),
    "phpenv": ("init",),
    "tfenv": ("init",),
    "direnv": ("export", "hook"),
    "brew": ("shellenv",),
    "mise": ("activate", "hook-env"),
    "rtx": ("activate", "hook-env"),
    "starship": ("init",),
    "zoxide": ("init",),
    "fnm": ("env",),
    "proto": ("activate",),
    "nix": ("print-dev-env",),
}
_SHELLS = frozenset({
    "bash", "sh", "zsh", "dash", "ksh", "mksh", "fish", "ash", "busybox",
})
_INTERPRETERS = frozenset({
    "python", "python3", "python2", "perl", "ruby", "node", "nodejs", "php",
})
_HERE_PROGRAMS = _SHELLS | {"source", ".", "eval", "exec"}
_ASSIGN = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")
_NAME_EXPANSION = re.compile(r"^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$|^\$([A-Za-z_][A-Za-z0-9_]*)$")


def _exec():
    from tiancode.tools import execution as execution

    return execution


def _unwrap_quotes(text: str) -> str:
    raw = text.strip()
    if len(raw) >= 2 and raw[0] == raw[-1] and raw[0] in "'\"":
        return raw[1:-1].strip()
    return raw


def _read_balanced(text: str, start: int, open_ch: str, close_ch: str) -> Optional[int]:
    """Index of the matching closer, or None. `start` is the first inner character."""
    depth = 1
    index = start
    quote = ""
    limit = len(text)
    while index < limit:
        char = text[index]
        if quote == "ansi":
            if char == "'":
                quote = ""
            index += 1
            continue
        if quote:
            if char == quote:
                quote = ""
            index += 1
            continue
        if text.startswith("$'", index):
            quote = "ansi"
            index += 2
            continue
        if char in "'\"":
            quote = char
            index += 1
            continue
        if char == open_ch:
            depth += 1
        elif char == close_ch:
            depth -= 1
            if depth == 0:
                return index
        index += 1
    return None


def _sole_substitution_body(text: str) -> Optional[str]:
    """Body when `text` is only one `$(...)` or backtick span, optionally quoted."""
    raw = _unwrap_quotes(text)
    if raw.startswith("$("):
        end = _read_balanced(raw, 2, "(", ")")
        if end is not None and not raw[end + 1 :].strip():
            return raw[2:end]
    if len(raw) >= 2 and raw.startswith("`") and raw.endswith("`") and raw.count("`") == 2:
        return raw[1:-1]
    return None


def _is_initializer_text(body: str) -> bool:
    """True for one local environment initializer and nothing else.

    `pyenv init -` is the usual `eval` target. A pipe, a second command, or
    another substitution is not that form.
    """
    if any(token in body for token in (";", "|", "&", "<", ">", "`", "$(", "\n")):
        return False
    words = body.split()
    while words and _ASSIGN.fullmatch(words[0].split("=", 1)[0]) and "=" in words[0]:
        words.pop(0)
    if not words:
        return False
    execution = _exec()
    program = execution._command_basename(words[0])
    rest = words[1:]
    if program == "ssh-agent":
        return all(word.startswith("-") for word in rest)
    if program in {"conda", "mamba", "micromamba"}:
        if len(rest) >= 2 and rest[0].startswith("shell") and rest[1] == "hook":
            return all(word.startswith("-") for word in rest[2:])
        return False
    allowed = _ENV_INIT.get(program)
    if not allowed or not rest or rest[0] not in allowed:
        return False
    return True


def _sole_allowlisted(text: str) -> bool:
    body = _sole_substitution_body(text)
    return body is not None and _is_initializer_text(body)


def substitution_bodies(text: str) -> List[str]:
    """Bodies of substitutions the shell will run. Single quotes suppress them."""
    bodies: List[str] = []
    index = 0
    limit = len(text)
    quote = ""
    while index < limit:
        char = text[index]
        if quote == "ansi":
            if char == "'":
                quote = ""
            index += 1
            continue
        if quote:
            if char == quote:
                quote = ""
            index += 1
            continue
        if text.startswith("$'", index):
            quote = "ansi"
            index += 2
            continue
        if char == "'":
            quote = "'"
            index += 1
            continue
        if text.startswith("$(", index):
            end = _read_balanced(text, index + 2, "(", ")")
            if end is None:
                break
            bodies.append(text[index + 2 : end])
            index = end + 1
            continue
        if char == "`":
            end = text.find("`", index + 1)
            if end < 0:
                break
            bodies.append(text[index + 1 : end])
            index = end + 1
            continue
        index += 1
    return bodies


def _shell_tokens(segment: str) -> List[str]:
    """Words, keeping one `$(...)` or backtick span together."""
    tokens: List[str] = []
    index = 0
    limit = len(segment)
    while index < limit:
        while index < limit and segment[index].isspace():
            index += 1
        if index >= limit:
            break
        if segment.startswith("$(", index):
            end = _read_balanced(segment, index + 2, "(", ")")
            if end is None:
                tokens.append(segment[index:])
                break
            tokens.append(segment[index : end + 1])
            index = end + 1
            continue
        if segment[index] == "`":
            end = segment.find("`", index + 1)
            if end < 0:
                tokens.append(segment[index:])
                break
            tokens.append(segment[index : end + 1])
            index = end + 1
            continue
        start = index
        quote = ""
        while index < limit:
            char = segment[index]
            if quote:
                if char == quote:
                    quote = ""
                index += 1
                continue
            if segment.startswith("$(", index):
                end = _read_balanced(segment, index + 2, "(", ")")
                if end is None:
                    index = limit
                    break
                index = end + 1
                continue
            if char == "`":
                end = segment.find("`", index + 1)
                if end < 0:
                    index = limit
                    break
                index = end + 1
                continue
            if char.isspace():
                break
            if char in "'\"":
                quote = char
            index += 1
        tokens.append(segment[start:index])
    return tokens


def _expansion_name(word: str) -> Optional[str]:
    match = _NAME_EXPANSION.fullmatch(_unwrap_quotes(word))
    if match is None:
        return None
    return match.group(1) or match.group(2)


def _hidden_assignment(word: str) -> Optional[str]:
    """Variable name when the value is a substitution this check cannot trust."""
    name, sep, value = word.partition("=")
    if not sep or _ASSIGN.fullmatch(name) is None:
        return None
    if _sole_allowlisted(value):
        return None
    if "$(" in value or "`" in value:
        return name
    return None


def _path_key(token: str) -> str:
    text = _unwrap_quotes(token.strip())
    if text.startswith("./"):
        text = text[2:]
    return text


def _read_word_at(text: str, start: int) -> Tuple[str, int]:
    index = start
    limit = len(text)
    while index < limit and text[index].isspace():
        index += 1
    if index >= limit:
        return "", index
    begin = index
    quote = ""
    while index < limit:
        char = text[index]
        if quote:
            if char == quote:
                quote = ""
            index += 1
            continue
        if char.isspace() or char in "<>|;":
            break
        if char in "'\"":
            quote = char
        index += 1
    return text[begin:index], index


def _redirection_targets(segment: str) -> List[str]:
    """Paths written with `>` or `>>`. Heredocs and `>&` are not files we execute."""
    targets: List[str] = []
    index = 0
    limit = len(segment)
    quote = ""
    while index < limit:
        char = segment[index]
        if quote:
            if char == quote:
                quote = ""
            index += 1
            continue
        if char in "'\"":
            quote = char
            index += 1
            continue
        if char == "\\" and index + 1 < limit:
            index += 2
            continue
        if segment.startswith("<<<", index) or segment.startswith("<<", index):
            jump = 3 if segment.startswith("<<<", index) else 2
            if segment.startswith("<<", index) and index + 2 < limit and segment[index + 2] == "-":
                jump = 3
            _word, index = _read_word_at(segment, index + jump)
            continue
        if char != ">":
            index += 1
            continue
        cursor = index + 1
        if cursor < limit and segment[cursor] == ">":
            cursor += 1
        if cursor < limit and segment[cursor] == "|":
            cursor += 1
        while cursor < limit and segment[cursor].isspace():
            cursor += 1
        if cursor < limit and segment[cursor] == "&":
            index = cursor + 1
            continue
        word, index = _read_word_at(segment, cursor)
        key = _path_key(word)
        if key and not key.isdigit() and key not in {"/dev/null", "NUL"}:
            targets.append(key)
    return targets


def _tool_output_paths(words: Sequence[str]) -> List[str]:
    """Files curl, wget, or tee write. The bytes are not in this command."""
    if not words:
        return []
    execution = _exec()
    program = execution._command_basename(words[0])
    targets: List[str] = []
    index = 1
    while index < len(words):
        word = words[index]
        name, eq, value = word.partition("=")
        consumed = False
        path = ""
        if program == "curl" and (name in {"-o", "--output"} or (word.startswith("-o") and not word.startswith("--"))):
            if word.startswith("-o") and word not in {"-o", "--output"} and not word.startswith("--"):
                path = word[2:]
            elif eq:
                path = value
            elif index + 1 < len(words):
                index += 1
                path = words[index]
                consumed = True
        elif (
            program == "curl"
            and word.startswith("-")
            and not word.startswith("--")
            and word.endswith("o")
            and len(word) > 2
        ):
            if index + 1 < len(words):
                index += 1
                path = words[index]
                consumed = True
        elif program == "wget" and name in {"-O", "--output-document", "-o", "--output-file"}:
            if eq:
                path = value
            elif index + 1 < len(words):
                index += 1
                path = words[index]
                consumed = True
        elif program == "tee" and not word.startswith("-"):
            path = word
        if path:
            key = _path_key(path)
            if key and key not in {"/dev/null", "NUL"}:
                targets.append(key)
        index += 1
        if consumed:
            continue
    return targets


def _writes_of(segment: str) -> Set[str]:
    execution = _exec()
    found: Set[str] = set()
    for part in execution._pipe_parts(segment):
        found.update(_redirection_targets(part))
        tokens = _shell_tokens(part.strip())
        peeled = execution._peel_prefixes(tokens)
        if peeled is None:
            continue
        _assignments, command = peeled
        found.update(_tool_output_paths(execution._shell_words(command)))
    return {item for item in found if item}


def _script_operand(program: str, words: Sequence[str]) -> Optional[str]:
    """The file this command would execute, when it is a file and not `-c` text."""
    execution = _exec()
    args = list(words[1:])
    if program == "busybox" and args and execution._command_basename(args[0]) in _SHELLS:
        return _script_operand(execution._command_basename(args[0]), args)
    shells = program in _SHELLS or program in {"source", ".", "exec"}
    if not shells and program not in _INTERPRETERS:
        return None
    index = 0
    while index < len(args):
        word = args[index]
        if word == "--":
            return args[index + 1] if index + 1 < len(args) else None
        short = word[1:] if word.startswith("-") and not word.startswith("--") else ""
        if shells and (word in {"-c", "--command"} or "c" in short):
            return None
        if program in _INTERPRETERS and word in {"-c", "-m"}:
            return None
        if word.startswith("-"):
            index += 1
            continue
        return word
    return None


def _executes_hidden(segment: str, hidden: Set[str]) -> bool:
    if not hidden:
        return False
    execution = _exec()
    for part in execution._pipe_parts(segment):
        tokens = _shell_tokens(part.strip())
        peeled = execution._peel_prefixes(tokens)
        if peeled is None:
            continue
        _assignments, command = peeled
        command = execution._shell_words(command)
        if not command:
            continue
        if _path_key(command[0]) in hidden:
            return True
        program = execution._command_basename(command[0])
        script = _script_operand(program, command)
        if script and _path_key(script) in hidden:
            return True
    return False


def runs_written_file(raw: str) -> bool:
    """True when a later word executes a path this same command just wrote."""
    execution = _exec()
    hidden: Set[str] = set()
    for segment in execution._quote_aware_segments(raw):
        if _executes_hidden(segment, hidden):
            return True
        hidden.update(_writes_of(segment))
    return False


def _process_sub_is_script(command: Sequence[str]) -> bool:
    """True when `<(` is the file a shell or `source` reads, not text inside `-c`."""
    execution = _exec()
    if not command:
        return False
    program = execution._command_basename(command[0])
    if program not in _HERE_PROGRAMS:
        return False
    skip: Optional[int] = None
    for index, word in enumerate(command[1:], start=1):
        short = word[1:] if word.startswith("-") and not word.startswith("--") else ""
        if word in {"-c", "--command"} or (program in _SHELLS and "c" in short):
            skip = index + 1
            break
    for index, word in enumerate(command):
        if index == skip:
            continue
        if "<(" in word:
            return True
    return False


def _command_word_unseen(word: str) -> bool:
    if _sole_allowlisted(word):
        return False
    return _sole_substitution_body(word) is not None


def unseen_execution(raw: str) -> bool:
    """True when eval, source, or the command word runs text this command does not contain.

    A plain `eval "$(pyenv init -)"` is false. `eval "$(python3 gen.py)"` is true.
    """
    execution = _exec()
    tainted: Set[str] = set()
    for segment in execution._quote_aware_segments(raw):
        for part in execution._pipe_parts(segment):
            tokens = _shell_tokens(part.strip())
            peeled = execution._peel_prefixes(tokens)
            if peeled is None:
                continue
            assignments, command = peeled
            for word in assignments:
                name = _hidden_assignment(word)
                if name:
                    tainted.add(name)
            command = execution._shell_words(command)
            if not command:
                continue
            program = execution._command_basename(command[0])
            if program == "export":
                for word in command[1:]:
                    name = _hidden_assignment(word)
                    if name:
                        tainted.add(name)
            if _expansion_name(command[0]) in tainted or _command_word_unseen(command[0]):
                return True
            if program in {"eval", "source", "."}:
                for word in command[1:]:
                    if _expansion_name(word) in tainted:
                        return True
                    if _sole_allowlisted(word):
                        continue
                    if _sole_substitution_body(word) is not None or "<(" in word:
                        return True
            if _process_sub_is_script(command):
                return True
            if program in _SHELLS:
                body_word = _c_script_word(command)
                if body_word is not None and not _sole_allowlisted(body_word):
                    if _sole_substitution_body(body_word) is not None:
                        return True
                    if _expansion_name(body_word) in tainted:
                        return True
    return False


def _c_script_word(command: Sequence[str]) -> Optional[str]:
    for index, word in enumerate(command[1:], start=1):
        short = word[1:] if word.startswith("-") and not word.startswith("--") else ""
        if word in {"-c", "--command"} or "c" in short:
            if index + 1 < len(command):
                return command[index + 1]
            return None
        if not word.startswith("-"):
            return None
    return None


def _program_before(prefix: str) -> str:
    execution = _exec()
    tokens = _shell_tokens(prefix.strip())
    peeled = execution._peel_prefixes(tokens)
    if peeled is None:
        return ""
    _assignments, command = peeled
    command = execution._shell_words(command)
    if not command:
        return ""
    return execution._command_basename(command[0])


def _heredoc_scripts(raw: str) -> List[str]:
    """Bodies a shell, source, or eval reads from a heredoc or here-string."""
    bodies: List[str] = []
    index = 0
    limit = len(raw)
    quote = ""
    while index < limit:
        char = raw[index]
        if quote:
            if char == quote:
                quote = ""
            index += 1
            continue
        if char in "'\"":
            quote = char
            index += 1
            continue
        here = raw.startswith("<<<", index) or raw.startswith("<<", index)
        if not here:
            index += 1
            continue
        line_start = raw.rfind("\n", 0, index) + 1
        program = _program_before(raw[line_start:index])
        if program not in _HERE_PROGRAMS:
            index += 2
            continue
        if raw.startswith("<<<", index):
            word, index = _read_word_at(raw, index + 3)
            if word:
                bodies.append(_unwrap_quotes(word))
            continue
        cursor = index + 2
        strip_tabs = False
        if cursor < limit and raw[cursor] == "-":
            strip_tabs = True
            cursor += 1
        word, cursor = _read_word_at(raw, cursor)
        delim = _unwrap_quotes(word)
        newline = raw.find("\n", cursor)
        if not delim or newline < 0:
            index = cursor
            continue
        lines: List[str] = []
        rest = raw[newline + 1 :]
        consumed = 0
        for line in rest.splitlines(keepends=True):
            consumed += len(line)
            check = line.rstrip("\r\n")
            if strip_tabs:
                check = check.lstrip("\t")
            if check == delim:
                break
            lines.append(line.rstrip("\r\n"))
        bodies.append("\n".join(lines))
        index = newline + 1 + consumed
    return bodies


def inline_scripts(text: str) -> List[str]:
    """Shell text `bash -c`, `eval`, and heredocs will execute as code."""
    execution = _exec()
    scripts: List[str] = []
    for segment in execution._quote_aware_segments(text):
        for part in execution._pipe_parts(segment):
            tokens = _shell_tokens(part.strip())
            peeled = execution._peel_prefixes(tokens)
            if peeled is None:
                continue
            _assignments, command = peeled
            command = execution._shell_words(command)
            if not command:
                continue
            program = execution._command_basename(command[0])
            if program == "busybox" and len(command) > 1 and execution._command_basename(command[1]) in _SHELLS:
                command = command[1:]
                program = execution._command_basename(command[0])
            if program in _SHELLS:
                word = _c_script_word(command)
                if word is not None:
                    body = _unwrap_quotes(word)
                    if body and not _sole_allowlisted(word):
                        scripts.append(body)
            if program == "eval" and len(command) > 1:
                body = " ".join(_unwrap_quotes(word) for word in command[1:]).strip()
                if body and not _sole_allowlisted(command[1] if len(command) == 2 else ""):
                    scripts.append(body)
    scripts.extend(_heredoc_scripts(text))
    return scripts
