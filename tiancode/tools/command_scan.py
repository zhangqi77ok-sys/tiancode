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
- the code string of `python -c`, `node -e` / `--eval`, `ruby -e`, `perl -e`,
  and `php -r` / `-R` / `-B` / `-E`, including the string passed to `os.system`
  or `execSync`. Perl and Ruby append every `-e` as another line, so those
  lines are scanned together. Python runs only the first `-c`, Node runs only
  the last `-e`, and PHP rejects a second `-r`, so those are not joined.

`eval "$(pyenv init -)"` stays allowed. `bash -c 'pytest -q'` stays allowed.
`python -c "print('hello')"` stays allowed. `echo hello | sh` was already
refused; writing that output to a file and running the file is refused for
the same reason. `busybox wget -O` is the same write as `wget -O`.
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


def _download_words(words: Sequence[str]) -> Sequence[str]:
    """Unwrap `busybox wget` so it is checked as wget. The applet is the next word."""
    if not words:
        return words
    execution = _exec()
    program = execution._command_basename(words[0])
    if program == "busybox" and len(words) > 1:
        applet = execution._command_basename(words[1])
        if applet in {"curl", "wget", "tee"}:
            return [applet, *words[2:]]
    return words


def _tool_output_paths(words: Sequence[str]) -> List[str]:
    """Files curl, wget, or tee write. The bytes are not in this command."""
    words = _download_words(words)
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
    found.update(interpreter_write_paths(segment))
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


# The last letter of a short cluster consumes the code. `-Bc` is `-B` then `-c`.
_CODE_LETTERS = {
    "python": "c",
    "python2": "c",
    "python3": "c",
    "node": "ep",
    "nodejs": "ep",
    "bun": "ep",
    "ruby": "e",
    "perl": "eE",
    # PHP `-r` is one program. `-R` runs per input line, `-B` before, `-E` after.
    # The letters are case-sensitive: `-e` is the debugger flag, not code.
    "php": "rRBE",
}
# These interpreters append each code flag as another line of one program.
# Python keeps only the first `-c`. Node keeps only the last `-e`. PHP errors
# on a second `-r`. Joining those would describe a program that does not run.
_JOINED_CODE = {
    "perl": "\n",
    "ruby": "\n",
}
_CODE_LONG = {
    "node": {"--eval", "--print"},
    "nodejs": {"--eval", "--print"},
    "bun": {"--eval", "--print"},
}
# These short flags take the next word, so `python -W error -c ...` still sees `-c`.
_VALUE_LETTERS = {
    "python": "WX",
    "python2": "WX",
    "python3": "WX",
    "node": "r",
    "nodejs": "r",
    "bun": "r",
    "ruby": "I",
    "perl": "I",
    "php": "d",
}
_EXEC_CALL = re.compile(
    r"""(?x)
    \bos\s*\.\s*(?:system|popen)\s*\(
    | \bsubprocess\s*\.\s*(?:run|call|Popen|check_call|check_output|getoutput|getstatusoutput)\s*\(
    | \bcommands\s*\.\s*(?:getoutput|getstatusoutput)\s*\(
    | \bpty\s*\.\s*spawn\s*\(
    | \b(?:execFileSync|execSync|spawnSync|execFile)\s*\(
    | \.(?:execFileSync|execSync|spawnSync|execFile|exec|spawn)\s*\(
    | \b(?:shell_exec|passthru|proc_open)\s*\(
    | (?<![.\w])(?:system|exec|popen)\s*(?:\(|\s+)
    """
)
_OPEN_CALL = re.compile(r"\bopen\s*\(")
_PATH_WRITE = re.compile(
    r"""\bPath\s*\(\s*(['"])(?P<path>(?:\\.|(?!\1).)*)\1\s*\)\s*\.\s*write_(?:text|bytes)\s*\("""
)
_JS_WRITE = re.compile(
    r"\b(?:writeFileSync|appendFileSync|writeFile|appendFile)\s*\(|\bBun\s*\.\s*write\s*\("
)
_RUBY_WRITE = re.compile(r"\b(?:File|IO)\s*\.\s*(?:write|binwrite)\s*(?:\(|(?=['\"]))")
_RUBY_OPEN = re.compile(r"\b(?:File|IO)\s*\.\s*(?:open|new)\s*(?:\(|(?=['\"]))")
_PHP_PUT = re.compile(r"\bfile_put_contents\s*\(")
_PHP_FOPEN = re.compile(r"\bfopen\s*\(")
_PERL_WRITE_PREFIXES = ("+>>", "+>", "+<", ">>", ">")
_PERL_WRITE_MODES = frozenset(_PERL_WRITE_PREFIXES)


def _short_option(program: str, word: str) -> Tuple[str, str]:
    """Classify one short-option word.

    `code` means this flag's argument is the program text. `value` means the
    rest of the cluster, or the next word, belongs to the flag. `stop` means
    `python -m`, which is a module name and not inline code.
    """
    if not word.startswith("-") or word.startswith("--"):
        return "", ""
    cluster = word[1:]
    code_letters = _CODE_LETTERS.get(program, "")
    value_letters = _VALUE_LETTERS.get(program, "")
    index = 0
    while index < len(cluster):
        char = cluster[index]
        if program.startswith("python") and char == "m":
            return "stop", ""
        if char in code_letters:
            return "code", cluster[index + 1 :]
        if char in value_letters:
            return "value", cluster[index + 1 :]
        index += 1
    return "flag", ""


def _long_code(program: str, word: str) -> Optional[str]:
    """Inline code attached to `--eval=...`, or `""` when the next word is the code."""
    name, eq, value = word.partition("=")
    if name not in _CODE_LONG.get(program, ()):
        return None
    if eq:
        return _unwrap_quotes(value)
    return ""


def _payloads_of(program: str, command: Sequence[str]) -> List[str]:
    if program not in _CODE_LETTERS and program not in _CODE_LONG:
        return []
    payloads: List[str] = []
    index = 1
    while index < len(command):
        word = command[index]
        if word == "--":
            break
        attached = _long_code(program, word)
        if attached is not None:
            if attached:
                payloads.append(attached)
            elif index + 1 < len(command):
                index += 1
                payloads.append(_unwrap_quotes(command[index]))
            index += 1
            continue
        kind, extra = _short_option(program, word)
        if kind == "stop":
            break
        if kind == "code":
            if extra:
                payloads.append(_unwrap_quotes(extra))
            elif index + 1 < len(command):
                index += 1
                payloads.append(_unwrap_quotes(command[index]))
            index += 1
            continue
        if kind == "value":
            if not extra and index + 1 < len(command) and not command[index + 1].startswith("-"):
                index += 1
            index += 1
            continue
        if word.startswith("-"):
            index += 1
            continue
        break
    payloads = [item for item in payloads if item]
    if len(payloads) > 1 and program in _JOINED_CODE:
        payloads.append(_JOINED_CODE[program].join(payloads))
    return payloads


def interpreter_payloads(text: str) -> List[Tuple[str, str]]:
    """`(program, code)` pairs a shell will hand to python -c, node -e, and the rest.

    The code is one shell word, so the danger patterns never see `sudo` inside
    `os.system('sudo reboot')` as its own command. The caller scans that word.
    """
    execution = _exec()
    found: List[Tuple[str, str]] = []
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
            if program == "busybox" and len(command) > 1:
                command = command[1:]
                program = execution._command_basename(command[0])
            for payload in _payloads_of(program, command):
                found.append((program, payload))
    return found


def _string_literals(text: str) -> List[str]:
    """Quoted spans. Backticks are included because several of these languages execute them."""
    found: List[str] = []
    index = 0
    limit = len(text)
    while index < limit:
        if text.startswith(("'''", '"""'), index):
            quote = text[index : index + 3]
            end = text.find(quote, index + 3)
            if end < 0:
                break
            found.append(text[index + 3 : end])
            index = end + 3
            continue
        if text[index] not in "'\"`":
            index += 1
            continue
        quote = text[index]
        end = index + 1
        while end < limit:
            if text[end] == "\\":
                end += 2
                continue
            if text[end] == quote:
                break
            end += 1
        else:
            break
        found.append(text[index + 1 : end])
        index = end + 1
    return found


def _balanced_slice(text: str, open_at: int) -> str:
    """Text inside the paren that opens at `open_at - 1` is `(`, so `open_at` is the first inner char."""
    end = _read_balanced(text, open_at, "(", ")")
    if end is None:
        return text[open_at:]
    return text[open_at:end]


def _exec_arguments(payload: str) -> List[str]:
    arguments: List[str] = []
    for match in _EXEC_CALL.finditer(payload):
        token = match.group(0).rstrip()
        if token.endswith("("):
            arguments.append(_balanced_slice(payload, match.end()))
            continue
        rest = payload[match.end() :]
        # `system 'reboot'` and `system ['sudo', 'reboot']` without a paren.
        bracket = rest.find("[")
        paren = rest.find("(")
        if 0 <= bracket < 80 and (paren < 0 or bracket < paren):
            end = _read_balanced(rest, bracket + 1, "[", "]")
            arguments.append(rest[bracket : end + 1] if end is not None else rest[:80])
            continue
        arguments.append(rest[:160])
    return arguments


def _backtick_commands(payload: str) -> List[str]:
    """Ruby, Perl, and PHP run a backtick span as a shell command."""
    bodies: List[str] = []
    index = 0
    limit = len(payload)
    quote = ""
    while index < limit:
        char = payload[index]
        if quote:
            if char == "\\" and quote != "'":
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
        if char != "`":
            index += 1
            continue
        end = index + 1
        while end < limit:
            if payload[end] == "\\":
                end += 2
                continue
            if payload[end] == "`":
                break
            end += 1
        else:
            break
        body = payload[index + 1 : end].strip()
        if body:
            bodies.append(body)
        index = end + 1
    return bodies


_DELIM_CLOSE = {"{": "}", "(": ")", "[": "]", "/": "/"}


def _read_delimited(payload: str, start: int, open_ch: str) -> Optional[Tuple[str, int]]:
    """Body and the index after the closer. Brace delimiters nest. `/` does not."""
    close = _DELIM_CLOSE[open_ch]
    nest = open_ch != "/"
    depth = 1
    index = start
    limit = len(payload)
    while index < limit:
        char = payload[index]
        if char == "\\":
            index += 2
            continue
        if nest and char == open_ch:
            depth += 1
        elif char == close:
            depth -= 1
            if depth == 0:
                return payload[start:index], index + 1
        index += 1
    return None


def _delimited_exec(payload: str, *, ruby: bool, perl: bool) -> List[str]:
    """Ruby `%x{cmd}` and Perl `qx{cmd}` run as a shell. Quoted text is not code."""
    bodies: List[str] = []
    index = 0
    limit = len(payload)
    quote = ""
    while index < limit:
        char = payload[index]
        if quote:
            if char == "\\" and quote != "'":
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
        opener = ""
        start = 0
        if ruby and payload.startswith("%x", index) and index + 2 < limit and payload[index + 2] in "{([":
            opener = payload[index + 2]
            start = index + 3
        elif (
            perl
            and payload.startswith("qx", index)
            and (index == 0 or not (payload[index - 1].isalnum() or payload[index - 1] == "_"))
            and index + 2 < limit
            and payload[index + 2] in "{([/"
        ):
            opener = payload[index + 2]
            start = index + 3
        if not opener:
            index += 1
            continue
        parsed = _read_delimited(payload, start, opener)
        if parsed is None:
            break
        body, index = parsed
        if body.strip():
            bodies.append(body.strip())
    return bodies


def executed_literals(program: str, payload: str) -> List[str]:
    """Shell text an interpreter one-liner will actually pass to the operating system.

    `print('sudo reboot')` is not here. `os.system('sudo reboot')` is, and so is
    `subprocess.run(['rm', '-rf', '/'])` joined back into one command. Ruby
    `%x{...}` and Perl `qx{...}` are the same kind of call as a backtick. A full
    language parser is not attempted: a string built by an f-expression or by
    `exec()` of another code string is not reconstructed.
    """
    found: List[str] = []
    if program in {"ruby", "perl", "php"}:
        found.extend(_backtick_commands(payload))
    if program == "ruby":
        found.extend(_delimited_exec(payload, ruby=True, perl=False))
    elif program == "perl":
        found.extend(_delimited_exec(payload, ruby=False, perl=True))
    for argument in _exec_arguments(payload):
        literals = [item.strip() for item in _string_literals(argument) if item.strip()]
        if not literals:
            continue
        found.append(" ".join(literals))
        found.extend(literals)
    return found


def _mode_writes(mode: str) -> bool:
    return any(char in mode for char in "wax+")


def _php_mode_writes(mode: str) -> bool:
    """PHP `fopen` creates or changes a file for `w`, `a`, `x`, `c`, and `+`."""
    return any(char in mode for char in "waxc+")


def _call_literals(payload: str, match: re.Match[str]) -> List[str]:
    """String literals of one call. A leading `(` is the argument list; otherwise the rest of the statement."""
    if match.group(0).rstrip().endswith("("):
        return _string_literals(_balanced_slice(payload, match.end()))
    rest = payload[match.end():]
    semi = rest.find(";")
    if semi >= 0:
        rest = rest[:semi]
    return _string_literals(rest)


def _perl_redirect_path(literal: str) -> str:
    """Path from Perl's two-argument `open` mode, such as `>run.sh`. `<file` only reads."""
    for prefix in _PERL_WRITE_PREFIXES:
        if literal.startswith(prefix) and len(literal) > len(prefix):
            return literal[len(prefix):].strip()
    return ""


def _perl_open_region(payload: str, at: int) -> str:
    rest = payload[at:]
    stripped = rest.lstrip()
    if stripped.startswith("("):
        open_at = at + rest.find("(") + 1
        return _balanced_slice(payload, open_at)
    semi = rest.find(";")
    return rest[:semi] if semi >= 0 else rest[:200]


def _perl_write_paths(payload: str) -> Set[str]:
    found: Set[str] = set()
    for match in re.finditer(r"\bopen\b", payload):
        literals = [item.strip() for item in _string_literals(_perl_open_region(payload, match.end())) if item.strip()]
        for literal in literals:
            path = _perl_redirect_path(literal)
            if path:
                found.add(_path_key(path))
        if any(literal in _PERL_WRITE_MODES for literal in literals):
            for literal in literals:
                if literal in _PERL_WRITE_MODES or literal == "<" or _perl_redirect_path(literal):
                    continue
                found.add(_path_key(literal))
                break
    return {item for item in found if item}


def _ruby_write_paths(payload: str) -> Set[str]:
    found: Set[str] = set()
    for match in _RUBY_WRITE.finditer(payload):
        literals = _call_literals(payload, match)
        if literals:
            found.add(_path_key(literals[0]))
    for match in _RUBY_OPEN.finditer(payload):
        literals = _call_literals(payload, match)
        mode = literals[1] if len(literals) > 1 else ""
        if literals and _mode_writes(mode):
            found.add(_path_key(literals[0]))
    return {item for item in found if item}


def _php_write_paths(payload: str) -> Set[str]:
    found: Set[str] = set()
    for match in _PHP_PUT.finditer(payload):
        literals = _call_literals(payload, match)
        if literals:
            found.add(_path_key(literals[0]))
    for match in _PHP_FOPEN.finditer(payload):
        literals = _call_literals(payload, match)
        mode = literals[1] if len(literals) > 1 else ""
        if literals and _php_mode_writes(mode):
            found.add(_path_key(literals[0]))
    return {item for item in found if item}


def _python_write_paths(payload: str) -> Set[str]:
    found: Set[str] = set()
    for match in _OPEN_CALL.finditer(payload):
        argument = _balanced_slice(payload, match.end())
        literals = _string_literals(argument)
        mode_match = re.search(r"\bmode\s*=\s*(['\"])(.*?)\1", argument)
        mode = mode_match.group(2) if mode_match else (literals[1] if len(literals) > 1 else "")
        if literals and _mode_writes(mode):
            found.add(_path_key(literals[0]))
    for match in _PATH_WRITE.finditer(payload):
        found.add(_path_key(match.group("path")))
    return {item for item in found if item}


def _js_write_paths(payload: str) -> Set[str]:
    found: Set[str] = set()
    for match in _JS_WRITE.finditer(payload):
        argument = _balanced_slice(payload, match.end())
        literals = _string_literals(argument)
        if literals:
            found.add(_path_key(literals[0]))
    return {item for item in found if item}


def interpreter_write_paths(text: str) -> Set[str]:
    """Paths an interpreter one-liner writes, when the call shape is obvious.

    `open(path, "w")`, `Path(path).write_text`, and `fs.writeFile` are writes.
    So are `File.write` / `IO.write`, Perl `open` with `>`, PHP `file_put_contents`,
    and `fopen` with a write mode. `open(path)` and `File.open(path)` read.
    A later shell or interpreter that runs that path is the same write-then-execute
    case as `> run.sh`.
    """
    found: Set[str] = set()
    for program, payload in interpreter_payloads(text):
        if program in {"python", "python2", "python3"}:
            found.update(_python_write_paths(payload))
        elif program in {"node", "nodejs", "bun"}:
            found.update(_js_write_paths(payload))
        elif program == "ruby":
            found.update(_ruby_write_paths(payload))
        elif program == "perl":
            found.update(_perl_write_paths(payload))
        elif program == "php":
            found.update(_php_write_paths(payload))
    return found
