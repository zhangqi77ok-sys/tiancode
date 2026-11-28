"""Commands that a safe-looking argv actually runs.

`timeout 5 sudo` is peeled before the danger patterns run. These are the
cases that stay hidden after that peel: `sed` executes the `e` flag,
`sqlite3 .shell` runs a shell, `git -c` and `git config` plant a shell,
`find -exec` and `xargs` run the utility they name, `ansible -a` is a
command, `ssh -o ProxyCommand` runs on this machine, and `tar --to-command`,
`-I`, and `--checkpoint-action=exec` run a program. `sort --compress-program`
and `scp -S` exec a program. `env --split-string` runs its string.
`vim -c`, `emacs --eval`, and a `groff -U` heredoc run a shell that is not
the outer command. `pic -U` and `groff -U -p` run a `sh` request from a
heredoc. `sftp -S` and `sftp -D` exec a program, `tar --rsh-command` does
too when the archive is remote, `zip -T -TT` runs a shell, and
`gcc -wrapper` execs its wrapper. `pnpm exec` forwards its tail. `perl -d`
runs `PERL5DB`, and so does `prove` when `PERL5OPT=-d`. `go build
-toolexec` and `go test`/`go run -exec` exec that program; `go build -n`
still runs `-toolexec`, while `-n` and `go test -c` do not run `-exec`.
`git config --edit`, `notes append`/`edit`, `merge`/`cherry-pick`/`revert
--edit`, and `filter-branch --*-filter` run a shell. Signing runs
`gpg.program`, `gpg.ssh.program`, or `gpg.x509.program` for `commit`, `tag`,
`cherry-pick`, `revert`, `merge`, `rebase`, and `am`. `trailer.<name>.cmd`
runs for `commit --trailer` and `interpret-trailers`. `difftool.<tool>.cmd`
runs when the prompt is off. A pipe whose program
is not in the text (`sed e`, `.shell` with no command, `pic -U`) is unseen.
The caller checks each returned string with the same danger rules. This is
not a language parser.
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
    config = effective_git_config(words, assignments, file_config, parent_env)
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
        if program in {"scp", "sftp"}:
            found.extend(_scp_program(command))
        if program == "sftp":
            found.extend(_sftp_server(command))
        return found, False
    if program == "tar":
        return _tar_commands(command), False
    if program == "zip":
        return _zip_commands(command), False
    if program in {"gcc", "g++"} or program.endswith("-gcc") or program.endswith("-g++"):
        return _compiler_wrappers(command), False
    if program == "pic":
        return _pic_commands(command, heredocs), False
    if program == "perl":
        return _perl_debug_commands(command, assignments), False
    if program == "prove":
        # prove is a perl program. Its own flags are not `perl -d`; the
        # debugger still starts when PERL5OPT asks for it.
        return _perl_debug_commands(["perl"], assignments), False
    if program == "go":
        return commands_go_will_run(command, assignments), False
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


def effective_git_config(
    words: Sequence[str],
    assignments: Sequence[str],
    file_config: Optional[Dict[str, str]] = None,
    parent_env: Optional[Dict[str, str]] = None,
) -> Dict[str, str]:
    """Config git will see: on-disk, then `GIT_CONFIG_*`, then `-c`.

    Later layers replace earlier ones. Repeated credential helpers are not
    stored here; the credential path keeps every value.
    """
    env = dict(parent_env or {})
    for name, value in _assignment_pairs(assignments):
        if name.startswith("GIT_CONFIG_"):
            env[name] = value
    config = {key.lower(): value for key, value in (file_config or {}).items()}
    config.update(_slots_from_env(env))
    config.update(_inline_git_config(words))
    return config


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


def _env_override(name: str, assignments: Sequence[str], parent_env: Dict[str, str]) -> Optional[str]:
    """The assignment in this command, otherwise the parent environment.

    None means the variable was not set. An empty string was set and wins
    over a later fallback, the same way an empty `GIT_EXTERNAL_DIFF` does.
    """
    chosen: Optional[str] = None
    for key, value in _assignment_pairs(assignments):
        if key == name:
            chosen = value
    if chosen is None and name in parent_env:
        chosen = parent_env[name]
    return chosen


def _resolved_editor(
    config: Dict[str, str],
    assignments: Sequence[str],
    parent_env: Dict[str, str],
) -> str:
    """The editor git runs on a dumb terminal.

    `GIT_EDITOR` beats `core.editor`, which beats `EDITOR`. `VISUAL` was not
    used when the terminal was dumb, so it is not consulted here.
    """
    git_editor = _env_override("GIT_EDITOR", assignments, parent_env)
    if git_editor is not None and git_editor.strip():
        return git_editor.strip()
    configured = config.get("core.editor", "").strip()
    if configured:
        return configured
    editor = _env_override("EDITOR", assignments, parent_env)
    if editor is not None and editor.strip():
        return editor.strip()
    return ""


def _resolved_sequence_editor(
    config: Dict[str, str],
    assignments: Sequence[str],
    parent_env: Dict[str, str],
) -> str:
    """The editor `git rebase -i` opens before it falls back to the commit editor."""
    sequence = _env_override("GIT_SEQUENCE_EDITOR", assignments, parent_env)
    if sequence is not None and sequence.strip():
        return sequence.strip()
    configured = config.get("sequence.editor", "").strip()
    if configured:
        return configured
    return _resolved_editor(config, assignments, parent_env)


def _git_bool(value: str) -> bool:
    return value.strip().lower() in {"true", "yes", "on", "1"}


def _flag_present(args: Sequence[str], names: frozenset) -> bool:
    for word in args:
        if word == "--":
            return False
        name = word.split("=", 1)[0]
        if name in names:
            return True
        if (
            len(name) == 2
            and name.startswith("-")
            and not name.startswith("--")
            and name in names
        ):
            return True
    return False


def _rebase_is_interactive(words: Sequence[str]) -> bool:
    if _git_subcommand(words) != "rebase":
        return False
    return _flag_present(_git_args(words), frozenset({"-i", "--interactive"}))


def _tag_uses_editor(words: Sequence[str]) -> bool:
    """Annotated or signed tags open the editor unless `-m` or `-F` gave a message.

    `git tag -a` and `git tag -s` were proved. `git tag -a -m` was not.
    """
    if _git_subcommand(words) != "tag":
        return False
    annotated = False
    has_message = False
    index = 0
    args = _git_args(words)
    while index < len(args):
        word = args[index]
        if word == "--":
            break
        name, eq, _value = word.partition("=")
        if name in {"-a", "--annotate", "-s", "--sign"}:
            annotated = True
        if name in {"-u", "--local-user"}:
            annotated = True
            if not eq:
                index += 2
                continue
        if name in {"-m", "--message", "-F", "--file"}:
            has_message = True
            if not eq:
                index += 2
                continue
        index += 1
    return annotated and not has_message


def _notes_uses_editor(words: Sequence[str]) -> bool:
    """`notes add` and `notes append` open the editor unless `-m` gave a message.

    `notes edit` always opens it. `notes add -m` and `notes append -m` do not.
    `notes show` does not.
    """
    if _git_subcommand(words) != "notes":
        return False
    action = ""
    has_message = False
    index = 0
    args = _git_args(words)
    while index < len(args):
        word = args[index]
        if word == "--":
            break
        name, eq, _value = word.partition("=")
        if name in {"-m", "--message", "-F", "--file", "-c", "--reuse-message", "-C", "--reedit-message"}:
            has_message = True
            if not eq and name in {"-m", "--message", "-F", "--file", "-c", "--reuse-message", "-C"}:
                index += 2
                continue
        if word.startswith("-"):
            index += 1
            continue
        if not action:
            action = _plain(word)
        index += 1
    if action == "edit":
        return True
    return action in {"add", "append"} and not has_message


def _trailing_edit(args: Sequence[str]) -> bool:
    """Whether the last of `--edit` / `-e` / `--no-edit` asks for an editor.

    The last flag wins. `merge --edit --no-edit` does not open one, and
    `merge --no-edit --edit` does.
    """
    editing = False
    for word in args:
        if word == "--":
            break
        name = word.split("=", 1)[0]
        if name in {"-e", "--edit"}:
            editing = True
        elif name == "--no-edit":
            editing = False
    return editing


def _config_uses_editor(words: Sequence[str]) -> bool:
    """`git config --edit` and `git config -e` open the editor."""
    if _git_subcommand(words) != "config":
        return False
    for word in _git_args(words):
        if word == "--":
            break
        if word.split("=", 1)[0] in {"-e", "--edit"}:
            return True
    return False


def _merge_uses_editor(words: Sequence[str]) -> bool:
    """`git merge --edit` opens the editor when a merge commit is created.

    `--ff-only` never creates one, so the editor stays closed. A fast-forward
    without `--ff-only` also stays closed, but that depends on ancestry this
    check does not compute. `--edit` is still reported unless `--ff-only` is set.
    `--no-edit` after `--edit` turns it back off.
    """
    if _git_subcommand(words) != "merge":
        return False
    args = _git_args(words)
    if _flag_present(args, frozenset({"--ff-only"})):
        return False
    return _trailing_edit(args)


def _cherry_pick_uses_editor(words: Sequence[str]) -> bool:
    """`git cherry-pick -e` / `--edit` opens the editor. A plain pick does not."""
    if _git_subcommand(words) != "cherry-pick":
        return False
    return _trailing_edit(_git_args(words))


def _revert_uses_editor(words: Sequence[str]) -> bool:
    """`git revert -e` / `--edit` opens the editor. `--no-edit` does not."""
    if _git_subcommand(words) != "revert":
        return False
    return _trailing_edit(_git_args(words))


_FILTER_BRANCH = frozenset({
    "--setup", "--env-filter", "--tree-filter", "--index-filter",
    "--parent-filter", "--msg-filter", "--commit-filter", "--tag-name-filter",
})


def _filter_branch_commands(words: Sequence[str]) -> List[str]:
    """Shell strings `git filter-branch` evals. A subdirectory filter is a path.

    Each of these was proved with a marker. The default message filter is
    `cat` and the default commit filter is `git commit-tree`, so only an
    explicit value is returned.
    """
    if _git_subcommand(words) != "filter-branch":
        return []
    found: List[str] = []
    index = 0
    args = _git_args(words)
    while index < len(args):
        word = args[index]
        if word == "--":
            break
        name, eq, value = word.partition("=")
        if name in _FILTER_BRANCH:
            if eq:
                body = value
            elif index + 1 < len(args):
                body = args[index + 1]
                index += 1
            else:
                body = ""
            body = _plain(body).strip()
            if body:
                found.append(body)
        index += 1
    return found


def _rebase_execs(words: Sequence[str]) -> List[str]:
    """Shell commands from `git rebase --exec` / `-x`. Each one was proved to run."""
    if _git_subcommand(words) != "rebase":
        return []
    found: List[str] = []
    index = 0
    args = _git_args(words)
    while index < len(args):
        word = args[index]
        if word == "--":
            break
        name, eq, value = word.partition("=")
        if name == "--exec":
            if eq:
                body = value
            elif index + 1 < len(args):
                body = args[index + 1]
                index += 1
            else:
                body = ""
            body = _plain(body).strip()
            if body:
                found.append(body)
        elif word == "-x" or (word.startswith("-x") and not word.startswith("--") and len(word) > 2):
            if word == "-x":
                body = args[index + 1] if index + 1 < len(args) else ""
                if word == "-x" and index + 1 < len(args):
                    index += 1
            else:
                body = word[2:]
            body = _plain(body).strip()
            if body:
                found.append(body)
        index += 1
    return found


def _bisect_run(words: Sequence[str]) -> str:
    """The command after `git bisect run`. Git execs those arguments."""
    if _git_subcommand(words) != "bisect":
        return ""
    args = [_plain(word) for word in _git_args(words)]
    if not args or args[0] != "run" or len(args) < 2:
        return ""
    return " ".join(args[1:]).strip()


def _difftool_extcmd(words: Sequence[str], config: Dict[str, str]) -> str:
    """The shell `git difftool` runs once the prompt is off.

    `--extcmd` / `-x` wins. Otherwise the command is `difftool.<tool>.cmd`,
    and `--tool` replaces `diff.tool`. Without `-y`, `--no-prompt`, or
    `difftool.prompt=false`, a non-tty does not launch the tool.
    """
    if _git_subcommand(words) != "difftool":
        return ""
    prompt = config.get("difftool.prompt", "").strip().lower() not in {"false", "no", "off", "0"}
    ext = ""
    tool = ""
    index = 0
    args = _git_args(words)
    while index < len(args):
        word = args[index]
        if word == "--":
            break
        name, eq, value = word.partition("=")
        if name in {"-y", "--no-prompt"}:
            prompt = False
        elif name == "--prompt":
            prompt = True
        elif name in {"--tool", "-t"} or (
            word.startswith("-t") and not word.startswith("--") and word != "-t"
        ):
            if name == "--tool" and eq:
                tool = value
            elif word.startswith("-t") and word != "-t" and not word.startswith("--"):
                tool = word[2:]
            elif index + 1 < len(args):
                tool = args[index + 1]
                index += 1
            else:
                tool = ""
        elif name == "--extcmd" or name == "-x" or (
            word.startswith("-x") and not word.startswith("--") and word != "-x"
        ):
            if name == "--extcmd" and eq:
                ext = value
            elif word.startswith("-x") and word != "-x" and not word.startswith("--"):
                ext = word[2:]
            elif index + 1 < len(args):
                ext = args[index + 1]
                index += 1
            else:
                ext = ""
        index += 1
    if prompt:
        return ""
    explicit = _plain(ext).strip()
    if explicit:
        return explicit
    chosen = _plain(tool).strip() or config.get("diff.tool", "").strip()
    if not chosen:
        return ""
    return config.get(f"difftool.{chosen.lower()}.cmd", "").strip()


def _gpg_sign_requested(args: Sequence[str]) -> Optional[bool]:
    """The last explicit sign flag, or None when the command did not say.

    `commit --no-gpg-sign -S` signs. `commit -S --no-gpg-sign` does not.
    """
    choice: Optional[bool] = None
    for word in args:
        if word == "--":
            break
        name = word.split("=", 1)[0]
        if name in {"-S", "--gpg-sign"} or (
            word.startswith("-S") and not word.startswith("--") and word != "-S"
        ):
            choice = True
        elif name == "--no-gpg-sign":
            choice = False
    return choice


def _commit_is_suppressed(args: Sequence[str]) -> bool:
    """True when `-n` / `--no-commit` is the last word on creating a commit.

    `cherry-pick -n -S` does not sign. `cherry-pick -n --commit` does, when
    signing was otherwise requested. `--commit` turns `-n` back off.
    """
    suppressed = False
    for word in args:
        if word == "--":
            break
        name = word.split("=", 1)[0]
        if name in {"-n", "--no-commit"}:
            suppressed = True
        elif name == "--commit":
            suppressed = False
    return suppressed


def _signs_from_commit_config(args: Sequence[str], config: Dict[str, str]) -> bool:
    choice = _gpg_sign_requested(args)
    if choice is not None:
        return choice
    return _git_bool(config.get("commit.gpgsign", ""))


_REBASE_NO_SIGN = frozenset({
    "--abort", "--quit", "--edit-todo", "--show-current-patch",
})


def _git_will_sign(words: Sequence[str], config: Dict[str, str]) -> bool:
    """True when this command invokes the configured signing program.

    `commit -S`, `commit.gpgsign`, and `tag -s` were proved. The same program
    runs for `cherry-pick`, `revert`, a merge that can create a commit,
    `rebase` while it is replaying, and `am`. `--no-commit` does not sign.
    `--ff-only` does not either. A fast-forward without `--ff-only` also
    stays unsigned, but that depends on ancestry this check does not compute,
    so a sign request is still reported unless `--ff-only` is set.
    `rebase --abort`, `--quit`, and `--edit-todo` do not sign.
    """
    sub = _git_subcommand(words)
    args = _git_args(words)
    if sub == "commit":
        return _signs_from_commit_config(args, config)
    if sub == "tag":
        return _flag_present(args, frozenset({"-s", "--sign", "-u", "--local-user"}))
    if sub in {"cherry-pick", "revert"}:
        if _flag_present(args, frozenset({"--abort", "--quit"})) or _commit_is_suppressed(args):
            return False
        return _signs_from_commit_config(args, config)
    if sub == "merge":
        if _flag_present(args, frozenset({"--abort", "--quit", "--ff-only"})):
            return False
        return _signs_from_commit_config(args, config)
    if sub == "rebase":
        if _flag_present(args, _REBASE_NO_SIGN):
            return False
        return _signs_from_commit_config(args, config)
    if sub == "am":
        if _flag_present(args, frozenset({"--abort", "--quit"})):
            return False
        return _signs_from_commit_config(args, config)
    return False


def _signing_program(words: Sequence[str], config: Dict[str, str]) -> str:
    """The program named for this signature format. An empty value is the default tool.

    `gpg.format=x509` runs `gpg.x509.program`, not `gpg.program`.
    `gpg.format=ssh` runs `gpg.ssh.program`. Anything else runs `gpg.program`.
    """
    if not _git_will_sign(words, config):
        return ""
    fmt = config.get("gpg.format", "").strip().lower()
    if fmt == "ssh":
        return config.get("gpg.ssh.program", "").strip()
    if fmt == "x509":
        return config.get("gpg.x509.program", "").strip()
    return config.get("gpg.program", "").strip()


def _interpret_trailers_ignores_config(args: Sequence[str]) -> bool:
    """`--parse` and `--only-input` skip trailer commands. The last flag wins."""
    skip = False
    for word in args:
        if word == "--":
            break
        name = word.split("=", 1)[0]
        if name in {"--parse", "--only-input"}:
            skip = True
        elif name == "--no-only-input":
            skip = False
    return skip


def _trailer_token(raw: str) -> str:
    text = _plain(raw).strip()
    for sep in (":", "="):
        at = text.find(sep)
        if at >= 0:
            text = text[:at]
            break
    return text.strip()


def _trailer_tokens(args: Sequence[str]) -> List[str]:
    found: List[str] = []
    index = 0
    while index < len(args):
        word = args[index]
        if word == "--":
            break
        name, eq, value = word.partition("=")
        if name != "--trailer":
            index += 1
            continue
        if eq:
            raw = value
        elif index + 1 < len(args):
            raw = args[index + 1]
            index += 1
        else:
            raw = ""
        token = _trailer_token(raw)
        if token:
            found.append(token)
        index += 1
    return found


def _trailer_commands(words: Sequence[str], config: Dict[str, str]) -> List[str]:
    """Shell strings `commit --trailer` and `interpret-trailers --trailer` run.

    `.cmd` replaces `.command`. An empty `.cmd` runs nothing. A token that is
    not named on the command line does not run, and `--only-input` / `--parse`
    do not either.
    """
    sub = _git_subcommand(words)
    if sub not in {"commit", "interpret-trailers"}:
        return []
    args = _git_args(words)
    if sub == "interpret-trailers" and _interpret_trailers_ignores_config(args):
        return []
    found: List[str] = []
    for token in _trailer_tokens(args):
        cmd_key = f"trailer.{token.lower()}.cmd"
        if cmd_key in config:
            body = config[cmd_key].strip()
        else:
            body = config.get(f"trailer.{token.lower()}.command", "").strip()
        if body:
            found.append(body)
    return found


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
    if sub == "commit" and _commit_uses_editor(words):
        editor = _resolved_editor(config, assignments, parent_env)
        if editor:
            found.append(editor)
    if _tag_uses_editor(words) or _notes_uses_editor(words):
        editor = _resolved_editor(config, assignments, parent_env)
        if editor:
            found.append(editor)
    if _rebase_is_interactive(words):
        editor = _resolved_sequence_editor(config, assignments, parent_env)
        if editor:
            found.append(editor)
    if sub == "diff":
        external = _git_external_diff(config, assignments, parent_env)
        if external:
            found.append(external)
    edits = (
        _config_uses_editor(words)
        or _merge_uses_editor(words)
        or _cherry_pick_uses_editor(words)
        or _revert_uses_editor(words)
    )
    if edits:
        editor = _resolved_editor(config, assignments, parent_env)
        if editor:
            found.append(editor)
    found.extend(_filter_branch_commands(words))
    found.extend(_rebase_execs(words))
    bisect = _bisect_run(words)
    if bisect:
        found.append(bisect)
    difftool = _difftool_extcmd(words, config)
    if difftool:
        found.append(difftool)
    signing = _signing_program(words, config)
    if signing:
        found.append(signing)
    found.extend(_trailer_commands(words, config))
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
    """Programs GNU tar runs for `--to-command`, the compress program, and rsh.

    `-z` selects gzip by itself and is not a shell string. `-I cmd` and
    `--use-compress-program` are. Both forms run, so each one is returned.
    `--rsh-command` execs only for a remote archive (`host:file`) and not
    when `--force-local` is set. A local archive leaves the program unused.
    """
    found: List[str] = []
    rsh = ""
    force_local = False
    archives: List[str] = []
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
        if name == "--rsh-command":
            if eq:
                rsh = _plain(value).strip()
                index += 1
            elif index + 1 < len(command):
                rsh = _plain(command[index + 1]).strip()
                index += 2
            else:
                return found
            continue
        if name == "--force-local":
            force_local = True
            index += 1
            continue
        if name == "--file":
            if eq:
                archives.append(_plain(value))
                index += 1
            elif index + 1 < len(command):
                archives.append(_plain(command[index + 1]))
                index += 2
            else:
                return found
            continue
        if word.startswith("-") and not word.startswith("--") and "f" in word[1:]:
            flag_at = word.find("f")
            attached = word[flag_at + 1 :]
            if attached:
                archives.append(_plain(attached))
                index += 1
            elif index + 1 < len(command):
                archives.append(_plain(command[index + 1]))
                index += 2
            else:
                return found
            continue
        index += 1
    if rsh and not force_local and any(":" in name for name in archives):
        found.append(rsh)
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


def _sftp_server(command: Sequence[str]) -> List[str]:
    """The local program `sftp -D` execs. It does not need a remote host."""
    index = 1
    while index < len(command):
        word = command[index]
        name, eq, value = word.partition("=")
        if word == "--":
            break
        if name == "-D" or (word.startswith("-D") and not word.startswith("--") and len(word) > 2):
            if word.startswith("-D") and word not in {"-D"} and not word.startswith("--"):
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


def _zip_commands(command: Sequence[str]) -> List[str]:
    """The shell `zip -T -TT cmd` runs instead of `unzip -tqq`.

    `-TT` without `-T` does not run. `-T` alone runs unzip, not `cmd`.
    The command is a shell string: `touch ran; true` was proved.
    """
    testing = False
    payload = ""
    index = 1
    while index < len(command):
        word = command[index]
        if word == "--":
            break
        if word == "-TT":
            if index + 1 < len(command):
                payload = _plain(command[index + 1]).strip()
                index += 2
                continue
            return []
        if word.startswith("-TT") and not word.startswith("--"):
            payload = _plain(word[3:]).strip()
            index += 1
            continue
        if word.startswith("-") and not word.startswith("--") and "T" in word[1:]:
            testing = True
        index += 1
    if testing and payload:
        return [payload]
    return []


def _compiler_wrappers(command: Sequence[str]) -> List[str]:
    """The program `gcc -wrapper` / `g++ -wrapper` execs before each subcommand.

    The value is a comma-separated argv, not a shell. `gcc -wrapper echo`
    stays allowed. `gcc -wrapper sudo,reboot` is sudo. No input file means
    gcc exits before the wrapper, which is an acceptable extra block.
    """
    found: List[str] = []
    index = 1
    while index < len(command):
        word = command[index]
        name, eq, value = word.partition("=")
        if name != "-wrapper":
            index += 1
            continue
        if eq:
            payload = value
            index += 1
        elif index + 1 < len(command):
            payload = command[index + 1]
            index += 2
        else:
            return found
        parts = [_plain(part).strip() for part in payload.split(",") if part.strip()]
        if parts:
            found.append(" ".join(parts))
    return found


_PIC_SH = re.compile(r"(?m)^[ \t]*sh[ \t]+(\S)(.*)\1[ \t]*$")


def _pic_sh_commands(text: str) -> List[str]:
    """Shell text of pic `sh X command X` lines inside a `.PS` / `.PE` picture.

    The delimiter is the character after `sh`. A `sh` line outside a picture
    is copied through and does not run, which was proved.
    """
    found: List[str] = []
    inside = False
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith(".PS"):
            inside = True
            continue
        if stripped.startswith(".PE"):
            inside = False
            continue
        if not inside:
            continue
        match = _PIC_SH.match(line)
        if match is None:
            continue
        shell = match.group(2).strip()
        if shell:
            found.append(shell)
    return found


def _pic_commands(
    command: Sequence[str],
    heredocs: Sequence[Tuple[Sequence[str], str]],
) -> List[str]:
    """`sh` lines in a heredoc fed to unsafe `pic`. A named file stays visible.

    `pic -U` runs the request. `pic -U -S` does not: the last of `-S` and
    `-U` wins. A pipe is refused separately because its document is not here.
    """
    execution = _execution()
    if not execution._pic_state(list(command))[1]:
        return []
    found: List[str] = []
    for words, body in heredocs:
        if not words or _basename(words[0]) != "pic":
            continue
        if not execution._pic_state(list(words))[1]:
            continue
        found.extend(_pic_sh_commands(body))
    return found


def _perl_switch_enables_debug(plain: str) -> bool:
    """True for `-d` / `-dt` and a cluster such as `-de`. `-d:Module` is another module.

    `-I` takes the rest of the cluster as a directory, so `-Idir` is not the debugger.
    """
    if plain in {"-d", "-dt"}:
        return True
    if not plain.startswith("-") or plain.startswith("--") or plain == "-" or ":" in plain:
        return False
    body = plain[1:]
    index = 0
    while index < len(body):
        char = body[index]
        if char == "d":
            return True
        # These switches consume the remainder as their argument.
        if char in {"I", "D", "F", "M", "m", "x"}:
            return False
        index += 1
    return False


def _perl_debug_enabled(command: Sequence[str], assignments: Sequence[str]) -> bool:
    """True for `perl -d` or `PERL5OPT=-d`. `perl -d:Module` loads that module instead."""
    for word in command[1:]:
        if _perl_switch_enables_debug(_plain(word)):
            return True
    for key, value in _assignment_pairs(assignments):
        if key != "PERL5OPT":
            continue
        for part in value.split():
            if _perl_switch_enables_debug(part):
                return True
    return False


def _perl_debug_commands(command: Sequence[str], assignments: Sequence[str]) -> List[str]:
    """Shell text inside `PERL5DB` when perl actually enters the debugger.

    The default debugger file is not a shell. `system("sudo reboot")` is.
    """
    if not _perl_debug_enabled(command, assignments):
        return []
    payload = ""
    for key, value in _assignment_pairs(assignments):
        if key == "PERL5DB":
            payload = value
    if not payload.strip():
        return []
    from tiancode.tools.command_scan import executed_literals

    return [piece.strip() for piece in executed_literals("perl", payload) if piece.strip()]


# `go test` and `go run` share `-exec`. Build commands share `-toolexec`.
# A flag in this set consumes the next word, so that word is not itself a flag.
_GO_VALUE_FLAGS = frozenset({
    "-C", "-o", "-p", "-covermode", "-coverpkg", "-asmflags", "-buildmode",
    "-compiler", "-gccgoflags", "-gcflags", "-installsuffix", "-ldflags",
    "-mod", "-modfile", "-overlay", "-pgo", "-pkgdir", "-tags", "-toolexec",
    "-exec", "-f", "-run", "-count", "-timeout", "-parallel", "-cpu", "-bench",
    "-benchtime", "-blockprofile", "-coverprofile", "-cpuprofile", "-memprofile",
    "-mutexprofile", "-outputdir", "-shuffle", "-skip", "-fuzz", "-fuzztime",
    "-vet",
})
_GO_BOOL_FLAGS = frozenset({"-n", "-c", "-json", "-compiled", "-export"})
_GO_TOOL_COMMANDS = frozenset({"build", "install", "run", "test", "vet", "list"})
_GO_EXEC_COMMANDS = frozenset({"run", "test"})
_GO_TRUE = frozenset({"1", "t", "true"})
_GO_FALSE = frozenset({"0", "f", "false"})


class _GoState:
    """The go flags that decide whether `-toolexec` or `-exec` actually runs."""

    def __init__(self) -> None:
        self.toolexec: Optional[str] = None
        self.exec_cmd: Optional[str] = None
        self.dry = False
        self.compile_only = False
        self.list_compiled = False
        self.list_export = False
        self.json_on = False
        self.json_fields: set = set()
        self.template = ""
        self.saw_template = False


def _go_flag_name(word: str) -> str:
    name = word.split("=", 1)[0]
    if name.startswith("--") and not name.startswith("---"):
        return "-" + name[2:]
    return name


def _go_bool(value: str) -> Optional[bool]:
    text = value.strip().lower()
    if text in _GO_TRUE:
        return True
    if text in _GO_FALSE:
        return False
    return None


def _go_quoted_split(text: str) -> Optional[List[str]]:
    """Split the way `cmd/internal/quoted.Split` does. None if a quote is open.

    Go execs that argv. It does not run a shell, and a semicolon stays one argument.
    """
    fields: List[str] = []
    index = 0
    limit = len(text)
    while index < limit:
        while index < limit and text[index] in " \t\n\r":
            index += 1
        if index >= limit:
            break
        if text[index] in "\"'":
            quote = text[index]
            end = text.find(quote, index + 1)
            if end < 0:
                return None
            fields.append(text[index + 1 : end])
            index = end + 1
            continue
        end = index
        while end < limit and text[end] not in " \t\n\r":
            end += 1
        fields.append(text[index:end])
        index = end
    return fields


def _go_apply(state: _GoState, name: str, value: Optional[str]) -> None:
    """Record one go flag. `value` is None when a boolean flag was bare."""
    if name == "-toolexec":
        state.toolexec = value or ""
        return
    if name == "-exec":
        state.exec_cmd = value or ""
        return
    if name == "-n":
        state.dry = True if value is None else _go_bool(value) is not False
        return
    if name == "-c":
        state.compile_only = True if value is None else _go_bool(value) is not False
        return
    if name == "-compiled":
        state.list_compiled = True if value is None else _go_bool(value) is not False
        return
    if name == "-export":
        state.list_export = True if value is None else _go_bool(value) is not False
        return
    if name == "-f":
        state.template = value or ""
        state.saw_template = True
        return
    if name != "-json":
        return
    # `-json` is a boolean flag. `-json` and `-json=true` keep the field set.
    # `-json=Name` adds fields and does not mean "every field".
    if value is None:
        state.json_on = True
        return
    parsed = _go_bool(value)
    if parsed is not None:
        state.json_on = parsed
        return
    state.json_on = True
    for field_name in value.split(","):
        if field_name:
            state.json_fields.add(field_name)


def _go_walk_args(state: _GoState, args: Sequence[str]) -> None:
    index = 0
    while index < len(args):
        word = args[index]
        if word in {"--", "-args"}:
            return
        if not word.startswith("-") or word == "-":
            index += 1
            continue
        name = _go_flag_name(word)
        _ignored, eq, value = word.partition("=")
        if name in {"-toolexec", "-exec", "-f"} or (
            name in _GO_VALUE_FLAGS and name not in _GO_BOOL_FLAGS
        ):
            if eq:
                _go_apply(state, name, value)
                index += 1
                continue
            if name in {"-toolexec", "-exec", "-f"}:
                nxt = args[index + 1] if index + 1 < len(args) else ""
                _go_apply(state, name, nxt)
                index += 2 if index + 1 < len(args) else 1
                continue
            index += 2 if index + 1 < len(args) else 1
            continue
        if name in _GO_BOOL_FLAGS or name in {"-n", "-c", "-json", "-compiled", "-export"}:
            _go_apply(state, name, value if eq else None)
            index += 1
            continue
        index += 1


def _go_apply_goflags(state: _GoState, raw: str) -> None:
    """Apply `$GOFLAGS`. A non-flag makes go exit before it runs anything.

    The whole value is ignored in that case: go prints the parse error and
    does not reach `-toolexec`.
    """
    parts = _go_quoted_split(raw)
    if parts is None:
        return
    parsed: List[Tuple[str, Optional[str]]] = []
    for part in parts:
        if (
            not part.startswith("-")
            or part in {"-", "--"}
            or part.startswith("---")
            or part.startswith("-=")
            or part.startswith("--=")
        ):
            return
        name = _go_flag_name(part)
        _ignored, eq, value = part.partition("=")
        if name in {"-toolexec", "-exec", "-f"} and not eq:
            return
        parsed.append((name, value if eq else None))
    for name, value in parsed:
        _go_apply(state, name, value)


def _go_subcommand(words: Sequence[str]) -> Tuple[str, List[str]]:
    index = 1
    while index < len(words):
        word = words[index]
        if word == "--":
            rest = list(words[index + 1 :])
            if not rest:
                return "", []
            return rest[0], rest[1:]
        name = _go_flag_name(word)
        _ignored, eq, _value = word.partition("=")
        if word.startswith("-"):
            if name == "-C" and not eq:
                index += 2
                continue
            index += 1
            continue
        return word, list(words[index + 1 :])
    return "", []


def _go_list_builds(state: _GoState) -> bool:
    """True when `go list` compiles, which is when `-toolexec` runs.

    Plain `go list` does not. `-compiled`, `-export`, `-json`, `-json=Stale`,
    and `-f '{{.Stale}}'` do. `-json=Name` and `-f '{{.Name}}'` do not.
    """
    if state.list_compiled or state.list_export:
        return True
    if state.json_on and (
        not state.json_fields
        or "Stale" in state.json_fields
        or "StaleReason" in state.json_fields
    ):
        return True
    return state.saw_template and ".Stale" in state.template


def _go_command_string(value: str) -> Optional[str]:
    """The argv go will exec, or None when the quote is broken and go exits."""
    if not value.strip():
        return ""
    parts = _go_quoted_split(value)
    if parts is None or not parts:
        return "" if parts is not None else None
    return _shell_join(parts)


def commands_go_will_run(
    words: Sequence[str],
    assignments: Sequence[str],
    parent_env: Optional[Dict[str, str]] = None,
) -> List[str]:
    """Programs `go -toolexec` and `go -exec` exec.

    The command line replaces the same flag in `GOFLAGS`, and an empty
    `-toolexec=` clears it. `-toolexec` still runs under `-n`. `-exec` does
    not run under `-n`, and `go test -c` does not run it either. `go list`
    runs `-toolexec` only when the listing compiles a package.
    """
    if not words or _basename(words[0]) != "go":
        return []
    sub, args = _go_subcommand(words)
    if sub not in _GO_TOOL_COMMANDS and sub not in _GO_EXEC_COMMANDS:
        return []
    # GOFLAGS is applied first. The command line then replaces the same flags,
    # which is the order `go` parses them.
    state = _GoState()
    goflags = _env_override("GOFLAGS", assignments, parent_env or {})
    if goflags:
        _go_apply_goflags(state, goflags)
    _go_walk_args(state, args)
    found: List[str] = []
    runs_tool = sub in {"build", "install", "run", "test", "vet"} or (
        sub == "list" and _go_list_builds(state)
    )
    if runs_tool and state.toolexec:
        rendered = _go_command_string(state.toolexec)
        if rendered:
            found.append(rendered)
    runs_exec = sub in _GO_EXEC_COMMANDS and not state.dry and not (
        sub == "test" and state.compile_only
    )
    if runs_exec and state.exec_cmd:
        rendered = _go_command_string(state.exec_cmd)
        if rendered:
            found.append(rendered)
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
        if execution._roff_runs_pic(list(words)):
            found.extend(_pic_sh_commands(body))
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
