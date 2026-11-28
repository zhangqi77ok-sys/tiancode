"""Commands that a safe-looking argv actually runs.

`timeout 5 sudo` is peeled before the danger patterns run. These are the
cases that stay hidden after that peel: `sed` executes the `e` flag,
`sqlite3 .shell` runs a shell, `git -c` and `git config` plant a shell,
`find -exec` and `xargs` run the utility they name, `ansible -a` is a
command, `ssh -o ProxyCommand` runs on this machine, and `tar --to-command`,
`-I`, and `--checkpoint-action=exec` run a program. `sort --compress-program`
and `scp -S` exec a program. `env --split-string` runs its string.
`vim -c`, `emacs --eval`, and a `groff -U` heredoc run a shell that is not
the outer command. `pnpm exec` forwards its tail. A pipe whose program
is not in the text (`sed e`, `.shell` with no command) is unseen. The caller checks
each returned string with the same danger rules. This is not a language parser.
"""

from __future__ import annotations

import re
from typing import Dict, List, Optional, Sequence, Tuple


_GIT_BUILTINS = frozenset({
    "add", "am", "annotate", "apply", "archive", "bisect", "blame", "branch",
    "bugreport", "bundle", "cat-file", "check-attr", "check-ignore", "check-mailmap",
    "check-ref-format", "checkout", "checkout--worker", "checkout-index", "cherry",
    "cherry-pick", "clean", "clone", "column", "commit", "commit-graph", "commit-tree",
    "config", "count-objects", "credential", "credential-cache", "credential-cache--daemon",
    "credential-store", "describe", "diagnose", "diff", "diff-files", "diff-index",
    "diff-tree", "difftool", "fast-export", "fast-import", "fetch", "fetch-pack",
    "fmt-merge-msg", "for-each-ref", "for-each-repo", "format-patch", "fsck",
    "fsck-objects", "fsmonitor--daemon", "gc", "get-tar-commit-id", "grep", "hash-object",
    "help", "hook", "index-pack", "init", "init-db", "interpret-trailers", "log",
    "ls-files", "ls-remote", "ls-tree", "mailinfo", "mailsplit", "maintenance", "merge",
    "merge-base", "merge-file", "merge-index", "merge-ours", "merge-recursive",
    "merge-recursive-ours", "merge-recursive-theirs", "merge-subtree", "merge-tree",
    "mktag", "mktree", "multi-pack-index", "mv", "name-rev", "notes", "pack-objects",
    "pack-redundant", "pack-refs", "patch-id", "pickaxe", "prune", "prune-packed",
    "pull", "push", "range-diff", "read-tree", "rebase", "receive-pack", "reflog",
    "remote", "remote-ext", "remote-fd", "repack", "replace", "rerere", "reset",
    "restore", "rev-list", "rev-parse", "revert", "rm", "send-pack", "shortlog",
    "show", "show-branch", "show-index", "show-ref", "sparse-checkout", "stage",
    "stash", "status", "stripspace", "submodule--helper", "switch", "symbolic-ref",
    "tag", "unpack-file", "unpack-objects", "update-index", "update-ref",
    "update-server-info", "upload-archive", "upload-archive--writer", "upload-pack",
    "var", "verify-commit", "verify-pack", "verify-tag", "version", "whatchanged",
    "worktree", "write-tree",
})
# Proved to invoke core.fsmonitor. A later git command is not assumed.
_GIT_FS_COMMANDS = frozenset({
    "status", "diff", "checkout", "ls-files", "stash", "merge", "commit",
})
# The value is only refused when it is itself dangerous, so a local fetch
# that would not have called ssh is an acceptable extra block.
_GIT_SSH_COMMANDS = frozenset({
    "ls-remote", "fetch", "pull", "push", "clone", "submodule",
})
_GIT_CONFIG_READS = frozenset({
    "--get", "--get-all", "--get-regexp", "--get-urlmatch", "--get-color",
    "--get-colorbool", "--unset", "--unset-all", "--list", "-l", "--edit", "-e",
    "--rename-section", "--remove-section", "--show-origin", "--show-scope",
    "--help", "--version",
})
_GIT_GLOBAL_VALUES = frozenset({
    "-C", "--git-dir", "--work-tree", "--namespace", "--exec-path",
    "--super-prefix", "--list-cmds", "-c", "--config-env",
})
_GIT_CONFIG_VALUES = frozenset({"-f", "--file", "--blob", "--default", "--type", "--comment"})
_SHELL_KEYS = frozenset({"core.fsmonitor", "core.sshcommand", "core.editor"})
_FIND_EXEC = frozenset({"-exec", "-execdir", "-ok", "-okdir"})
_FIND_END = frozenset({";", "+", r"\;"})
_ANSIBLE_MODULES = frozenset({"command", "shell", "raw"})
_ANSIBLE_TEST_VALUES = frozenset({
    "--truncate", "--export", "--docker-network", "--docker-terminate",
    "--python", "--python-interpreter", "--docker-seccomp", "--docker-memory",
    "--controller", "--target-posix",
})
_ANSIBLE_TEST_OPTIONAL = frozenset({"--color", "--docker"})
_SED_SUBST = frozenset({"s", "y"})
_SED_LINE = frozenset({"a", "c", "i"})
_BACKREF = re.compile(r"(?<!\\)(?:\\\\)*\\[1-9]")
_SLOT_KEY = re.compile(r"^GIT_CONFIG_KEY_(\d+)$")
_SLOT_VALUE = re.compile(r"^GIT_CONFIG_VALUE_(\d+)$")


def _execution():
    from tiancode.tools import execution

    return execution


def _plain(word: str) -> str:
    return _execution()._unquote_shell_word(word).strip()


def _shell_join(words: Sequence[str]) -> str:
    """Join argv the way the shell would see it again, keeping spaces inside quotes."""
    rendered: List[str] = []
    for word in words:
        if word and not re.search(r"[\s;|&<>\"'$\\*?()]", word):
            rendered.append(word)
            continue
        rendered.append("'" + word.replace("'", "'\\''") + "'")
    return " ".join(rendered)


def _basename(word: str) -> str:
    return _execution()._command_basename(_plain(word))


def concealed_commands(raw: str) -> List[str]:
    """Shell text this command will run that the outer words do not show."""
    commands, _unseen = _scan(raw)
    return commands


def conceals_unseen_input(raw: str) -> bool:
    """True when the executed text is not present, as with a bare `sed e`."""
    _commands, unseen = _scan(raw)
    return unseen


def commands_git_will_run(
    words: Sequence[str],
    assignments: Sequence[str],
    *,
    file_config: Optional[Dict[str, str]] = None,
    parent_env: Optional[Dict[str, str]] = None,
    credential_helpers: Optional[Sequence[str]] = None,
) -> List[str]:
    """Shell strings this git argv would execute.

    On-disk config is the base. `GIT_CONFIG_*` from the parent and from this
    command override it, and `-c` wins. A `git config` write of a shell-valued
    key is included even when this invocation would not run it yet.
    `credential_helpers` are the on-disk helpers in the order git will try
    them, before `-c`. An empty `-c credential.helper=` clears that list.
    """
    if not words or _basename(words[0]) != "git":
        return []
    env = dict(parent_env or {})
    for name, value in _assignment_pairs(assignments):
        if name.startswith("GIT_CONFIG_"):
            env[name] = value
    config = {key.lower(): value for key, value in (file_config or {}).items()}
    config.update(_slots_from_env(env))
    config.update(_inline_git_config(words))
    found = _triggered_git_commands(config, words, assignments, parent_env or {})
    found.extend(_credential_shells(words, list(credential_helpers or [])))
    found.extend(_planted_git_commands(words))
    return [item for item in found if item.strip()]


def _scan(raw: str) -> Tuple[List[str], bool]:
    execution = _execution()
    from tiancode.tools.command_scan import _heredoc_entries

    commands: List[str] = []
    unseen = False
    heredocs = _heredoc_entries(raw)
    for segment in execution._quote_aware_segments(raw):
        parts = execution._pipe_parts(segment)
        for index, part in enumerate(parts):
            raw_words = execution._split_words(part.strip())
            commands.extend(_env_split_commands(raw_words))
            peeled = execution._peel_prefixes(raw_words)
            if peeled is None:
                continue
            assignments, command = peeled
            command = execution._shell_words(command)
            if not command:
                continue
            from tiancode.core.mcp_client import _unwrap_corepack

            command = _unwrap_corepack([_plain(word) for word in command])
            if not command:
                continue
            # A launcher or env/nice wrapper was in front of this command.
            # The patterns never see `sudo` in `env -C /tmp sudo` unless the
            # inner argv is checked on its own. Quote-aware words keep a
            # value like `xvfb-run -s '-screen 0 1x1x8'` in one piece.
            outer = _plain_command_words(raw_words)
            inner = [_plain(word) for word in command]
            if inner and inner != outer:
                commands.append(_shell_join(inner))
            program = _basename(command[0])
            previous = parts[index - 1] if index else ""
            extra, hidden = _commands_of(program, command, assignments, previous, heredocs)
            commands.extend(extra)
            unseen = unseen or hidden
    return commands, unseen


def _plain_command_words(words: Sequence[str]) -> List[str]:
    """Words after leading assignments, with one layer of quotes removed."""
    execution = _execution()
    index = 0
    while index < len(words) and execution._is_assignment_word(words[index]):
        index += 1
    return [_plain(word) for word in words[index:]]


def _commands_of(
    program: str,
    command: Sequence[str],
    assignments: Sequence[str],
    previous: str,
    heredocs: Sequence[Tuple[Sequence[str], str]],
) -> Tuple[List[str], bool]:
    if program == "sed":
        return _sed_commands(command)
    if program in {"sqlite3", "sqlite"}:
        return _sqlite_commands(command, previous, heredocs)
    if program == "git":
        return commands_git_will_run(command, assignments), False
    if program == "find":
        return _find_commands(command), False
    if program == "xargs":
        return _xargs_commands(command), False
    if program == "ansible":
        return _ansible_commands(command), False
    if program == "ansible-test":
        return _ansible_test_commands(command), False
    if program in {"ssh", "scp", "sftp"}:
        found = _ssh_proxy_commands(command)
        if program == "scp":
            found.extend(_scp_program(command))
        return found, False
    if program == "tar":
        return _tar_commands(command), False
    if program == "sort":
        return _sort_commands(command), False
    if program in {"vim", "vi", "ex", "view"}:
        return _vim_commands(command), False
    if program == "emacs":
        return _emacs_commands(command), False
    if program in {"groff", "troff", "nroff"}:
        return _roff_commands(command, heredocs), False
    if program in {"npm", "pnpm", "yarn"}:
        tail = _package_exec_tail(program, command[1:])
        if tail:
            return [_shell_join(tail)], False
    return [], False


def _sed_commands(command: Sequence[str]) -> Tuple[List[str], bool]:
    scripts = _sed_scripts(command)
    commands: List[str] = []
    unseen = False
    for script in scripts:
        extra, hidden = _scan_sed_script(script)
        commands.extend(extra)
        unseen = unseen or hidden
    return commands, unseen


def _sed_scripts(command: Sequence[str]) -> List[str]:
    """Scripts from `-e` and the positional program. `-f file` is explicit."""
    scripts: List[str] = []
    saw_file = False
    index = 1
    while index < len(command):
        word = command[index]
        if word == "--":
            rest = command[index + 1 :]
            if rest and not scripts and not saw_file:
                scripts.append(_plain(rest[0]))
            break
        if word in {"-e", "--expression"}:
            if index + 1 < len(command):
                scripts.append(_plain(command[index + 1]))
                index += 2
                continue
            break
        if word.startswith("--expression="):
            scripts.append(_plain(word.split("=", 1)[1]))
            index += 1
            continue
        if word.startswith("--file="):
            saw_file = True
            index += 1
            continue
        if word in {"-f", "--file"}:
            saw_file = True
            index += 2
            continue
        if word.startswith("-") and not word.startswith("--"):
            cluster = word[1:]
            if "e" in cluster:
                rest = cluster.split("e", 1)[1]
                if rest:
                    scripts.append(_plain(rest))
                    index += 1
                    continue
                if index + 1 < len(command):
                    scripts.append(_plain(command[index + 1]))
                    index += 2
                    continue
            if "f" in cluster:
                saw_file = True
                rest = cluster.split("f", 1)[1]
                index += 1 if rest else 2
                continue
            index += 1
            continue
        if word.startswith("--"):
            index += 1
            continue
        if not scripts and not saw_file:
            scripts.append(_plain(word))
        break
    return scripts


def _scan_sed_script(script: str) -> Tuple[List[str], bool]:
    commands: List[str] = []
    unseen = False
    index = 0
    limit = len(script)
    while index < limit:
        while index < limit and script[index] in " \t\r":
            index += 1
        if index >= limit:
            break
        char = script[index]
        if char == "\n":
            index += 1
            continue
        if char in ";{}":
            index += 1
            continue
        if char == "#":
            newline = script.find("\n", index)
            index = limit if newline < 0 else newline + 1
            continue
        index = _skip_sed_addresses(script, index)
        if index >= limit:
            break
        if script[index] == "!":
            index += 1
            while index < limit and script[index] in " \t":
                index += 1
            if index >= limit:
                break
        char = script[index]
        if char in _SED_SUBST:
            parsed = _sed_substitution(script, index)
            if parsed is None:
                index += 1
                continue
            replacement, flags, index = parsed
            if char == "s" and "e" in flags:
                if _sed_replacement_is_unseen(replacement):
                    unseen = True
                else:
                    rendered = _sed_unescape(replacement).strip()
                    if rendered:
                        commands.append(rendered)
            continue
        if char == "e":
            end = index + 1
            while end < limit and script[end] not in ";\n":
                end += 1
            body = script[index + 1 : end].strip()
            if body:
                commands.append(body)
            else:
                unseen = True
            index = end
            continue
        if char in _SED_LINE:
            newline = script.find("\n", index)
            index = limit if newline < 0 else newline + 1
            continue
        end = index + 1
        while end < limit and script[end] not in ";\n":
            end += 1
        index = end
    return commands, unseen


def _skip_sed_addresses(script: str, index: int) -> int:
    for _ in range(2):
        nxt = _skip_one_sed_address(script, index)
        if nxt == index:
            return index
        index = nxt
        while index < len(script) and script[index] in " \t":
            index += 1
        if index >= len(script) or script[index] != ",":
            return index
        index += 1
        while index < len(script) and script[index] in " \t":
            index += 1
    return index


def _skip_one_sed_address(script: str, index: int) -> int:
    if index >= len(script):
        return index
    char = script[index]
    if char.isdigit():
        while index < len(script) and script[index].isdigit():
            index += 1
        return index
    if char == "$":
        return index + 1
    if char in "+~" and index + 1 < len(script) and script[index + 1].isdigit():
        index += 1
        while index < len(script) and script[index].isdigit():
            index += 1
        return index
    delim = ""
    if char == "\\":
        if index + 2 >= len(script):
            return index
        delim = script[index + 1]
        index += 2
    elif char == "/":
        delim = "/"
        index += 1
    else:
        return index
    while index < len(script):
        if script[index] == "\\":
            index += 2
            continue
        if script[index] == delim:
            return index + 1
        if script[index] == "\n":
            return index
        index += 1
    return index


def _sed_substitution(script: str, index: int) -> Optional[Tuple[str, str, int]]:
    if index + 1 >= len(script):
        return None
    delim = script[index + 1]
    if delim.isalnum() or delim in " \t\n\\;{}":
        return None
    parts: List[str] = []
    buf: List[str] = []
    cursor = index + 2
    while cursor < len(script) and len(parts) < 2:
        char = script[cursor]
        if char == "\\":
            buf.append(script[cursor : cursor + 2] if cursor + 1 < len(script) else "\\")
            cursor += 2
            continue
        if char == delim:
            parts.append("".join(buf))
            buf = []
            cursor += 1
            continue
        if char == "\n":
            return None
        buf.append(char)
        cursor += 1
    if len(parts) < 2:
        return None
    flags: List[str] = []
    while cursor < len(script) and script[cursor].isalpha():
        flags.append(script[cursor])
        cursor += 1
    return parts[1], "".join(flags), cursor


def _sed_replacement_is_unseen(replacement: str) -> bool:
    """True when `&` or a backreference makes the executed text depend on input.

    `\\&` is a literal ampersand. `s/foo/bar/e` runs `bar`.
    """
    index = 0
    while index < len(replacement):
        if replacement[index] == "\\" and index + 1 < len(replacement):
            index += 2
            continue
        if replacement[index] == "&":
            return True
        index += 1
    return _BACKREF.search(replacement) is not None


def _sed_unescape(text: str) -> str:
    rendered: List[str] = []
    index = 0
    while index < len(text):
        if text[index] == "\\" and index + 1 < len(text):
            rendered.append(text[index + 1])
            index += 2
            continue
        rendered.append(text[index])
        index += 1
    return "".join(rendered)


def _sqlite_commands(
    command: Sequence[str],
    previous: str,
    heredocs: Sequence[Tuple[Sequence[str], str]],
) -> Tuple[List[str], bool]:
    texts = [_plain(word) for word in command[1:] if not _plain(word).startswith("-")]
    if previous:
        texts.extend(_echo_literals(previous))
    for words, body in heredocs:
        if words and _basename(words[0]) in {"sqlite3", "sqlite"}:
            texts.append(body)
    commands: List[str] = []
    unseen = False
    for text in texts:
        extra, hidden = _dot_shells(text)
        commands.extend(extra)
        unseen = unseen or hidden
    return commands, unseen


def _dot_shells(text: str) -> Tuple[List[str], bool]:
    commands: List[str] = []
    unseen = False
    lines = text.splitlines() or [text]
    for line in lines:
        stripped = line.strip()
        if not stripped.startswith("."):
            continue
        head, _, rest = stripped.partition(" ")
        if head.lower() not in {".shell", ".system"}:
            continue
        body = rest.strip()
        if body:
            commands.append(body)
        else:
            unseen = True
    return commands, unseen


def _echo_literals(part: str) -> List[str]:
    execution = _execution()
    peeled = execution._peel_prefixes(execution._split_words(part.strip()))
    if peeled is None:
        return []
    _assignments, command = peeled
    command = execution._shell_words(command)
    if not command:
        return []
    program = _basename(command[0])
    if program not in {"echo", "printf"}:
        return []
    literals: List[str] = []
    for word in command[1:]:
        plain = _plain(word)
        if program == "echo" and plain in {"-n", "-e", "-E"}:
            continue
        if plain:
            literals.append(plain)
    return literals


def _inline_git_config(words: Sequence[str]) -> Dict[str, str]:
    config: Dict[str, str] = {}
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            break
        name, eq, value = word.partition("=")
        if name in {"--version", "--help", "-h"}:
            break
        if word.startswith("-"):
            index += 1
            if name in {"-c", "--config-env"} and not eq and index < len(words):
                if name == "-c":
                    pair = _config_pair(words[index])
                    if pair is not None:
                        config[pair[0]] = pair[1]
                index += 1
                continue
            if name == "-c" and eq:
                pair = _config_pair(word)
                if pair is not None:
                    config[pair[0]] = pair[1]
            elif name in _GIT_GLOBAL_VALUES and not eq and index < len(words):
                index += 1
            continue
        break
    return config


def _config_pair(word: str) -> Optional[Tuple[str, str]]:
    text = _plain(word)
    if "=" not in text:
        return None
    key, value = text.split("=", 1)
    key = key.strip().lower()
    if not key:
        return None
    return key, _plain(value).strip()


def _git_subcommand(words: Sequence[str]) -> str:
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            return _basename(words[index + 1]) if index + 1 < len(words) else ""
        name, eq, _value = word.partition("=")
        if name in {"--version", "--help", "-h"}:
            return ""
        if word.startswith("-"):
            index += 1
            if name in _GIT_GLOBAL_VALUES and not eq and index < len(words):
                index += 1
            continue
        return _basename(word)
    return ""


def _git_args(words: Sequence[str]) -> List[str]:
    """Words after the subcommand."""
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            return list(words[index + 2 :]) if index + 1 < len(words) else []
        name, eq, _value = word.partition("=")
        if word.startswith("-"):
            index += 1
            if name in _GIT_GLOBAL_VALUES and not eq and index < len(words):
                index += 1
            continue
        return list(words[index + 1 :])
    return []


def _triggered_git_commands(
    config: Dict[str, str],
    words: Sequence[str],
    assignments: Sequence[str],
    parent_env: Dict[str, str],
) -> List[str]:
    sub = _git_subcommand(words)
    if not sub:
        return []
    found: List[str] = []
    monitor = config.get("core.fsmonitor", "")
    if monitor and sub in _GIT_FS_COMMANDS:
        found.append(monitor)
    ssh = config.get("core.sshcommand", "")
    if ssh and sub in _GIT_SSH_COMMANDS:
        found.append(ssh)
    editor = config.get("core.editor", "")
    if editor and sub == "commit" and _commit_uses_editor(words):
        found.append(editor)
    if sub == "diff":
        external = _git_external_diff(config, assignments, parent_env)
        if external:
            found.append(external)
    if sub not in _GIT_BUILTINS:
        alias = config.get(f"alias.{sub}", "")
        if alias.lstrip().startswith("!"):
            body = alias.lstrip()[1:].strip()
            if body:
                found.append(body)
    return found


def _git_external_diff(
    config: Dict[str, str],
    assignments: Sequence[str],
    parent_env: Dict[str, str],
) -> str:
    """The diff program git will exec. `GIT_EXTERNAL_DIFF` replaces the config.

    An empty environment value disables the config entry. `git status` does
    not call this program; only `git diff` was proved to.
    """
    chosen: Optional[str] = None
    for name, value in _assignment_pairs(assignments):
        if name == "GIT_EXTERNAL_DIFF":
            chosen = value
    if chosen is None and "GIT_EXTERNAL_DIFF" in parent_env:
        chosen = parent_env["GIT_EXTERNAL_DIFF"]
    if chosen is None:
        chosen = config.get("diff.external", "")
    return chosen.strip()


def _credential_shells(words: Sequence[str], file_helpers: List[str]) -> List[str]:
    """Shell helpers `git credential` runs. A name without `!` is not a shell.

    Git tries every helper. An empty value clears the ones collected so far,
    which is how `-c credential.helper=` drops the on-disk list.
    """
    if _git_subcommand(words) != "credential":
        return []
    helpers = list(file_helpers)
    for value in _repeated_config(words, "credential.helper"):
        if value == "":
            helpers = []
        else:
            helpers.append(value)
    found: List[str] = []
    for helper in helpers:
        text = helper.lstrip()
        if text.startswith("!"):
            body = text[1:].strip()
            if body:
                found.append(body)
    return found


def _repeated_config(words: Sequence[str], key: str) -> List[str]:
    """Every `-c key=value` in order. A repeated key is not collapsed."""
    found: List[str] = []
    index = 1
    wanted = key.strip().lower()
    while index < len(words):
        word = words[index]
        if word == "--":
            break
        name, eq, value = word.partition("=")
        if name in {"--version", "--help", "-h"}:
            break
        if word.startswith("-"):
            index += 1
            if name in {"-c", "--config-env"} and not eq and index < len(words):
                if name == "-c":
                    pair = _config_pair(words[index])
                    if pair is not None and pair[0] == wanted:
                        found.append(pair[1])
                index += 1
                continue
            if name == "-c" and eq:
                pair = _config_pair(word)
                if pair is not None and pair[0] == wanted:
                    found.append(pair[1])
            elif name in _GIT_GLOBAL_VALUES and not eq and index < len(words):
                index += 1
            continue
        break
    return found


def _commit_uses_editor(words: Sequence[str]) -> bool:
    if _git_subcommand(words) != "commit":
        return False
    has_message = False
    forces_edit = False
    index = 0
    args = _git_args(words)
    message_flags = {"-m", "--message", "-F", "--file", "-C", "--reuse-message"}
    while index < len(args):
        word = args[index]
        name, eq, _value = word.partition("=")
        if word == "--":
            break
        if name in {"-e", "--edit"}:
            forces_edit = True
        if name == "--no-edit":
            has_message = True
        if name in message_flags:
            has_message = True
            if not eq and name in {"-m", "--message", "-F", "--file", "-C", "--reuse-message"}:
                index += 2
                continue
        index += 1
    if forces_edit:
        return True
    return not has_message


def _planted_git_commands(words: Sequence[str]) -> List[str]:
    if _git_subcommand(words) != "config":
        return []
    args = _git_args(words)
    if any(_option_name(word) in _GIT_CONFIG_READS for word in args):
        return []
    positionals: List[str] = []
    index = 0
    while index < len(args):
        word = args[index]
        if word == "--":
            positionals.extend(_plain(item) for item in args[index + 1 :])
            break
        name, eq, _value = word.partition("=")
        if word.startswith("-"):
            index += 1
            if name in _GIT_CONFIG_VALUES and not eq and index < len(args):
                index += 1
            continue
        positionals.append(_plain(word))
        index += 1
    if len(positionals) < 2:
        return []
    planted = _shell_from_config_key(positionals[0], positionals[1])
    return [planted] if planted else []


def _option_name(word: str) -> str:
    return word.split("=", 1)[0]


def _shell_from_config_key(key: str, value: str) -> str:
    lowered = key.strip().lower()
    text = value.strip()
    if not text:
        return ""
    if lowered in _SHELL_KEYS:
        return text
    if lowered.startswith("alias.") and text.startswith("!"):
        return text[1:].strip()
    return ""


def _assignment_pairs(assignments: Sequence[str]) -> List[Tuple[str, str]]:
    pairs: List[Tuple[str, str]] = []
    for word in assignments:
        name, sep, value = word.partition("=")
        if not sep:
            continue
        pairs.append((name, _plain(value)))
    return pairs


def _slots_from_env(env: Dict[str, str]) -> Dict[str, str]:
    raw = env.get("GIT_CONFIG_COUNT", "")
    if not str(raw).isdigit():
        return {}
    count = min(int(raw), 64)
    slots: Dict[int, Dict[str, str]] = {}
    for name, value in env.items():
        key_match = _SLOT_KEY.fullmatch(name)
        if key_match:
            slots.setdefault(int(key_match.group(1)), {})["key"] = value
        value_match = _SLOT_VALUE.fullmatch(name)
        if value_match:
            slots.setdefault(int(value_match.group(1)), {})["value"] = value
    found: Dict[str, str] = {}
    for index in range(count):
        slot = slots.get(index) or {}
        key = slot.get("key", "").strip().lower()
        if key and "value" in slot:
            found[key] = slot["value"]
    return found


def _find_commands(command: Sequence[str]) -> List[str]:
    found: List[str] = []
    index = 1
    while index < len(command):
        if command[index] not in _FIND_EXEC:
            index += 1
            continue
        index += 1
        utility: List[str] = []
        while index < len(command) and command[index] not in _FIND_END:
            utility.append(_plain(command[index]))
            index += 1
        if utility:
            found.append(_shell_join(utility))
        index += 1
    return found


def _xargs_commands(command: Sequence[str]) -> List[str]:
    execution = _execution()
    index = 1
    while index < len(command):
        word = command[index]
        if word == "--":
            index += 1
            break
        if word.startswith("-"):
            flag = word.split("=", 1)[0]
            index += 1
            if flag in execution._XARGS_VALUE_FLAGS and "=" not in word and index < len(command):
                index += 1
            continue
        break
    utility = [_plain(word) for word in command[index :] if _plain(word)]
    if not utility:
        return []
    return [_shell_join(utility)]


def _ansible_commands(command: Sequence[str]) -> List[str]:
    module = "command"
    payload = ""
    index = 1
    while index < len(command):
        word = command[index]
        name, eq, value = word.partition("=")
        if name in {"-m", "--module-name"}:
            if eq:
                module = _plain(value)
            elif index + 1 < len(command):
                module = _plain(command[index + 1])
                index += 1
            index += 1
            continue
        if name in {"-a", "--args"}:
            if eq:
                payload = _plain(value)
            elif index + 1 < len(command):
                payload = _plain(command[index + 1])
                index += 1
            index += 1
            continue
        index += 1
    leaf = module.split(".")[-1].lower()
    if payload and leaf in _ANSIBLE_MODULES:
        return [payload]
    return []


def _ansible_test_commands(command: Sequence[str]) -> List[str]:
    index = 1
    sub = ""
    while index < len(command):
        word = command[index]
        if word == "--":
            index += 1
            break
        if word.startswith("-"):
            index += 1
            continue
        sub = _plain(word)
        index += 1
        break
    if sub != "shell":
        return []
    tail: List[str] = []
    while index < len(command):
        word = command[index]
        name, eq, _value = word.partition("=")
        if word == "--":
            tail.extend(_plain(item) for item in command[index + 1 :])
            break
        if name in {"-h", "--help"}:
            return []
        if word.startswith("-"):
            index += 1
            if name in _ANSIBLE_TEST_VALUES and not eq and index < len(command):
                index += 1
                continue
            if name in _ANSIBLE_TEST_OPTIONAL and not eq and index < len(command):
                if not command[index].startswith("-"):
                    index += 1
            continue
        tail.append(_plain(word))
        index += 1
    if not tail:
        return []
    return [_shell_join(tail)]


def _ssh_proxy_commands(command: Sequence[str]) -> List[str]:
    """The first `ProxyCommand`, which is the one OpenSSH runs.

    `ProxyCommand=none` disables the proxy. A later `-o ProxyCommand` does
    not run. The value is a shell command, not an argument of ssh.
    """
    found = ""
    index = 1
    while index < len(command):
        word = command[index]
        if word == "--":
            break
        option = ""
        if word == "-o" and index + 1 < len(command):
            option = command[index + 1]
            index += 2
        elif word.startswith("-o") and len(word) > 2:
            option = word[2:]
            index += 1
        else:
            index += 1
            continue
        name, eq, value = option.partition("=")
        if not eq or name.lower() != "proxycommand" or found:
            continue
        found = _plain(value).strip()
    if not found or found.lower() == "none":
        return []
    return [found]


def _tar_commands(command: Sequence[str]) -> List[str]:
    """Programs GNU tar runs for `--to-command` and the compress program.

    `-z` selects gzip by itself and is not a shell string. `-I cmd` and
    `--use-compress-program` are. Both forms run, so each one is returned.
    """
    found: List[str] = []
    index = 1
    while index < len(command):
        word = command[index]
        name, eq, value = word.partition("=")
        if name == "--checkpoint-action":
            if eq:
                payload = value
                index += 1
            elif index + 1 < len(command):
                payload = command[index + 1]
                index += 2
            else:
                return found
            payload = _plain(payload).strip()
            if payload.startswith("exec="):
                # `exec='sudo reboot'` keeps the quotes on the shell text.
                body = _plain(payload[len("exec=") :].strip())
                if body:
                    found.append(body)
            continue
        if name in {"--to-command", "--use-compress-program"}:
            if eq:
                payload = value
                index += 1
            elif index + 1 < len(command):
                payload = command[index + 1]
                index += 2
            else:
                return found
            payload = _plain(payload).strip()
            if payload:
                found.append(payload)
            continue
        if word == "-I" or (word.startswith("-") and not word.startswith("--") and "I" in word[1:]):
            attached = ""
            if word != "-I":
                flag_at = word.find("I")
                attached = word[flag_at + 1 :]
            if attached:
                payload = attached
                index += 1
            elif index + 1 < len(command):
                payload = command[index + 1]
                index += 2
            else:
                return found
            payload = _plain(payload).strip()
            if payload:
                found.append(payload)
            continue
        index += 1
    return found


def _env_split_commands(words: Sequence[str]) -> List[str]:
    """The command `env -S` / `--split-string` executes.

    The string is split into arguments, not passed to a shell. `env -S 'pytest -q'`
    stays a normal command. `env --split-string='rm -rf /'` hides `rm` inside
    the option once quotes are removed, so the value is returned whole.
    """
    execution = _execution()
    index = 0
    while index < len(words) and execution._is_assignment_word(words[index]):
        index += 1
    if index >= len(words) or _basename(words[index]) != "env":
        return []
    index += 1
    while index < len(words):
        word = words[index]
        if word == "--":
            break
        plain = _plain(word)
        name, eq, value = plain.partition("=")
        glued = plain.startswith("-S") and not plain.startswith("--") and plain not in {"-S", "-S="}
        if name in {"-S", "--split-string"} or glued:
            if glued:
                payload = plain[2:]
                if payload.startswith("="):
                    payload = payload[1:]
            elif eq:
                payload = value
            elif index + 1 < len(words):
                payload = _plain(words[index + 1])
            else:
                payload = ""
            payload = _plain(payload).strip()
            return [payload] if payload else []
        if not plain.startswith("-"):
            break
        index += 1
        if name in {"-u", "--unset", "-C", "--chdir"} and not eq and index <= len(words):
            # The directory or the variable name is not the split string.
            if not plain.startswith("-C") or plain in {"-C", "--chdir"}:
                if index < len(words) and not words[index].startswith("-"):
                    index += 1
    return []


def _scp_program(command: Sequence[str]) -> List[str]:
    """The program `scp -S` execs in place of ssh. `/usr/bin/ssh` is not a shell."""
    index = 1
    while index < len(command):
        word = command[index]
        name, eq, value = word.partition("=")
        if word == "--":
            break
        if name == "-S" or (word.startswith("-S") and not word.startswith("--") and len(word) > 2):
            if word.startswith("-S") and word not in {"-S"} and not word.startswith("--"):
                payload = word[2:]
            elif eq:
                payload = value
            elif index + 1 < len(command):
                payload = command[index + 1]
            else:
                return []
            payload = _plain(payload).strip()
            return [payload] if payload else []
        index += 1
    return []


def _sort_commands(command: Sequence[str]) -> List[str]:
    """The program GNU sort execs for `--compress-program`. It is not a shell.

    `gzip` stays allowed. `sudo` is the same danger rule as any other program.
    """
    found: List[str] = []
    index = 1
    while index < len(command):
        word = command[index]
        name, eq, value = word.partition("=")
        if name == "--compress-program":
            if eq:
                payload = value
                index += 1
            elif index + 1 < len(command):
                payload = command[index + 1]
                index += 2
            else:
                return found
            payload = _plain(payload).strip()
            if payload:
                found.append(payload)
            continue
        index += 1
    return found


_VIM_SYSTEM = re.compile(r"""\bsystem\s*\(\s*(['"])((?:\\.|(?!\1).)*)\1""")


def _vim_commands(command: Sequence[str]) -> List[str]:
    """Ex commands from `vim -c`, `--cmd`, and `+cmd`. A named file is not one.

    `!sudo reboot` and `call system("sudo reboot")` were proved. `set number`
    does not run a shell.
    """
    found: List[str] = []
    index = 1
    while index < len(command):
        word = command[index]
        name, eq, value = word.partition("=")
        body = ""
        if name in {"-c", "--cmd"}:
            if eq:
                body = value
            elif index + 1 < len(command):
                body = command[index + 1]
                index += 1
        elif word.startswith("+") and word != "+":
            body = word[1:]
        elif word == "+" and index + 1 < len(command):
            body = command[index + 1]
            index += 1
        if body:
            found.extend(_ex_shells(_plain(body)))
        index += 1
    return found


def _ex_shells(text: str) -> List[str]:
    found: List[str] = []
    for part in _split_unquoted(text, "|"):
        body = part.strip()
        if body.startswith(":"):
            body = body[1:].lstrip()
        body = re.sub(r"^(?:silent!|silent)\s+", "", body)
        if body.startswith("!"):
            shell = body[1:].strip()
            if shell:
                found.append(shell)
        for match in _VIM_SYSTEM.finditer(body):
            shell = match.group(2).strip()
            if shell:
                found.append(shell)
    return found


def _split_unquoted(text: str, sep: str) -> List[str]:
    parts: List[str] = []
    buf: List[str] = []
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
        if char == sep:
            parts.append("".join(buf))
            buf = []
            continue
        buf.append(char)
    parts.append("".join(buf))
    return parts


_EMACS_SHELL = re.compile(
    r"""\((?:async-)?shell-command\s+(?:"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)')"""
)


def _emacs_commands(command: Sequence[str]) -> List[str]:
    """Shell text inside `emacs --eval`. `(message "hi")` is not a command.

    `shell-command` and `call-process` were proved. A `--load` file is named
    by the user and is not read here.
    """
    found: List[str] = []
    index = 1
    while index < len(command):
        word = command[index]
        name, eq, value = word.partition("=")
        body = ""
        if name in {"--eval", "--execute"}:
            if eq:
                body = value
            elif index + 1 < len(command):
                body = command[index + 1]
                index += 1
        if body:
            found.extend(_elisp_shells(_plain(body)))
        index += 1
    return found


def _elisp_shells(text: str) -> List[str]:
    found: List[str] = []
    for match in _EMACS_SHELL.finditer(text):
        body = next(group for group in match.groups() if group is not None)
        body = body.replace('\\"', '"').replace("\\'", "'").strip()
        if body:
            found.append(body)
    start = 0
    needle = "(call-process"
    while True:
        at = text.find(needle, start)
        if at < 0:
            break
        # `(call-process "sudo" nil nil nil "reboot")` joins the strings.
        # `nil` is not a string, so the program and its arguments remain.
        args = _lisp_call_strings(text, at + len(needle))
        if args:
            found.append(" ".join(args))
        start = at + len(needle)
    return found


def _lisp_call_strings(text: str, start: int) -> List[str]:
    """String literals up to the matching close paren. `nil` is skipped."""
    found: List[str] = []
    depth = 1
    index = start
    limit = len(text)
    while index < limit and depth:
        char = text[index]
        if char in "'\"":
            end = index + 1
            buf: List[str] = []
            while end < limit and text[end] != char:
                if text[end] == "\\" and end + 1 < limit:
                    buf.append(text[end + 1])
                    end += 2
                    continue
                buf.append(text[end])
                end += 1
            found.append("".join(buf))
            index = end + 1
            continue
        if char == "(":
            depth += 1
        elif char == ")":
            depth -= 1
        index += 1
    return [item for item in found if item.strip()]


_ROFF_REQUEST = re.compile(r"(?m)^[ \t]*\.(?:sy|pso)[ \t]+(.+?)\s*$")


def _roff_commands(
    command: Sequence[str],
    heredocs: Sequence[Tuple[Sequence[str], str]],
) -> List[str]:
    """`.sy` / `.pso` lines in a heredoc fed to `groff -U`.

    A named document stays visible input. Safer mode does not run these
    requests, which was proved without `-U`. A pipe is refused separately
    because its document is not in the command.
    """
    execution = _execution()
    if not execution._roff_unsafe(list(command)):
        return []
    if execution._roff_has_file(list(command)):
        return []
    found: List[str] = []
    for words, body in heredocs:
        if not words or _basename(words[0]) != _basename(command[0]):
            continue
        if not execution._roff_unsafe(list(words)):
            continue
        for match in _ROFF_REQUEST.finditer(body):
            shell = match.group(1).strip()
            if shell:
                found.append(shell)
    return found


def _package_exec_tail(program: str, args: Sequence[str]) -> List[str]:
    """The command after `exec`, including `pnpm m exec`. Empty when it is not exec."""
    from tiancode.core.mcp_client import (
        _NPM_SHORT_VALUES,
        _NPM_VALUE_FLAGS,
        _PNPM_LONG_VALUES,
        _PNPM_SHORT_VALUES,
        _YARN_VALUE_FLAGS,
        _canonicalize_manager,
    )

    if program == "npm":
        longs, shorts = _NPM_VALUE_FLAGS, _NPM_SHORT_VALUES
    elif program == "pnpm":
        longs, shorts = _PNPM_LONG_VALUES, _PNPM_SHORT_VALUES
    else:
        longs, shorts = _YARN_VALUE_FLAGS, frozenset()
    positionals: List[Tuple[int, str]] = []
    index = 0
    args = list(args)
    while index < len(args):
        arg = args[index]
        if arg == "--":
            break
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
        positionals.append((index, arg))
        index += 1
    if not positionals:
        return []
    first = _canonicalize_manager(program, positionals[0][1])
    start: Optional[int] = None
    if first == "exec":
        start = positionals[0][0] + 1
    elif (
        program == "pnpm"
        and first in {"recursive", "m", "multi"}
        and len(positionals) >= 2
        and _canonicalize_manager(program, positionals[1][1]) == "exec"
    ):
        start = positionals[1][0] + 1
    if start is None:
        return []
    rest = args[start:]
    if rest[:1] == ["--"]:
        rest = rest[1:]
    return [word for word in rest if word]
