#!/usr/bin/env python3
"""Offline exhaustive Grace review client; Python 3.10+, standard library only.

Requests: OPERATION --input JSON_FILE. Exact v1 request keys are in REQUESTS.
Artifact paths are explicit, local and absolute; ancestors may be links, the named item not. Coordinator-owned
directories must not be writable by untrusted users. Reviewer identity is supplied
by the calling harness, never extracted from the untrusted response.
"""

import argparse
import difflib
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import uuid


VERSION = "grace-audit-packet-v2"
# v3 adds fragments and index packets; v2 packets, their schemaVersion 1 states and reports keep their exact meaning.
FRAGMENT_VERSION = "grace-audit-packet-v3"
# inline-v1 plans keep identities and digests only: no rule body or Custom instruction on disk.
INLINE_VERSION = "grace-review-inline-v1"
# inline-v2 plans also select the v3 packets: partition absorbs size overflows on its own.
INLINE_FRAGMENT_VERSION = "grace-review-inline-v2"
CLIENT_VERSION = "1.4"
PACKET_LIMIT = 65536
PACKET_TARGET = 32768
ARTIFACT_LIMIT = 64 * 1024 * 1024
OUTPUT_LIMIT = 8192
# Serialized identities and empty groups of a context packet holding one entry, path excluded.
ENVELOPE_RESERVE = 1024
# Whole-packet references one packet lists; about 110 bytes each, so well under the target.
INDEX_FANOUT = 256
# A response is not a packet: bounding the obligations of a review packet leaves about 4 KiB per verdict.
RESPONSE_LIMIT = 1024 * 1024
REVIEW_OBLIGATIONS = 256
# Share of the 8 KiB output left to the reading items, whatever the length of their paths.
READING_PAGE_BYTES = 6144
# grace_record_validation accepts a 4 MiB body and 20000 input elements, envelope and up to 100 executions included;
# Both limits also count each check named once by those executions (see report_elements and report_bytes).
REPORT_LIMIT = 4 * 1024 * 1024 - 512 * 1024
REPORT_ELEMENTS = 18000
RESPONSE_KEYS = "reviewId packetId packetHash rulesDigests verdicts"
HASH = re.compile(r"[0-9a-f]{64}\Z")
# ASCII that JSON does not escape weighs one byte per character, so a run can be cut without measuring it.
UNESCAPED_RUN = re.compile(r'(?P<plain>[ !#-\[\]-~]+)|(?P<other>.)', re.DOTALL)
COMMIT = re.compile(r"[0-9a-f]{40}(?:[0-9a-f]{24})?\Z")
PACKET_KEYS = "reviewId packetId resolutionId packetHash maxPacketBytes files entries talentIds rulesDigests obligationIds dependencies"
REQUESTS = {
    "capture": "schemaVersion repository changeSource outputDirectory",
    "manifest": "schemaVersion capture outputDirectory",
    "partition": "schemaVersion manifest plan groups outputDirectory",
    "pack": "schemaVersion manifest plan assignments outputDirectory",
    "verify": "schemaVersion packet expected dependencies state range",
    "reading": "schemaVersion state reviewId offset count",
    "accept": "schemaVersion state expectedStateHash response reviewer submissionId arbitration repair",
    "invalidate": "schemaVersion state expectedStateHash current currentStateHash upstreamChanges attempts",
    "acknowledge": "schemaVersion state expectedStateHash submissionId reportId",
}


class Refusal(Exception):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code


def require(condition, code, message):
    if not condition:
        raise Refusal(code, message)


def closed(value, keys):
    require(type(value) is dict and set(value) == set(keys.split()),
            "schema", "Expected the documented closed object; check missing or unknown keys.")


def v1(value):
    require(type(value) is int and value == 1, "version", "Only schemaVersion 1 is supported.")


def v1_or_v2(value):
    require(type(value) is int and value in (1, 2), "version", "Only schemaVersion 1 or 2 is supported.")


def string(value):
    require(type(value) is str and value.strip() and "\0" not in value,
            "string", "Expected a nonblank string without NUL.")
    try:
        value.encode("utf-8", "strict")
    except UnicodeError:
        raise Refusal("utf8", "Invalid Unicode scalar; provide strict UTF-8.") from None
    return value


def digest(value):
    require(type(value) is str and HASH.fullmatch(value), "digest", "Expected bare lowercase SHA-256.")
    return value


def identifier(value):
    string(value)
    try:
        parsed = uuid.UUID(value)
    except ValueError:
        raise Refusal("uuid", "Expected a UUID identifier.") from None
    require(str(parsed) == value and parsed.version in range(1, 9), "uuid", "Expected a canonical UUID.")
    return value


def array(value):
    require(type(value) is list, "schema", "Expected an array.")
    return value


def ordered(values):
    array(values)
    for value in values:
        string(value)
    require(values == sorted(set(values)), "order", "Expected unique strings in Unicode scalar order.")
    return values


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encoded(value):
    try:
        return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False).encode("utf-8")
    except (UnicodeError, ValueError, TypeError, RecursionError):
        raise Refusal("json", "Object cannot be serialized as strict UTF-8 JSON.") from None


def unique_pairs(items):
    result = {}
    for key, value in items:
        require(key not in result, "duplicate_key", "Duplicate JSON keys are forbidden.")
        result[key] = value
    return result


def parse(data, limit=ARTIFACT_LIMIT):
    require(len(data) <= limit, "size", "Input exceeds its trusted byte limit; split the review explicitly.")
    try:
        value = json.loads(data.decode("utf-8", "strict"), object_pairs_hook=unique_pairs,
                           parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
        encoded(value)  # Reject escaped unpaired surrogates too.
        return value
    except (UnicodeError, ValueError, RecursionError):
        raise Refusal("json", "Expected unambiguous strict UTF-8 JSON, without nonfinite numbers.") from None


def local_path(value, exists=True):
    string(value)
    path = Path(value)
    require(path.is_absolute() and ".." not in path.parts, "path", "Use an absolute local path without traversal.")
    for item in reversed([path, *path.parents]):
        try:
            info = item.lstat()
        except FileNotFoundError:
            require(not exists and item == path, "path", "Parent directory or input is missing.")
            continue
        require(not stat.S_ISLNK(info.st_mode) and not getattr(info, "st_file_attributes", 0) & 0x400,
                "path", "This path is a link or reparse point; pass the real path it designates.")
        require(stat.S_ISDIR(info.st_mode) or (item == path and stat.S_ISREG(info.st_mode) and info.st_nlink == 1),
                "path", "Only ordinary files and directories are supported; no hardlinks.")
    return path


def canonical_parent(value):
    # Ancestors may be links (macOS /tmp); the named item stays unresolved, so anchored
    # opens still refuse a link there and any ancestor swapped after this point.
    if type(value) is not str or not os.path.isabs(value) or ".." in Path(value).parts:
        return value
    path = Path(value)
    return str(Path(os.path.realpath(path.parent)) / path.name)


def canonical_request(request):
    if type(request.get("repository")) is str and os.path.isabs(request["repository"]):
        request["repository"] = os.path.realpath(request["repository"])
    for key in ("outputDirectory", "capture", "manifest", "plan", "state", "current", "packet", "response"):
        if key in request:
            request[key] = canonical_parent(request[key])
    for parent, key in (("expected", "plan"), ("repair", "original")):
        if type(request.get(parent)) is dict and key in request[parent]:
            request[parent][key] = canonical_parent(request[parent][key])
    if type(request.get("dependencies")) is list:
        for dependency in request["dependencies"]:
            if type(dependency) is dict and "path" in dependency:
                dependency["path"] = canonical_parent(dependency["path"])
    return request


def windows_handle(path, directory=False, create=False, write=False):
    # Denying FILE_SHARE_DELETE pins each opened component against rename/removal.
    # OPEN_REPARSE_POINT opens the link itself, which is rejected before use.
    import ctypes
    from ctypes import wintypes
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, ctypes.c_void_p,
                                   wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
    kernel.CreateFileW.restype = wintypes.HANDLE
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.CloseHandle.restype = wintypes.BOOL
    kernel.GetFileInformationByHandleEx.argtypes = [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD]
    kernel.GetFileInformationByHandleEx.restype = wintypes.BOOL
    class Attributes(ctypes.Structure):
        _fields_ = [("attributes", wintypes.DWORD), ("tag", wintypes.DWORD)]
    access = 0x80 if directory else (0x40000000 if write else 0x80000000)
    handle = kernel.CreateFileW(str(path), access, 3, None, 1 if create else 3, 0x00200000 | 0x02000000, None)
    if handle == wintypes.HANDLE(-1).value:
        raise ctypes.WinError(ctypes.get_last_error())
    try:
        info = Attributes()
        if not kernel.GetFileInformationByHandleEx(handle, 9, ctypes.byref(info), ctypes.sizeof(info)):
            raise ctypes.WinError(ctypes.get_last_error())
        require(not info.attributes & 0x400 and bool(info.attributes & 0x10) == directory,
                "path", "Reparse points or unexpected file types require manual review.")
    except BaseException:
        kernel.CloseHandle(handle)
        raise
    return handle, kernel


@contextmanager
def anchored_parent(value):
    path = Path(value)
    string(str(value))
    require(path.is_absolute() and ".." not in path.parts, "path", "Use an absolute local path without traversal.")
    if os.name == "nt":
        require(len(path.drive) == 2 and path.drive[1] == ":", "path", "Only local drive paths are supported, never network/device paths.")
        reserved = {"CON", "PRN", "AUX", "NUL", *("COM" + str(n) for n in range(1, 10)), *("LPT" + str(n) for n in range(1, 10))}
        require(all(":" not in part and not part.endswith((" ", ".")) and part.split(".")[0].upper() not in reserved
                    for part in path.parts[1:]), "path", "Windows device names, streams and ambiguous components are forbidden.")
        handles = []
        try:
            for parent in reversed(path.parents):
                handles.append(windows_handle(parent, directory=True))
            yield path, {}
        finally:
            for handle, kernel in reversed(handles):
                kernel.CloseHandle(handle)
    else:
        require(os.open in os.supports_dir_fd and hasattr(os, "O_NOFOLLOW") and hasattr(os, "O_DIRECTORY"),
                "capability", "Anchored filesystem operations unavailable; manual review required.")
        fd = os.open(path.anchor, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            for part in path.parts[1:-1]:
                next_fd = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
                os.close(fd); fd = next_fd
            yield path.name, {"dir_fd": fd}
        finally:
            os.close(fd)


def open_anchored(name, parent, write=False, create=False):
    if os.name == "nt":
        import msvcrt
        handle, kernel = windows_handle(name, create=create, write=write)
        try:
            return msvcrt.open_osfhandle(handle, os.O_BINARY | os.O_NOINHERIT)
        except BaseException:
            kernel.CloseHandle(handle)
            raise
    flags = (os.O_WRONLY if write else os.O_RDONLY) | os.O_NOFOLLOW | os.O_NONBLOCK
    if create:
        flags |= os.O_CREAT | os.O_EXCL
    return os.open(name, flags, 0o600, **parent)


def read_anchored(name, parent, limit):
    fd = open_anchored(name, parent)
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_nlink == 1, "path", "Input must be an ordinary unlinked file.")
        require(info.st_size <= limit, "size", "Input exceeds its trusted byte limit before parsing.")
        data = stream.read(limit + 1)
    require(len(data) <= limit, "size", "Input grew beyond its trusted byte limit.")
    return data


def read_bytes(value, limit=ARTIFACT_LIMIT):
    with anchored_parent(value) as (name, parent):
        return read_anchored(name, parent, limit)


def read_json(path, limit=ARTIFACT_LIMIT):
    return parse(read_bytes(path, limit), limit)


def new_directory(value):
    path = local_path(value, exists=False)
    with anchored_parent(path) as (name, parent):
        os.mkdir(name, mode=0o700, **parent)  # Existing output is never replaced.
    return path


def write_new(path, data, readonly=False):
    require(len(data) <= ARTIFACT_LIMIT, "size", "Artifact exceeds local byte limit.")
    with anchored_parent(path) as (name, parent):
        fd = open_anchored(name, parent, write=True, create=True)
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
            if readonly:
                if os.name == "nt":
                    # The open non-delete-shared handle pins the final name too.
                    os.chmod(name, 0o400)
                else:
                    os.fchmod(stream.fileno(), 0o400)
    return {"path": str(path), "bytes": len(data), "sha256": sha(data)}


def frame(value):
    data = str(value).encode("utf-8", "strict")
    return str(len(data)).encode("ascii") + b"\0" + data


def group(name, values):
    return frame(name) + frame(len(values)) + b"".join(frame(v) for v in values)


def entry_id(entry):
    return "m-" + sha(b"".join(frame(entry[k]) for k in ("file", "kind", "content")))


def packet_id(obligations):
    return "p-" + sha(group("obligationIds", sorted(obligations)))


def context_packet_id(entry_ids):
    return "p-" + sha(group("entryIds", sorted(entry_ids)))


def index_packet_id(packet_ids):
    return "p-" + sha(group("dependencyPacketIds", sorted(packet_ids)))


def fragment_id(item):
    parent = item["parent"]
    return "f-" + sha(b"".join(frame(v) for v in (*(parent[k] for k in ("entryId", "kind", "bytes", "sha256", "index", "count", "offset")),
                                                  item["file"], item["content"])))


def packet_hash(packet):
    data = b"".join(frame(v) for v in (packet.get("version", VERSION), packet["resolutionId"], packet["packetId"], PACKET_LIMIT))
    data += group("files", packet["files"])
    data += frame("entries") + frame(len(packet["entries"]))
    data += b"".join(frame(e[k]) for e in packet["entries"] for k in ("entryId", "file", "kind", "content"))
    data += group("talentIds", packet["talentIds"])
    data += frame("rulesDigests") + frame(len(packet["rulesDigests"]))
    data += b"".join(frame(d[k]) for d in packet["rulesDigests"] for k in ("talentId", "rulesDigest"))
    data += group("obligationIds", packet["obligationIds"])
    data += frame("dependencies") + frame(len(packet["dependencies"]))
    data += b"".join(frame(d["packetId"]) + group("entryIds", d["entryIds"]) + group("obligationIds", d["obligationIds"])
                     for d in packet["dependencies"])
    return sha(data)


def git_path(value):
    string(value)
    require(not value.startswith(("/", "\\")) and "\\" not in value and ":" not in value
            and all(p not in ("", ".", "..", ".git") for p in value.split("/")),
            "git_path", "Ambiguous or unsafe Git path; use manual review.")
    return value


def decode_git_name(value):
    if not value.startswith('"'):
        return value
    require(value.endswith('"'), "git_path", "Unterminated Git quoted path.")
    raw = bytearray()
    body = value[1:-1]
    i = 0
    escapes = {"a": 7, "b": 8, "t": 9, "n": 10, "v": 11, "f": 12, "r": 13, '"': 34, "\\": 92}
    while i < len(body):
        c = body[i]
        if c != "\\":
            raw.extend(c.encode("utf-8")); i += 1
        else:
            i += 1
            require(i < len(body), "git_path", "Incomplete Git escape.")
            if body[i] in escapes:
                raw.append(escapes[body[i]]); i += 1
            else:
                octal = body[i:i + 3]
                require(len(octal) == 3 and all(c in "01234567" for c in octal) and int(octal, 8) <= 255,
                        "git_path", "Unsupported Git path escape.")
                raw.append(int(octal, 8)); i += 3
    try:
        return raw.decode("utf-8", "strict")
    except UnicodeError:
        raise Refusal("git_path", "Git path is not strict UTF-8.") from None


def header_paths(line):
    body = line.removeprefix("diff --git ").rstrip("\n")
    # Git does not quote spaces, so accept exactly one unambiguous a/ -> b/ split.
    candidates = []
    for match in re.finditer(r' (?=b/|"b/)', body):
        try:
            a, b = decode_git_name(body[:match.start()]), decode_git_name(body[match.end():])
            if a.startswith("a/") and b.startswith("b/"):
                candidates.append((git_path(a[2:]), git_path(b[2:])))
        except Refusal:
            pass
    require(len(candidates) == 1, "git_path", "Ambiguous diff header path; manual review required.")
    return candidates[0]


def manifest_entries(data):
    try:
        text = data.decode("utf-8", "strict")
    except UnicodeError:
        raise Refusal("utf8", "Diff is not strict UTF-8; manual review required.") from None
    require("\0" not in text, "binary", "Binary diff is unsupported; manual review required.")
    # splitlines also treats Unicode separators as lines: only LF is a Git boundary.
    lines = text.split("\n")
    lines = [line + "\n" for line in lines[:-1]] + ([lines[-1]] if lines[-1] else [])
    blocks = []
    for line in lines:
        if line.startswith("diff --git "):
            blocks.append([line])
        else:
            require(bool(blocks), "diff", "Diff has data outside a Git file section.")
            blocks[-1].append(line)
    entries = []
    for block in blocks:
        source, destination = header_paths(block[0])
        chunks = [[]]
        for line in block:
            if line.startswith("@@ "):
                require(re.fullmatch(r"@@ -\d+(?:,\d+)? \+\d+(?:,\d+)? @@[^\n]*\n?", line) is not None,
                        "diff", "Malformed unified hunk header.")
                chunks.append([])
            elif line.startswith(("@@@", "GIT binary patch", "Binary files ")):
                raise Refusal("binary", "Binary or combined diff requires manual review.")
            chunks[-1].append(line)
        header = chunks[0]
        require(all(line.startswith(("diff --git ", "index ", "old mode ", "new mode ", "new file mode ",
                                     "deleted file mode ", "similarity index ", "dissimilarity index ",
                                     "rename from ", "rename to ", "copy from ", "copy to ", "--- ", "+++ "))
                    for line in header), "diff", "Unsupported data in diff header; manual review required.")
        for line in header:
            require(not re.search(r"(?:^| )160000(?:\s|$)", line),
                    "special_file", "Submodule changes require manual review.")
        deleted = any(line.startswith("deleted file mode ") for line in header)
        file = source if deleted else destination
        for prefix, expected in (("--- ", "/dev/null" if any(l.startswith("new file mode ") for l in header) else "a/" + source),
                                 ("+++ ", "/dev/null" if deleted else "b/" + destination)):
            names = [decode_git_name(line[len(prefix):].removesuffix("\n").removesuffix("\t")) for line in header if line.startswith(prefix)]
            require(len(names) <= 1 and (not names or names == [expected]), "git_path", "Header paths disagree.")
        for index, chunk in enumerate(chunks):
            if index:
                match = re.match(r"@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@", chunk[0])
                counts = [int(value) if value is not None else 1 for value in match.groups()]
                observed = [0, 0]
                previous_line = False
                for line in chunk[1:]:
                    if line == "\\ No newline at end of file\n":
                        require(previous_line, "diff", "Misplaced no-newline marker.")
                        previous_line = False
                        continue
                    require(line[:1] in ("+", "-", " "), "diff", "Malformed hunk body.")
                    observed[0] += line[0] in ("-", " ")
                    observed[1] += line[0] in ("+", " ")
                    previous_line = True
                require(observed == counts, "diff", "Hunk line counts disagree with its header.")
            content = "".join(chunk)
            entry = {"entryId": "", "file": file, "kind": "header" if index == 0 else "hunk", "content": content}
            entry["entryId"] = entry_id(entry)
            entries.append(entry)
    require(len({e["entryId"] for e in entries}) == len(entries), "duplicate_entry", "Identical manifest entries require manual review.")
    require(b"".join(e["content"].encode("utf-8") for e in entries) == data, "coverage", "Manifest does not cover exact diff bytes.")
    return entries


def git(repo, *args, accepted=(0,)):
    env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
    env.update(GIT_OPTIONAL_LOCKS="0", GIT_TERMINAL_PROMPT="0", GIT_CONFIG_NOSYSTEM="1", GIT_NO_LAZY_FETCH="1",
               GIT_ALLOW_PROTOCOL="")
    # No shell, external diff, textconv, pager or network operations.
    with subprocess.Popen(["git", "--no-replace-objects", "--no-pager", "-c", "core.quotePath=true", "-c", "core.fsmonitor=false",
                           "-c", "core.autocrlf=false", "-c", "core.filemode=true",
                           "-C", str(repo), *args], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env) as process:
        data = process.stdout.read(ARTIFACT_LIMIT + 1)
        if len(data) > ARTIFACT_LIMIT:
            process.kill()
        status = process.wait()
    require(len(data) <= ARTIFACT_LIMIT, "size", "Git output exceeds local artifact limit.")
    require(status in accepted, "git", "Git refused the capture; check repository, refs and index conflicts.")
    return data


def resolve_commit(repo, ref):
    string(ref)
    require(not ref.startswith("-"), "ref", "A Git ref cannot be an option.")
    value = git(repo, "rev-parse", "--verify", "--end-of-options", ref + "^{commit}").decode().strip()
    require(COMMIT.fullmatch(value), "ref", "Expected a complete commit identity.")
    return value


def git_index(repo):
    flags = git(repo, "ls-files", "-v", "-z").split(b"\0")
    require(all(not item or (item[:1].isupper() and item[:1] != b"S") for item in flags),
            "index_flags", "assume-unchanged or skip-worktree can hide changes; clear these flags or use manual review.")
    # ls-files exposes intent-to-add as a normal empty blob. Compare two explicit
    # index-only views; neither consults the live worktree or runs its filters.
    options = ("--cached", "--raw", "-z", "--no-abbrev", "--no-renames", "--no-ext-diff", "--no-textconv",
               "--ignore-submodules=none")
    visible = git(repo, "diff", *options, "--ita-visible-in-index", "HEAD", "--")
    invisible = git(repo, "diff", *options, "--ita-invisible-in-index", "HEAD", "--")
    require(visible == invisible, "intent_to_add",
            "Intent-to-add entries require staging actual content, clearing intent-to-add, or manual review.")
    index = git(repo, "ls-files", "--stage", "-z")
    files = {}
    for item in index.split(b"\0"):
        if not item:
            continue
        meta, name = item.split(b"\t", 1)
        mode, oid, stage = meta.split()
        require(stage == b"0", "conflict", "Unmerged index requires manual review.")
        path = git_path(name.decode("utf-8", "strict"))
        require(mode in (b"100644", b"100755", b"120000", b"160000") and COMMIT.fullmatch(oid.decode("ascii")),
                "index", "Unsupported index entry; manual review required.")
        files[path] = (mode, oid.decode("ascii"))
    return index, files


def blob_hash(data, oid):
    algorithm = "sha1" if len(oid) == 40 else "sha256"
    return hashlib.new(algorithm, b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()


def below_link(repo, name):
    current = repo
    for part in Path(name).parts[:-1]:
        current = current / part
        if current.is_symlink():
            return True
    return False


def worktree_item(repo, name):
    # What Git would record: a link is its declared target, never the content it points to.
    path = repo / name
    if below_link(repo, name) or not os.path.lexists(path):
        return None, None
    if path.is_symlink():
        return os.fsencode(os.readlink(path)), b"120000"
    data = read_bytes(str(path))
    return data, b"100755" if path.stat().st_mode & stat.S_IXUSR else b"100644"


def snapshot(repo, clean=False):
    index, entries = git_index(repo)
    files = {name: mode for name, (mode, _) in entries.items()}
    untracked = git(repo, "ls-files", "--others", "--exclude-standard", "-z").split(b"\0")
    names = sorted(git_path(n.decode("utf-8", "strict")) for n in untracked if n)
    for name in names:
        files[name] = b"100644"
    paths = sorted(files)
    for offset in range(0, len(paths), 256):
        attrs = git(repo, "check-attr", "-z", "filter", "text", "eol", "working-tree-encoding", "ident",
                    "--", *paths[offset:offset + 256]).split(b"\0")
        require(all(value in (b"unspecified", b"unset") for value in attrs[2::3]),
                "filter", "Git filters or text conversions can alter bytes/execute code; use manual review.")
    records = []
    for name, mode in sorted(files.items()):
        path = repo / name
        if below_link(repo, name) or not os.path.lexists(path):
            require(mode != b"160000", "submodule", "Missing submodule prevents complete capture; use manual review.")
            require(not clean, "submodule", "Dirty submodule has a missing tracked file; manual review required.")
            records.append([name, "missing"])
            continue
        if path.is_symlink():
            require(mode != b"160000", "submodule", "A submodule replaced by a link requires manual review.")
            target = os.fsencode(os.readlink(path))
            if clean:
                require(name in entries and mode == b"120000" and blob_hash(target, entries[name][1]) == entries[name][1],
                        "submodule", "Dirty submodule link requires manual review.")
            records.append([name, "link", sha(target)])
            continue
        local_path(str(path))
        if mode == b"160000":
            snapshot(path, clean=True)  # Raw blobs only; never status/diff against a live submodule worktree.
            subhead = resolve_commit(path, "HEAD")
            expected = entries[name][1]
            require(subhead == expected, "submodule", "Submodule HEAD differs from index; manual review required.")
            records.append([name, subhead])
        else:
            info = path.stat()
            data = read_bytes(str(path))
            if clean:
                require(name in entries and blob_hash(data, entries[name][1]) == entries[name][1]
                        and (b"100755" if info.st_mode & stat.S_IXUSR else b"100644") == mode,
                        "submodule", "Dirty submodule content or mode requires manual review.")
            records.append([name, info.st_mode, sha(data)])
    if clean:
        require(not names and not git(repo, "diff", *DIFF_OPTIONS, "--cached", "HEAD", "--"),
                "submodule", "Dirty submodule index/untracked files require manual review.")
    return {"head": resolve_commit(repo, "HEAD"), "index": sha(index), "worktree": sha(encoded(records)), "untracked": names}


DIFF_OPTIONS = ("--no-ext-diff", "--no-textconv", "--no-color", "--ignore-submodules=none", "--binary", "--full-index", "--src-prefix=a/", "--dst-prefix=b/", "--unified=3")


def raw_worktree_diff(repo):
    # The live index stat cache and attributes never decide whether bytes changed.
    _, entries = git_index(repo)
    links_supported = git(repo, "config", "--bool", "core.symlinks", accepted=(0, 1)).strip() != b"false"
    result = bytearray()
    for name, (mode, oid) in sorted(entries.items()):
        if mode == b"160000":
            continue  # snapshot recursively proves these clean, or refuses capture.
        new, new_mode = worktree_item(repo, name)
        new = b"" if new is None else new
        new_oid = "0" * len(oid) if new_mode is None else blob_hash(new, oid)
        # core.symlinks=false checks a link out as a file holding its target: same blob, no change.
        if new_oid == oid and (new_mode == mode or (mode == b"120000" and not links_supported)):
            continue
        old = git(repo, "cat-file", "blob", oid)
        require(blob_hash(old, oid) == oid, "object", "Index blob bytes disagree with their object identity.")
        require(b"\0" not in old and b"\0" not in new, "binary", "Changed binary requires manual review.")
        old_text, new_text = old.decode("utf-8", "strict"), new.decode("utf-8", "strict")
        a, b = json.dumps("a/" + name, ensure_ascii=False), json.dumps("b/" + name, ensure_ascii=False)
        header = f"diff --git {a} {b}\n"
        if new_mode is None:
            header += "deleted file mode " + mode.decode() + "\n"
        elif mode != new_mode:
            header += "old mode " + mode.decode() + "\nnew mode " + new_mode.decode() + "\n"
        header += "index " + oid + ".." + new_oid + (" " + mode.decode() if mode == new_mode else "") + "\n"
        if old != new:
            header += "--- " + a + "\n+++ " + ("/dev/null" if new_mode is None else b) + "\n"
        result.extend(header.encode())
        require(len(result) <= ARTIFACT_LIMIT, "size", "Captured diff exceeds local artifact limit.")
        def lines(text):
            pieces = text.split("\n")
            return [line + "\n" for line in pieces[:-1]] + ([pieces[-1]] if pieces[-1] else [])
        # unified_diff's two pathname lines are already represented above.
        patch = iter(difflib.unified_diff(lines(old_text), lines(new_text), n=3, lineterm="\n"))
        next(patch, None); next(patch, None)
        for line in patch:
            result.extend(line.encode())
            if not line.endswith("\n"):
                result.extend(b"\n\\ No newline at end of file\n")
            require(len(result) <= ARTIFACT_LIMIT, "size", "Captured diff exceeds local artifact limit.")
    return bytes(result)


def capture(request):
    try:
        return capture_change(request)
    except Refusal as error:
        if error.code != "size":
            raise
        raise Refusal("capture_limit", f"The captured change or a repository file exceeds the {ARTIFACT_LIMIT}-byte capture limit; the review stays incomplete and nothing was written. No automatic retry.") from None


def capture_change(request):
    repo = local_path(request["repository"])
    require(repo.is_dir() and Path(git(repo, "rev-parse", "--show-toplevel").decode().strip()) == repo,
            "repository", "Capture must target the repository root.")
    output = local_path(request["outputDirectory"], exists=False)
    require(not output.is_relative_to(repo), "output", "Keep review artifacts outside the captured repository.")
    source = request["changeSource"]
    require(type(source) is dict and source.get("kind") in ("local", "pr"), "source", "Specify local or pr provenance.")
    parts = []
    if source["kind"] == "local":
        closed(source, "kind baseCommit")
        base = resolve_commit(repo, source["baseCommit"])
        require(base == source["baseCommit"], "source", "Local baseCommit must be the full immutable commit.")
        before = snapshot(repo)
        parts = [("committed", git(repo, "diff", *DIFF_OPTIONS, base, before["head"], "--")),
                 ("index", git(repo, "diff", *DIFF_OPTIONS, "--cached", before["head"], "--")),
                 ("worktree", raw_worktree_diff(repo))]
        for name in before["untracked"]:
            content, item_mode = worktree_item(repo, name)
            require(content is not None, "concurrent_change", "Repository changed during capture; capture again after writers stop.")
            content.decode("utf-8", "strict")
            require(b"\0" not in content, "binary", "Untracked binary requires manual review.")
            a, b = json.dumps("a/" + name, ensure_ascii=False), json.dumps("b/" + name, ensure_ascii=False)
            header = f"diff --git {a} {b}\nnew file mode {item_mode.decode()}\n--- /dev/null\n+++ {b}\n"
            if content:
                lines = content.split(b"\n")
                count = len(lines) - (1 if not lines[-1] else 0)
                body = b"".join(b"+" + line + b"\n" for line in lines[:-1])
                if lines[-1]:
                    body += b"+" + lines[-1] + b"\n\\ No newline at end of file\n"
                header += f"@@ -0,0 +1,{count} @@\n"
                parts.append(("untracked", header.encode() + body))
            else:
                parts.append(("untracked", header.encode()))
        after = snapshot(repo)
        require(before == after, "concurrent_change", "Repository changed during capture; capture again after writers stop.")
        provenance = {"kind": "local", "repository": str(repo), "baseCommit": base, "headCommit": before["head"],
                      "before": before, "after": after}
    else:
        closed(source, "kind baseRef headRef baseCommit headCommit mergeBaseCommit")
        base, head = resolve_commit(repo, source["baseRef"]), resolve_commit(repo, source["headRef"])
        merge = git(repo, "merge-base", "--all", base, head).decode().splitlines()
        require(len(merge) == 1 and [base, head, merge[0]] == [source[k] for k in ("baseCommit", "headCommit", "mergeBaseCommit")],
                "source", "PR refs or merge base disagree with explicit provenance.")
        parts = [("pr", git(repo, "diff", *DIFF_OPTIONS, merge[0], head, "--"))]
        require(base == resolve_commit(repo, source["baseRef"]) and head == resolve_commit(repo, source["headRef"]),
                "concurrent_change", "PR refs changed during capture.")
        provenance = {**source, "repository": str(repo)}
    data = b"".join(part for _, part in parts)
    require(len(data) <= ARTIFACT_LIMIT, "size", "Captured diff exceeds local artifact limit.")
    manifest_entries(data)
    segments = []
    offset = 0
    for kind, part in parts:
        segments.append({"kind": kind, "offset": offset, "bytes": len(part), "sha256": sha(part)})
        offset += len(part)
    directory = new_directory(str(output))
    diff = write_new(directory / "full.diff", data, readonly=True)
    captured = {"schemaVersion": 1, "changeSource": provenance, "fullDiff": diff, "segments": segments}
    artifact = write_new(directory / "capture.json", encoded(captured))
    return [artifact], {"fullDiffHash": diff["sha256"], "bytes": len(data), "segments": len(segments)}


def check_descriptor(descriptor):
    closed(descriptor, "path bytes sha256")
    digest(descriptor["sha256"])
    require(type(descriptor["bytes"]) is int and 0 <= descriptor["bytes"] <= ARTIFACT_LIMIT, "size", "Invalid artifact size.")
    data = read_bytes(descriptor["path"])
    require(len(data) == descriptor["bytes"] and sha(data) == descriptor["sha256"], "integrity", "Artifact bytes or hash changed.")
    return data


def load_capture(path):
    captured = read_json(path)
    closed(captured, "schemaVersion changeSource fullDiff segments")
    v1(captured["schemaVersion"])
    data = check_descriptor(captured["fullDiff"])
    offset = 0
    for segment in array(captured["segments"]):
        closed(segment, "kind offset bytes sha256")
        require(segment["kind"] in ("committed", "index", "worktree", "untracked", "pr")
                and type(segment["offset"]) is int and segment["offset"] == offset
                and type(segment["bytes"]) is int and segment["bytes"] >= 0,
                "source", "Invalid provenance segment.")
        end = offset + segment["bytes"]
        require(end <= len(data) and sha(data[offset:end]) == digest(segment["sha256"]), "source", "Provenance range changed.")
        offset = end
    require(offset == len(data), "coverage", "Provenance does not cover full diff.")
    source = captured["changeSource"]
    require(type(source) is dict and source.get("kind") in ("local", "pr"), "source", "Missing change source.")
    if source["kind"] == "local":
        closed(source, "kind repository baseCommit headCommit before after")
        for snapshot_value in (source["before"], source["after"]):
            closed(snapshot_value, "head index worktree untracked")
            require(snapshot_value["head"] == source["headCommit"], "source", "Snapshot HEAD mismatch.")
            digest(snapshot_value["index"]); digest(snapshot_value["worktree"])
            for name in ordered(snapshot_value["untracked"]):
                git_path(name)
        require(source["before"] == source["after"], "source", "Capture records concurrent changes.")
    else:
        closed(source, "kind repository baseRef headRef baseCommit headCommit mergeBaseCommit")
        string(source["baseRef"]); string(source["headRef"])
        require(type(source["mergeBaseCommit"]) is str and COMMIT.fullmatch(source["mergeBaseCommit"]), "source", "Invalid merge base.")
    string(source["repository"])
    for key in ("baseCommit", "headCommit"):
        require(type(source[key]) is str and COMMIT.fullmatch(source[key]), "source", "Invalid source commit.")
    return captured, data


def stored_entries(entries):
    # full.diff already holds the bytes: repeating them would double the artifact and its escapes.
    return [{key: entry[key] for key in ("entryId", "file", "kind")} for entry in entries]


def manifest(request):
    captured, data = load_capture(request["capture"])
    entries = manifest_entries(data)
    result = {"schemaVersion": 2, "capture": request["capture"], "captureHash": sha(encoded(captured)), "entries": stored_entries(entries)}
    directory = new_directory(request["outputDirectory"])
    artifact = write_new(directory / "manifest.json", encoded(result))
    return [artifact], {"entries": len(entries), "fullDiffHash": captured["fullDiff"]["sha256"]}


def load_manifest(path):
    value = read_json(path)
    closed(value, "schemaVersion capture captureHash entries")
    v1_or_v2(value["schemaVersion"])
    captured, data = load_capture(value["capture"])
    require(sha(encoded(captured)) == digest(value["captureHash"]), "integrity", "Capture metadata changed.")
    entries = manifest_entries(data)
    require(value["entries"] == (entries if value["schemaVersion"] == 1 else stored_entries(entries)),
            "coverage", "Manifest differs from exact captured diff.")
    return {**value, "entries": entries}, captured


def validate_plan(plan):
    closed(plan, "schemaVersion resolutionId protocol sourceHash obligations")
    v1(plan["schemaVersion"])
    identifier(plan["resolutionId"])
    digest(plan["sourceHash"])
    protocol = plan["protocol"]
    inline = type(protocol) is dict and protocol.get("version") in (INLINE_VERSION, INLINE_FRAGMENT_VERSION)
    if inline:
        closed(protocol, "version digest")
        digest(protocol["digest"])
    else:
        closed(protocol, "version content digest")
        require(protocol["version"] == VERSION and sha(string(protocol["content"]).encode()) == digest(protocol["digest"]),
                "protocol", "Materialized protocol version/content digest mismatch.")
    obligations = {}
    talents = {}
    custom_identities = set()
    applied = {}
    for obligation in array(plan["obligations"]):
        require(type(obligation) is dict and obligation.get("kind") in ("grace", "custom"), "plan", "Unknown obligation kind.")
        common = "kind obligationId appliedExpertiseId severity"
        if obligation["kind"] == "grace":
            closed(obligation, common + " talentId ruleId rulesDigest")
            tid, rid = string(obligation["talentId"]), string(obligation["ruleId"])
            require(obligation["obligationId"] == tid + ":" + rid and ":" not in tid and ":" not in rid,
                    "plan", "Grace obligation identity mismatch.")
            value = digest(obligation["rulesDigest"])
            require(tid not in talents or talents[tid] == value, "plan", "Conflicting rules digests.")
            talents[tid] = value
            require(tid not in applied or applied[tid] == obligation["appliedExpertiseId"], "plan", "Talent has inconsistent applied identity.")
            applied[tid] = obligation["appliedExpertiseId"]
        else:
            closed(obligation, common + " expertiseId expertiseVersionId " + ("instructionDigest" if inline else "contentDigest instruction"))
            identifier(obligation["expertiseId"]); identifier(obligation["expertiseVersionId"])
            identity = (obligation["expertiseId"], obligation["expertiseVersionId"])
            require(identity not in custom_identities, "plan", "Duplicate Custom identity/version.")
            custom_identities.add(identity)
            require(obligation["obligationId"] == obligation["appliedExpertiseId"] and obligation["severity"] == "blocking",
                    "plan", "Custom identity or blocking severity mismatch.")
            if inline:
                digest(obligation["instructionDigest"])
            else:
                require(sha(string(obligation["instruction"]).encode()) == digest(obligation["contentDigest"]),
                        "plan", "Custom instruction digest mismatch.")
        identifier(obligation["appliedExpertiseId"])
        require(obligation["severity"] in ("blocking", "warning", "info"), "plan", "Unknown severity.")
        oid = string(obligation["obligationId"])
        require(oid not in obligations, "plan", "Duplicate expected obligation.")
        obligations[oid] = obligation
    require(bool(obligations), "plan", "No expected obligations; do not manufacture an empty review.")
    return obligations


def fragmented_protocol(plan):
    return plan["protocol"]["version"] == INLINE_FRAGMENT_VERSION


def closed_packet(packet):
    if type(packet) is dict and "version" in packet:
        closed(packet, PACKET_KEYS + " version")
        require(packet["version"] == FRAGMENT_VERSION, "version", "Unknown packet version.")
    else:
        closed(packet, PACKET_KEYS)


def packet_weight(entry):
    return len(encoded(entry)) + len(encoded(entry["file"])) + ENVELOPE_RESERVE


def entry_fits(entry):
    return packet_weight(entry) <= PACKET_LIMIT


def cut_text(text, budget):
    """Cuts between lines when possible, between Unicode scalars inside a longer line; budgets are serialized bytes.
    Past a quarter of the budget, a line whose digest starts with "00" also ends its piece: cuts follow the content, so an
    edit only moves the pieces around it."""
    pieces, current, size = [], [], 0
    def add(piece, weight):
        nonlocal size, current
        if current and size + weight > budget:
            pieces.append("".join(current))
            current, size = [], 0
        current.append(piece)
        size += weight
    parts = text.split("\n")
    for line in [part + "\n" for part in parts[:-1]] + ([parts[-1]] if parts[-1] else []):
        weight = len(encoded(line)) - 2
        if weight <= budget:
            add(line, weight)
            if size >= budget // 4 and sha(line.encode("utf-8")).startswith("00"):
                pieces.append("".join(current))
                current, size = [], 0
        else:
            for match in UNESCAPED_RUN.finditer(line):
                run = match.group()
                if match.lastgroup == "other":
                    add(run, len(encoded(run)) - 2)
                while match.lastgroup == "plain" and run:
                    room = budget - size if size < budget else budget
                    add(run[:room], len(run[:room]))
                    run = run[room:]
    if current:
        pieces.append("".join(current))
    return pieces


def reading_items(entries):
    """Whole entries when they fit one packet, otherwise fragments of the same exact bytes. Deterministic."""
    items = []
    for entry in entries:
        if entry_fits(entry):
            items.append(entry)
            continue
        raw = entry["content"].encode("utf-8")
        raw_digest = sha(raw)
        pieces = cut_text(entry["content"], max(1024, PACKET_TARGET - ENVELOPE_RESERVE - 512 - 2 * len(encoded(entry["file"]))))
        offset = 0
        for index, piece in enumerate(pieces):
            item = {"entryId": "", "file": entry["file"], "kind": "fragment", "content": piece,
                    "parent": {"entryId": entry["entryId"], "kind": entry["kind"], "bytes": len(raw), "sha256": raw_digest,
                               "index": index, "count": len(pieces), "offset": offset}}
            item["entryId"] = fragment_id(item)
            items.append(item)
            offset += len(piece.encode("utf-8"))
    return items


def item_catalog(items):
    return {item["entryId"]: (position, item) for position, item in enumerate(items)}


def validate_fragment(item):
    closed(item, "entryId file kind content parent")
    parent = item["parent"]
    closed(parent, "entryId kind bytes sha256 index count offset")
    string(parent["entryId"]); digest(parent["sha256"])
    require(parent["kind"] in ("header", "hunk") and all(type(parent[k]) is int and parent[k] >= 0 for k in ("bytes", "index", "count", "offset"))
            and parent["index"] < parent["count"], "fragment", "Invalid fragment origin.")
    require(type(item["content"]) is str and item["content"] and "\0" not in item["content"], "fragment", "Empty or invalid fragment content.")
    encoded(item["content"])
    require(fragment_id(item) == item["entryId"], "fragment", "Fragment identity mismatch.")


def assembled_entries(items):
    """Entries one reading completely holds; a partial, repeated or altered set of fragments refuses."""
    message = "Missing, altered, repeated or misplaced fragment; prepare the packets again."
    ids = [item["entryId"] for item in items]
    require(len(set(ids)) == len(ids), "fragment", message)
    whole = {item["entryId"] for item in items if item["kind"] != "fragment"}
    parents = {}
    for item in items:
        if item["kind"] == "fragment":
            parents.setdefault(item["parent"]["entryId"], []).append(item)
    for parent_id, fragments in parents.items():
        fragments = sorted(fragments, key=lambda f: f["parent"]["index"])
        first = fragments[0]["parent"]
        data = bytearray()
        for index, fragment in enumerate(fragments):
            parent = fragment["parent"]
            require(all(parent[k] == first[k] for k in ("entryId", "kind", "bytes", "sha256", "count")) and parent["index"] == index
                    and parent["offset"] == len(data) and fragment["file"] == fragments[0]["file"], "fragment", message)
            data.extend(fragment["content"].encode("utf-8"))
        require(len(fragments) == first["count"] and len(data) == first["bytes"] and sha(data) == first["sha256"] and parent_id not in whole
                and entry_id({"file": fragments[0]["file"], "kind": first["kind"], "content": data.decode("utf-8")}) == parent_id,
                "fragment", message)
    return whole | set(parents)


def read_items(packet, by_id):
    """The entries and fragments a reviewer of this packet must read, whole references followed transitively."""
    items = list(packet["entries"])
    for dependency in packet["dependencies"]:
        upstream = by_id[dependency["packetId"]]
        if dependency["entryIds"]:
            named = set(dependency["entryIds"])
            items.extend(e for e in upstream["entries"] if e["entryId"] in named)
        elif not dependency["obligationIds"]:
            items.extend(read_items(upstream, by_id))
    return items


def packet_closure(packet, by_id):
    reached, pending = {}, [packet]
    while pending:
        current = pending.pop()
        if current["packetId"] not in reached:
            reached[current["packetId"]] = current
            pending.extend(by_id[d["packetId"]] for d in current["dependencies"])
    return reached


def reading_digest(packet, by_id):
    return sha(group("reading", sorted(p["packetId"] + ":" + p["packetHash"] for p in packet_closure(packet, by_id).values())))


def reading_key(item):
    """What a reviewer reads in an entry or fragment: its file and text, hunk line numbers aside, so moved text stays read."""
    content = item["content"]
    if item["kind"] == "hunk" or item["kind"] == "fragment" and item["parent"]["kind"] == "hunk" and item["parent"]["index"] == 0:
        content = re.sub(r"\A@@ -[0-9]+(?:,[0-9]+)? \+[0-9]+(?:,[0-9]+)? @@", "@@ @@", content)
    return sha(frame(item["file"]) + frame(content))


def evidence_file(evidence):
    return re.fullmatch(r"(.+?):([1-9][0-9]*)(?::([1-9][0-9]*))?", evidence["location"]).group(1)


def reading_scope(state, packet, by_id, role):
    """What a reviewer must read. A primary view after a correction reads what changed since its baseline: the final verdicts
    of the last complete reading of the same checks, given as data. A second view, or a change touching most of the packets,
    reads everything."""
    closure = packet_closure(packet, by_id)
    complete = {"mode": "complete", "required": set(closure), "baseline": None, "digest": reading_digest(packet, by_id)}
    baseline = state.get("baselines", {}).get(packet["packetId"]) if role == "primary" else None
    if baseline is None:
        return complete
    known = set(baseline["read"])
    verdicts = response_for(state, baseline["reviewId"])["verdicts"]
    # Missing evidence may lie anywhere, even outside what changed: an inconclusive verdict reads everything again.
    if any(v["verdict"] == "inconclusive" for v in verdicts):
        return complete
    # Where an earlier view found a violation must be read again, changed or not.
    watched = {evidence_file(e) for v in verdicts if v["verdict"] == "violated" for e in v["evidence"]}
    content = {pid: p for pid, p in closure.items() if p["entries"] and not p["obligationIds"]}
    required = {pid for pid, p in closure.items() if p["obligationIds"]
                or pid in content and (any(reading_key(e) not in known for e in p["entries"]) or watched & set(p["files"]))}
    weight = lambda ids: sum(len(encoded(content[pid])) for pid in ids if pid in content)
    if 2 * weight(required) > weight(content):
        return complete
    return {"mode": "differential", "required": required, "baseline": baseline["reviewId"],
            "digest": sha(group("differential", [baseline["reviewId"], *sorted(pid + ":" + closure[pid]["packetHash"] for pid in required)]))}


def baseline_path(state_path, packet_id, baseline):
    return Path(state_path).parent / ("baseline-" + sha(frame(packet_id) + frame(baseline["reviewId"])) + ".json")


def baseline_verdicts(state, packet_id):
    baseline = state["baselines"][packet_id]
    return encoded({"packetId": packet_id, "reviewId": baseline["reviewId"], "verdicts": response_for(state, baseline["reviewId"])["verdicts"]})


def validate_packet(packet):
    require(len(encoded(packet)) <= PACKET_LIMIT, "size", "Packet exceeds 65536 bytes; repartition explicitly.")
    closed_packet(packet)
    fragmented = "version" in packet
    string(packet["reviewId"]); string(packet["resolutionId"])
    require(type(packet["maxPacketBytes"]) is int and packet["maxPacketBytes"] == PACKET_LIMIT,
            "size", "Packet cannot override the trusted 65536-byte limit.")
    for key in ("files", "talentIds", "obligationIds"):
        ordered(packet[key])
    # A context packet carries entries for its dependents: no obligation, no attempt, no verdict.
    # In v3 an index packet carries only references to other packets, so that no list outgrows a packet.
    require(packet["obligationIds"] or bool(packet["entries"]) != (fragmented and bool(array(packet["dependencies"]))) if fragmented
            else packet["obligationIds"] or (packet["entries"] and not packet["dependencies"]),
            "empty_packet", "Each packet needs obligations, or entries as context.")
    require(packet["entries"] or any(fragmented or d.get("entryIds") for d in array(packet["dependencies"]) if type(d) is dict),
            "empty_packet", "Each packet needs entries, its own or from its dependencies.")
    ids = []
    fragment_order = {}
    for entry in array(packet["entries"]):
        if fragmented and type(entry) is dict and entry.get("kind") == "fragment":
            validate_fragment(entry)
            git_path(entry["file"])
            parent = entry["parent"]
            require(fragment_order.get(parent["entryId"], -1) < parent["index"], "fragment", "Fragments are out of order in their packet.")
            fragment_order[parent["entryId"]] = parent["index"]
            ids.append(entry["entryId"])
            continue
        closed(entry, "entryId file kind content")
        git_path(entry["file"]); string(entry["content"])
        require(entry["kind"] in ("header", "hunk") and entry_id(entry) == entry["entryId"], "entry", "Entry identity mismatch.")
        require(entry["content"].startswith("diff --git " if entry["kind"] == "header" else "@@ "), "entry", "Entry kind mismatch.")
        ids.append(entry["entryId"])
    require(len(set(ids)) == len(ids) and packet["files"] == sorted({e["file"] for e in packet["entries"]}),
            "entry", "Duplicate entry or inconsistent files.")
    tids = []
    for value in array(packet["rulesDigests"]):
        closed(value, "talentId rulesDigest")
        tids.append(string(value["talentId"])); digest(value["rulesDigest"])
    require(tids == packet["talentIds"], "rules", "Exactly one ordered digest per talent is required.")
    dependencies = []
    for dependency in array(packet["dependencies"]):
        closed(dependency, "packetId entryIds obligationIds")
        ordered(dependency["entryIds"]); ordered(dependency["obligationIds"])
        require(fragmented or dependency["entryIds"] or dependency["obligationIds"], "dependency", "Empty dependency.")
        dependencies.append(string(dependency["packetId"]))
    ordered(dependencies)
    require(packet["packetId"] not in dependencies, "dependency", "Self dependency.")
    if packet["obligationIds"]:
        expected_id = packet_id(packet["obligationIds"])
    else:
        expected_id = context_packet_id(ids) if ids else index_packet_id(dependencies)
    require(expected_id == packet["packetId"] and packet_hash(packet) == packet["packetHash"],
            "hash", "Packet identity/hash mismatch.")


def validate_graph(packets):
    by_id = {p["packetId"]: p for p in packets}
    require(len(by_id) == len(packets), "packet", "Duplicate packet identity.")
    for packet in packets:
        validate_packet(packet)
        for dependency in packet["dependencies"]:
            upstream = by_id.get(dependency["packetId"])
            require(upstream is not None, "dependency", "Missing upstream packet.")
            require(set(dependency["entryIds"]) <= {e["entryId"] for e in upstream["entries"]}
                    and set(dependency["obligationIds"]) <= set(upstream["obligationIds"]),
                    "dependency", "Dependency identifiers do not belong to upstream packet.")
            # A whole reference reads a content-addressed packet; a review packet's identity does not bind its content.
            require(dependency["entryIds"] or dependency["obligationIds"] or not upstream["obligationIds"],
                    "dependency", "A whole-packet reference targets context or index packets only.")
    remaining = {key: {d["packetId"] for d in p["dependencies"]} for key, p in by_id.items()}
    while remaining:
        ready = {key for key, deps in remaining.items() if not deps}
        require(ready, "cycle", "Packet dependency graph contains a cycle.")
        remaining = {key: deps - ready for key, deps in remaining.items() if key not in ready}
    return by_id


def check_bindings(packet, plan, obligations):
    require(packet["resolutionId"] == plan["resolutionId"] and set(packet["obligationIds"]) <= set(obligations),
            "binding", "Packet differs from expected resolution/obligations.")
    mapping = {obligations[oid]["talentId"]: obligations[oid]["rulesDigest"] for oid in packet["obligationIds"]
               if obligations[oid]["kind"] == "grace"}
    require(packet["rulesDigests"] == [{"talentId": tid, "rulesDigest": value} for tid, value in sorted(mapping.items())],
            "binding", "Packet rules differ from the materialized plan.")


def assignment_identity(assignment):
    if assignment["obligationIds"]:
        return packet_id(assignment["obligationIds"])
    if assignment["entryIds"]:
        return context_packet_id(assignment["entryIds"])
    return index_packet_id(d["packetId"] for d in assignment["dependencies"])


def compose_packet(assignment, catalog, plan, obligations):
    selected = [item for _, item in sorted((catalog[i] for i in assignment["entryIds"]), key=lambda pair: pair[0])]
    mapping = {obligations[oid]["talentId"]: obligations[oid]["rulesDigest"] for oid in assignment["obligationIds"]
               if obligations[oid]["kind"] == "grace"}
    identity = assignment_identity(assignment)
    packet = {"reviewId": assignment["reviewId"] or "context-" + identity, "packetId": identity,
              "resolutionId": plan["resolutionId"], "packetHash": "", "maxPacketBytes": PACKET_LIMIT,
              "files": sorted({e["file"] for e in selected}), "entries": selected, "talentIds": sorted(mapping),
              "rulesDigests": [{"talentId": tid, "rulesDigest": value} for tid, value in sorted(mapping.items())],
              "obligationIds": assignment["obligationIds"], "dependencies": assignment["dependencies"]}
    if fragmented_protocol(plan):
        packet["version"] = FRAGMENT_VERSION
    packet["packetHash"] = packet_hash(packet)
    return packet


def build_packet(assignment, catalog, plan, obligations):
    packet = compose_packet(assignment, catalog, plan, obligations)
    validate_packet(packet)
    return packet


def new_assignment(entry_ids, obligation_ids, dependencies, index):
    review = bool(obligation_ids)
    return {"entryIds": entry_ids, "obligationIds": obligation_ids, "dependencies": dependencies,
            "reviewId": "review-" + uuid.uuid4().hex if review else None,
            "attemptId": str(uuid.uuid4()) if review else None,
            "reviewer": "reviewer-" + str(index) if review else None}


def item_weight(values):
    # The serialized weight includes `files`.
    return sum(len(encoded(v)) + len(encoded(v["file"])) for v in values)


def compose_chunks(items):
    # One file section per context packet, split only past the target: a correction then changes the packets of the
    # files it touches, never the boundaries of the others.
    sections = []
    for item in items:
        if item["kind"] == "header" or not sections or (item["kind"] == "fragment" and item["parent"]["kind"] == "header" and item["parent"]["index"] == 0):
            sections.append([])
        sections[-1].append(item)
    chunks = []
    for section in sections:
        chunks.append([])
        size = 0
        for value in section:
            if chunks[-1] and size + item_weight([value]) > PACKET_TARGET:
                chunks.append([])
                size = 0
            chunks[-1].append(value)
            size += item_weight([value])
    return chunks


def report_floor(obligations):
    """Elements of the smallest report: four root keys, then one conclusive verdict per check with its single evidence, and its mention in executions."""
    grace = [o for o in obligations.values() if o["kind"] == "grace"]
    return 4 + 4 * len({o["talentId"] for o in grace}) + 10 * len(grace) + 11 * (len(obligations) - len(grace))


def check_report_floor(obligations):
    require(report_floor(obligations) <= REPORT_ELEMENTS, "report_limit",
            f"{len(obligations)} checks cannot fit in one report Grace can record; designate fewer talents before reviewing. No automatic retry.")


def whole_references(assignments):
    return sorted(({"packetId": assignment_identity(a), "entryIds": [], "obligationIds": []} for a in assignments), key=lambda d: d["packetId"])


def partition_fragments(request, manifest_value, plan, obligations, groups):
    """Absorbs every recoverable overflow: fragments, index packets, then smaller review packets for the same reviewer."""
    check_report_floor(obligations)
    entries = manifest_value["entries"]
    items = reading_items(entries)
    catalog = item_catalog(items)
    adaptations = []
    def adapted(cause, measured, limit, unit, element, adaptation, created):
        adaptations.append({"cause": cause, "measured": measured, "limit": limit, "unit": unit, "element": element, "adaptation": adaptation, "created": created})
    fragments = {item["parent"]["entryId"]: item["parent"]["count"] for item in items if item["kind"] == "fragment"}
    for entry in entries:
        if entry["entryId"] in fragments:
            adapted("entry_over_packet_limit", packet_weight(entry), PACKET_LIMIT, "bytes", entry["file"], "fragments", fragments[entry["entryId"]])
    def build(assignment):
        try:
            return build_packet(assignment, catalog, plan, obligations)
        except Refusal as error:
            if error.code != "size":
                raise
            raise Refusal("packet_limit", f"A single element cannot fit in {PACKET_LIMIT} bytes even alone; the review stays incomplete and resumable. No automatic retry.") from None
    chunks = compose_chunks(items)
    if len(groups) == 1 and item_weight(items) <= PACKET_TARGET and len(groups[0]) <= REVIEW_OBLIGATIONS:
        ordinary = new_assignment(sorted(item["entryId"] for item in items), groups[0], [], 1)
        try:
            build_packet(ordinary, catalog, plan, obligations)
            return write_partition(request, [ordinary], adaptations, entries)
        except Refusal as error:
            if error.code != "size":
                raise
            adapted("review_packet_over_limit", len(encoded(compose_packet(ordinary, catalog, plan, obligations))), PACKET_LIMIT, "bytes", "reviewer-1", "context_packets", len(chunks))
    contexts = [new_assignment(sorted(item["entryId"] for item in chunk), [], [], 0) for chunk in chunks if chunk]
    for context in contexts:
        build(context)
    if len(contexts) > 1:
        adapted("change_over_packet_target", sum(len(encoded(item)) + len(encoded(item["file"])) for item in items) + ENVELOPE_RESERVE, PACKET_TARGET, "bytes", "change", "context_packets", len(contexts))
    level, indexes = contexts, []
    while len(level) > INDEX_FANOUT:
        level = [new_assignment([], [], whole_references(level[start:start + INDEX_FANOUT]), 0) for start in range(0, len(level), INDEX_FANOUT)]
        indexes.extend(level)
    if indexes:
        adapted("dependencies_over_packet_budget", len(contexts), INDEX_FANOUT, "packets", "dependencies", "index_packets", len(indexes))
    for index_packet in indexes:
        build(index_packet)
    dependencies = whole_references(level)
    reviews = []
    def add_reviews(group_value, reviewer):
        candidate = new_assignment([], group_value, dependencies, reviewer)
        fits = len(group_value) <= REVIEW_OBLIGATIONS
        if fits:
            try:
                build_packet(candidate, catalog, plan, obligations)
            except Refusal as error:
                if error.code != "size":
                    raise
                fits = False
        if fits:
            reviews.append(candidate)
            return
        if len(group_value) == 1:
            build(candidate)
        # The same reviewer keeps every part: packets carry the list, they do not add reviewers.
        if len(group_value) > REVIEW_OBLIGATIONS:
            adapted("obligations_over_review_packet", len(group_value), REVIEW_OBLIGATIONS, "obligations", "reviewer-" + str(reviewer), "review_packets", 2)
        else:
            adapted("review_packet_over_limit", len(encoded(compose_packet(candidate, catalog, plan, obligations))), PACKET_LIMIT, "bytes", "reviewer-" + str(reviewer), "review_packets", 2)
        middle = len(group_value) // 2
        add_reviews(group_value[:middle], reviewer)
        add_reviews(group_value[middle:], reviewer)
    for number, group_value in enumerate(groups):
        add_reviews(group_value, number + 1)
    return write_partition(request, contexts + indexes + reviews, adaptations, entries)


def write_partition(request, assignments, adaptations, entries):
    directory = new_directory(request["outputDirectory"])
    artifact = write_new(directory / "partition.json", encoded({"schemaVersion": 2, "assignments": assignments, "adaptations": adaptations}))
    return [artifact], {"contextPackets": sum(bool(a["entryIds"]) and not a["obligationIds"] for a in assignments),
                        "indexPackets": sum(not a["entryIds"] and not a["obligationIds"] for a in assignments),
                        "reviewPackets": sum(bool(a["obligationIds"]) for a in assignments),
                        "fragmentedEntries": sum(1 for a in adaptations if a["cause"] == "entry_over_packet_limit"),
                        "adaptations": len(adaptations)}


def partition(request):
    manifest_value, captured = load_manifest(request["manifest"])
    plan = read_json(request["plan"])
    obligations = validate_plan(plan)
    require(plan["sourceHash"] == sha(encoded(captured["changeSource"])), "source", "Plan binds a different change source.")
    groups = [sorted(obligations)] if request["groups"] is None else array(request["groups"])
    for value in groups:
        require(type(value) is list and bool(value), "assignment", "Each group needs obligations.")
        ordered(value)
    require(sorted(oid for value in groups for oid in value) == sorted(obligations), "coverage", "Groups must assign each obligation exactly once.")
    if fragmented_protocol(plan):
        return partition_fragments(request, manifest_value, plan, obligations, groups)
    # Entries stay whole and file sections stay together while they fit; each group still sees every entry.
    sections = []
    for entry in manifest_value["entries"]:
        if entry["kind"] == "header" or not sections:
            sections.append([])
        sections[-1].append(entry)
    chunks, size = [[]], 0
    def weight(entries):
        return sum(len(encoded(e)) for e in entries)
    for section in sections:
        for entries in ([section] if weight(section) <= PACKET_TARGET else [[e] for e in section]):
            if chunks[-1] and size + weight(entries) > PACKET_TARGET:
                chunks.append([])
                size = 0
            chunks[-1].extend(entries)
            size += weight(entries)
    catalog = item_catalog(manifest_value["entries"])
    if len(groups) == 1 and len(chunks) == 1:
        assignments = [new_assignment(sorted(e["entryId"] for e in chunks[0]), groups[0], [], 1)]
    else:
        contexts = [new_assignment(sorted(e["entryId"] for e in chunk), [], [], 0) for chunk in chunks if chunk]
        dependencies = sorted(({"packetId": context_packet_id(c["entryIds"]), "entryIds": c["entryIds"], "obligationIds": []} for c in contexts),
                              key=lambda d: d["packetId"])
        assignments = contexts + [new_assignment([], value, dependencies, index + 1) for index, value in enumerate(groups)]
    try:
        for item in assignments:
            build_packet(item, catalog, plan, obligations)
    except Refusal as error:
        if error.code != "size":
            raise
        raise Refusal("indivisible", "An entry or dependency list cannot fit in one packet; review this change manually and exhaustively.") from None
    directory = new_directory(request["outputDirectory"])
    artifact = write_new(directory / "partition.json", encoded({"schemaVersion": 1, "assignments": assignments}))
    return [artifact], {"contextPackets": sum(not a["obligationIds"] for a in assignments),
                        "reviewPackets": sum(bool(a["obligationIds"]) for a in assignments)}


def reviewed_entries(packets):
    # A context packet covers nothing by itself: an entry counts once a review packet reads it.
    if any("version" in p for p in packets):
        by_id = {p["packetId"]: p for p in packets}
        covered = set()
        for packet in packets:
            if packet["obligationIds"]:
                covered |= assembled_entries(read_items(packet, by_id))
        return covered
    return {entry_id_value for p in packets if p["obligationIds"]
            for entry_id_value in [*(e["entryId"] for e in p["entries"]), *(i for d in p["dependencies"] for i in d["entryIds"])]}


def pack(request):
    manifest_value, captured = load_manifest(request["manifest"])
    plan = read_json(request["plan"])
    obligations = validate_plan(plan)
    require(plan["sourceHash"] == sha(encoded(captured["changeSource"])), "source", "Plan binds a different change source.")
    if fragmented_protocol(plan):
        check_report_floor(obligations)
    catalog = item_catalog(reading_items(manifest_value["entries"]) if fragmented_protocol(plan) else manifest_value["entries"])
    packets, attempts = [], []
    assigned = []
    for assignment in array(request["assignments"]):
        closed(assignment, "entryIds obligationIds dependencies reviewId attemptId reviewer")
        ordered(assignment["entryIds"]); ordered(assignment["obligationIds"])
        require(set(assignment["entryIds"]) <= set(catalog) and set(assignment["obligationIds"]) <= set(obligations),
                "assignment", "Unknown entry or obligation in assignment.")
        if assignment["obligationIds"]:
            string(assignment["reviewId"])
        packet = build_packet(assignment, catalog, plan, obligations)
        packets.append(packet)
        if assignment["obligationIds"]:
            attempts.append({"reviewId": packet["reviewId"], "attemptId": identifier(assignment["attemptId"]),
                             "packetId": packet["packetId"], "packetHash": packet["packetHash"],
                             "reviewer": string(assignment["reviewer"]), "role": "primary", "status": "pending", "response": None})
        assigned.extend(assignment["obligationIds"])
    require(sorted(assigned) == sorted(obligations) and reviewed_entries(packets) == {e["entryId"] for e in manifest_value["entries"]},
            "coverage", "Obligation assignment or entry coverage is incomplete/duplicated.")
    require(len({a["reviewId"] for a in attempts}) == len(attempts) and len({a["attemptId"] for a in attempts}) == len(attempts),
            "attempt", "Attempt/review identities must be unique.")
    validate_graph(packets)
    directory = new_directory(request["outputDirectory"])
    stored = [write_new(directory / (packet["packetId"] + ".json"), encoded(packet)) for packet in packets]
    fragmented = fragmented_protocol(plan)
    state = {"schemaVersion": 2 if fragmented else 1, "revision": 0, "generation": 0, "plan": plan, "planHash": sha(encoded(plan)),
             "manifest": request["manifest"], "manifestHash": sha(encoded(manifest_value)),
             "fullDiffHash": captured["fullDiff"]["sha256"], "packets": stored if fragmented else [encoded(p).decode() for p in packets],
             "attempts": attempts, "finals": {}, "previousFinals": {}, "reports": []}
    if fragmented:
        state["baselines"] = {}
    artifact = write_new(directory / "state.json", encoded(state))
    return [artifact], {"packets": len(packets), "aboveTarget": sum(len(encoded(p)) > PACKET_TARGET for p in packets),
                        "packetDirectory": str(directory)}


def stored_packets(state):
    # A schemaVersion 2 state references its packet files: the diff is not copied into each new state revision.
    values = array(state["packets"])
    if state["schemaVersion"] == 1:
        return [parse(string(p).encode(), PACKET_LIMIT) for p in values]
    return [parse(check_descriptor(p), PACKET_LIMIT) for p in values]


def load_state(path, raw=None):
    raw = read_bytes(path) if raw is None else raw
    state = parse(raw)
    require(type(state) is dict, "schema", "Expected the documented closed object; check missing or unknown keys.")
    v1_or_v2(state.get("schemaVersion"))
    # A schemaVersion 2 state also keeps, per reissued review packet, the baseline its next primary view reads against.
    closed(state, "schemaVersion revision generation plan planHash manifest manifestHash fullDiffHash packets attempts finals previousFinals reports"
           + (" baselines" if state["schemaVersion"] == 2 else ""))
    require(type(state["revision"]) is int and state["revision"] >= 0, "state", "Invalid state revision.")
    require(type(state["generation"]) is int and state["generation"] >= 0, "state", "Invalid review generation.")
    validate_plan(state["plan"])
    require(sha(encoded(state["plan"])) == digest(state["planHash"]), "state", "Plan digest mismatch.")
    packets = stored_packets(state)
    validate_graph(packets)
    fragmented = state["schemaVersion"] == 2
    require(fragmented_protocol(state["plan"]) == fragmented and all(("version" in p) == fragmented for p in packets),
            "state", "State schema, plan protocol and packet versions disagree.")
    obligations = validate_plan(state["plan"])
    for packet in packets:
        check_bindings(packet, state["plan"], obligations)
    validate_lifecycle(state, packets)
    return state, packets, sha(raw)


def validate_verdict(verdict, identity_keys, custom=False):
    closed(verdict, identity_keys + " verdict justification evidence missingEvidence")
    require(verdict["verdict"] in ("respected", "violated", "not_applicable", "inconclusive"), "verdict", "Unknown verdict.")
    require(not custom or verdict["verdict"] != "not_applicable", "custom", "Custom obligations cannot be not_applicable.")
    string(verdict["justification"])
    for evidence in array(verdict["evidence"]):
        closed(evidence, "location explanation")
        match = re.fullmatch(r"(.+?):([1-9][0-9]*)(?::([1-9][0-9]*))?", string(evidence["location"]))
        require(match is not None, "evidence", "Evidence needs a relative file:line[:column] location.")
        git_path(match.group(1)); string(evidence["explanation"])
    ordered(verdict["missingEvidence"])
    if verdict["verdict"] == "inconclusive":
        require(verdict["missingEvidence"], "evidence", "Inconclusive verdict needs missingEvidence.")
    else:
        require(verdict["evidence"] and not verdict["missingEvidence"], "evidence", "Conclusive verdict needs evidence and no missingEvidence.")


def validate_preserved_payload(payload):
    closed(payload, "submissionId resolutionId talentResults expertiseResults")
    identifier(payload["submissionId"]); identifier(payload["resolutionId"])
    talents, custom = [], []
    for talent in array(payload["talentResults"]):
        closed(talent, "talentId rulesDigest ruleResults")
        talents.append(string(talent["talentId"])); digest(talent["rulesDigest"])
        rules = []
        for verdict in array(talent["ruleResults"]):
            validate_verdict(verdict, "ruleId")
            rules.append(string(verdict["ruleId"]))
        ordered(rules)
    ordered(talents)
    for verdict in array(payload["expertiseResults"]):
        validate_verdict(verdict, "expertiseId expertiseVersionId", True)
        custom.append((identifier(verdict["expertiseId"]), identifier(verdict["expertiseVersionId"])))
    require(custom == sorted(set(custom)), "report", "Duplicate or unsorted Custom result identities.")


def closed_response(response):
    closed(response, RESPONSE_KEYS + (" readingDigest" if type(response) is dict and "readingDigest" in response else ""))


def validate_response(response, packet, plan, by_id, expected_reading=None):
    fragmented = "version" in packet
    closed(response, RESPONSE_KEYS + (" readingDigest" if fragmented else ""))
    string(response["reviewId"])
    if fragmented:
        # Declared by the reviewer, bound to everything it had to read; a stored fragment alone never produces it.
        require(response["readingDigest"] == expected_reading, "unread", "The response does not cover every packet to read; finish reading before answering.")
    require(response["packetId"] == packet["packetId"] and response["packetHash"] == packet["packetHash"]
            and response["rulesDigests"] == packet["rulesDigests"], "response_binding", "Response packet/hash/rules mapping differs from current assignment.")
    obligations = validate_plan(plan)
    ids = []
    for verdict in array(response["verdicts"]):
        closed(verdict, "obligationId verdict justification evidence missingEvidence")
        oid = string(verdict["obligationId"])
        require(oid in obligations, "verdict", "Unknown obligation in response.")
        ids.append(oid)
        validate_verdict(verdict, "obligationId", obligations[oid]["kind"] == "custom")
    require(ids == packet["obligationIds"], "coverage", "Response verdicts must exactly cover assigned obligations, sorted and unique.")


def validate_lifecycle(state, packets):
    by_id = {p["packetId"]: p for p in packets}
    reviews, executions, primaries = set(), set(), set()
    attempts = {}
    for attempt in array(state["attempts"]):
        closed(attempt, "reviewId attemptId packetId packetHash reviewer role status response")
        rid = string(attempt["reviewId"])
        aid = identifier(attempt["attemptId"])
        require(rid not in reviews and aid not in executions, "attempt", "Reused attempt identity in state.")
        reviews.add(rid); executions.add(aid); attempts[rid] = attempt
        string(attempt["reviewer"]); string(attempt["packetId"]); digest(attempt["packetHash"])
        require(attempt["role"] in ("primary", "second") and attempt["status"] in ("pending", "consumed", "revoked"),
                "attempt", "Invalid attempt role or lifecycle state.")
        if attempt["status"] == "pending":
            require(attempt["response"] is None, "attempt", "Pending attempt already has a response.")
            packet = by_id.get(attempt["packetId"])
            require(packet is not None and packet["packetHash"] == attempt["packetHash"], "attempt", "Pending attempt targets a stale packet.")
            if attempt["role"] == "primary":
                require(attempt["packetId"] not in primaries, "attempt", "Multiple pending primary attempts.")
                primaries.add(attempt["packetId"])
        if attempt["status"] == "consumed":
            require(type(attempt["response"]) is dict, "attempt", "Consumed attempt lacks its response.")
        if attempt["response"] is not None:
            closed_response(attempt["response"])
            response = attempt["response"]
            ids = []
            for verdict in array(response["verdicts"]):
                validate_verdict(verdict, "obligationId")
                ids.append(string(verdict["obligationId"]))
            ordered(ids)
            talents = []
            for mapping in array(response["rulesDigests"]):
                closed(mapping, "talentId rulesDigest")
                talents.append(string(mapping["talentId"])); digest(mapping["rulesDigest"])
            ordered(talents)
            require(all(attempt["response"][key] == attempt[key] for key in ("reviewId", "packetId", "packetHash")),
                    "attempt", "Stored response differs from attempt binding.")
    require(type(state["finals"]) is dict and type(state["previousFinals"]) is dict, "state", "Final selections must be objects.")
    if state["schemaVersion"] == 2:
        require(type(state["baselines"]) is dict, "state", "Baselines must be an object.")
        for packet_id_value, baseline in state["baselines"].items():
            closed(baseline, "reviewId read")
            attempt = attempts.get(string(baseline["reviewId"]))
            require(packet_id_value in by_id and by_id[packet_id_value]["obligationIds"] and attempt is not None
                    and attempt["response"] is not None and attempt["packetId"] == packet_id_value, "state", "A baseline names the answer of an earlier view of this review packet.")
            ordered(baseline["read"])
    for packet_id_value, selection in state["finals"].items():
        closed(selection, "reviewId reason")
        string(selection["reason"])
        attempt = attempts.get(selection["reviewId"])
        require(attempt is not None and attempt["status"] == "consumed" and attempt["packetId"] == packet_id_value
                and packet_id_value in by_id, "final", "Final selection is not a consumed current attempt.")
        require(not any(a["packetId"] == packet_id_value and a["status"] == "pending" for a in attempts.values()),
                "final", "A pending second view prevents finalization.")
        validate_response(attempt["response"], by_id[packet_id_value], state["plan"], by_id, expected_reading(state, attempt, by_id))
    for packet_id_value, review_id in state["previousFinals"].items():
        attempt = attempts.get(review_id)
        require(attempt is not None and attempt["response"] is not None and attempt["packetId"] == packet_id_value,
                "state", "Previous final response is missing.")
    submissions = set()
    for report in array(state["reports"]):
        closed(report, "submissionId payload payloadHash revision generation" + (" reportId" if type(report) is dict and "reportId" in report else ""))
        if "reportId" in report:
            require(state["schemaVersion"] == 2, "report", "A historical state holds no acknowledged report.")
            identifier(report["reportId"])
        require(identifier(report["submissionId"]) not in submissions, "report", "Duplicate report submission identity.")
        submissions.add(report["submissionId"])
        validate_preserved_payload(report["payload"])
        require(report["payload"]["submissionId"] == report["submissionId"] and sha(encoded(report["payload"])) == digest(report["payloadHash"]),
                "report", "Preserved report payload changed.")
        require(type(report["revision"]) is int and 0 <= report["revision"] <= state["revision"], "report", "Invalid report revision.")
        require(type(report["generation"]) is int and 0 <= report["generation"] <= state["generation"], "report", "Invalid report generation.")


def global_bindings(state, packets):
    value, captured = load_manifest(state["manifest"])
    require(sha(encoded(value)) == state["manifestHash"] and captured["fullDiff"]["sha256"] == state["fullDiffHash"]
            and sha(encoded(captured["changeSource"])) == state["plan"]["sourceHash"], "state", "State source/manifest binding mismatch.")
    assigned = []
    catalog = item_catalog(reading_items(value["entries"]) if state["schemaVersion"] == 2 else value["entries"])
    for packet in packets:
        positions = [catalog.get(e["entryId"], (-1, None))[0] for e in packet["entries"]]
        require(all(position >= 0 and catalog[e["entryId"]][1] == e for position, e in zip(positions, packet["entries"]))
                and positions == sorted(set(positions)), "coverage", "Packet is not an ordered subset of the manifest.")
        assigned.extend(packet["obligationIds"])
    require(reviewed_entries(packets) == {e["entryId"] for e in value["entries"]} and sorted(assigned) == sorted(validate_plan(state["plan"])),
            "coverage", "Global coverage differs from manifest/plan.")
    return value, catalog


@contextmanager
def locked_state(request):
    path = local_path(request["state"])
    with anchored_parent(path) as (name, parent):
        lock = str(name) + ".lock"
        try:
            fd = open_anchored(lock, parent, write=True, create=True)
        except FileExistsError:
            raise Refusal("locked", "State is locked; verify the writer has stopped before explicit lock recovery.") from None
        os.close(fd)
        temporary = None
        try:
            state, packets, old_hash = load_state(str(path), read_anchored(name, parent, ARTIFACT_LIMIT))
            require(old_hash == digest(request["expectedStateHash"]), "state_conflict", "State changed; reread before making a new decision.")
            global_bindings(state, packets)
            yield state, packets
            state["revision"] += 1
            validate_lifecycle(state, stored_packets(state))
            data = encoded(state)
            require(len(data) <= ARTIFACT_LIMIT, "size", "State exceeds local artifact limit.")
            temporary = (path.parent if os.name == "nt" else Path()) / (".review-state-" + uuid.uuid4().hex)
            fd = open_anchored(temporary, parent, write=True, create=True)
            with os.fdopen(fd, "wb") as stream:
                stream.write(data); stream.flush(); os.fsync(stream.fileno())
            require(sha(read_anchored(name, parent, ARTIFACT_LIMIT)) == old_hash,
                    "state_conflict", "State changed despite the lock; stop all writers.")
            replace_args = {} if os.name == "nt" else {"src_dir_fd": parent["dir_fd"], "dst_dir_fd": parent["dir_fd"]}
            os.replace(temporary, name, **replace_args)
            temporary = None
        finally:
            if temporary is not None:
                os.unlink(temporary, **parent)
            os.unlink(lock, **parent)


def expected_reading(state, attempt, by_id):
    return reading_scope(state, by_id[attempt["packetId"]], by_id, attempt["role"])["digest"] if state["schemaVersion"] == 2 else None


def response_for(state, review_id):
    return next(a["response"] for a in state["attempts"] if a["reviewId"] == review_id)


def reverse_closure(packets, seeds):
    invalid = set(seeds)
    while True:
        added = {p["packetId"] for p in packets if any(d["packetId"] in invalid for d in p["dependencies"])} - invalid
        if not added:
            return invalid
        invalid.update(added)


def revoke(state, packet_ids):
    for attempt in state["attempts"]:
        if attempt["packetId"] in packet_ids:
            attempt["status"] = "revoked"
    for pid in packet_ids:
        if pid in state["finals"]:
            state["previousFinals"][pid] = state["finals"].pop(pid)["reviewId"]


def element_count(value):
    """Counts as the MCP server does: each object key and each array item, at every depth."""
    count, pending = 0, [value]
    while pending:
        node = pending.pop()
        children = node.values() if type(node) is dict else node if type(node) is list else ()
        for child in children:
            count += 1
            pending.append(child)
    return count


def project_report(state, packets, submission_id):
    require(set(state["finals"]) == {p["packetId"] for p in packets if p["obligationIds"]}, "coverage", "Final verdicts are incomplete.")
    obligations = validate_plan(state["plan"])
    verdicts = {}
    for selection in state["finals"].values():
        for item in response_for(state, selection["reviewId"])["verdicts"]:
            require(item["obligationId"] not in verdicts, "coverage", "Multiple final verdicts for one obligation.")
            verdicts[item["obligationId"]] = item
    require(set(verdicts) == set(obligations), "coverage", "Final verdict coverage mismatch.")
    talents, custom = {}, []
    for oid, obligation in sorted(obligations.items()):
        verdict = verdicts[oid]
        result = {key: verdict[key] for key in ("verdict", "justification", "evidence", "missingEvidence")}
        if obligation["kind"] == "grace":
            group_value = talents.setdefault(obligation["talentId"], {"talentId": obligation["talentId"],
                "rulesDigest": obligation["rulesDigest"], "ruleResults": []})
            group_value["ruleResults"].append({"ruleId": obligation["ruleId"], **result})
        else:
            custom.append({"expertiseId": obligation["expertiseId"], "expertiseVersionId": obligation["expertiseVersionId"], **result})
    return {"submissionId": identifier(submission_id), "resolutionId": state["plan"]["resolutionId"],
            "talentResults": [talents[key] for key in sorted(talents)],
            "expertiseResults": sorted(custom, key=lambda x: (x["expertiseId"], x["expertiseVersionId"]))}


def report_elements(payload, obligations):
    return element_count(payload) + len(obligations)


def report_bytes(payload, obligations):
    return len(encoded(payload)) + sum(len(oid.encode()) + 3 for oid in obligations)


def verdict_outcomes(state, verdicts):
    # In v3 a dependent reads the upstream verdict: a shorter justification does not change it.
    return verdicts if state["schemaVersion"] == 1 else [(v["obligationId"], v["verdict"]) for v in verdicts]


def check_violations_reported(state, packet_ids, revoked=False):
    unreported = unreported_violations(state, packet_ids, revoked)
    require(not unreported, "violation_unreported",
            f"Revoking these packets would drop {len(unreported)} violation(s) from the review before any recorded report holds them ({', '.join(unreported[:5])}); nothing changed: relaunch the reviewer of a pending view on its same attempt, ask a second view with role second and arbitrate, or record the report and acknowledge it first. When a dependent packet holds the violation, keep the upstream verdict until then; an older review can also restart through a new pack.")


def unreported_violations(state, packet_ids, revoked=False):
    """Violations the review would lose if these packets were revoked: no recorded report carries them as they stand."""
    def result(item):
        return encoded({key: item[key] for key in ("verdict", "justification", "evidence", "missingEvidence")})
    preserved, reported = set(), set()
    # Only a server receipt makes a v3 report recorded; a historical state never had one to keep.
    for report in (r for r in state["reports"] if state["schemaVersion"] == 1 or "reportId" in r):
        for talent in report["payload"]["talentResults"]:
            preserved.update((talent["talentId"], item["ruleId"], result(item)) for item in talent["ruleResults"])
            reported.update((talent["talentId"], item["ruleId"]) for item in talent["ruleResults"] if item["verdict"] == "violated")
        preserved.update((item["expertiseId"], item["expertiseVersionId"], result(item)) for item in report["payload"]["expertiseResults"])
        reported.update((item["expertiseId"], item["expertiseVersionId"]) for item in report["payload"]["expertiseResults"] if item["verdict"] == "violated")
    obligations = validate_plan(state["plan"])
    unreported = set()
    for attempt in state["attempts"]:
        selection = state["finals"].get(attempt["packetId"])
        answered = attempt["status"] == "consumed" and (selection is None or selection["reviewId"] == attempt["reviewId"])
        if attempt["packetId"] in packet_ids and (answered or (revoked and attempt["status"] == "revoked" and attempt["response"] is not None)):
            for verdict in attempt["response"]["verdicts"]:
                # A resolution seals its checks: only a historical answer can name one the current plan lost.
                obligation = obligations.get(verdict["obligationId"])
                if obligation is None:
                    continue
                identity = (obligation["talentId"], obligation["ruleId"]) if obligation["kind"] == "grace" else (obligation["expertiseId"], obligation["expertiseVersionId"])
                # A revoked answer may have been reworded since: its check being reported as violated holds it.
                if verdict["verdict"] == "violated" and (identity not in reported if attempt["status"] == "revoked" else (*identity, result(verdict)) not in preserved):
                    unreported.add(verdict["obligationId"])
    return sorted(unreported)


def materialize_exact(path, data):
    if path.exists():
        require(read_bytes(str(path)) == data, "artifact_conflict", "Existing immutable artifact has different bytes.")
        return {"path": str(path), "bytes": len(data), "sha256": sha(data)}
    return write_new(path, data)


def accept(request):
    identifier(request["submissionId"])
    artifacts = []
    with locked_state(request) as (state, packets):
        by_id = {p["packetId"]: p for p in packets}
        previous = {pid: response_for(state, selection["reviewId"])["verdicts"] for pid, selection in state["finals"].items()}
        previous.update({pid: response_for(state, rid)["verdicts"] for pid, rid in state["previousFinals"].items() if pid not in previous})
        if request["response"] is not None:
            response_limit = RESPONSE_LIMIT if state["schemaVersion"] == 2 else PACKET_LIMIT
            try:
                raw = read_bytes(request["response"], response_limit)
            except Refusal as error:
                if error.code != "size" or state["schemaVersion"] != 2:
                    raise
                raise Refusal("response_size", f"The reviewer response exceeds {RESPONSE_LIMIT} bytes; the attempt stays pending: ask for a shorter justification, keeping every verdict.") from None
            if request["repair"] is not None:
                repair = request["repair"]
                closed(repair, "original originalSha256 semanticSha256")
                original = read_bytes(repair["original"], response_limit)
                require(sha(original) == digest(repair["originalSha256"]), "repair", "Original envelope changed.")
                # Only this explicit, unambiguous markdown envelope is repairable.
                require(original.startswith(b"```json\n") and original.endswith(b"\n```"), "repair", "Unsupported envelope; request a new response.")
                old = parse(original[8:-4], response_limit)
                response = parse(raw, response_limit)
                require(old == response and sha(encoded(old)) == digest(repair["semanticSha256"]), "repair", "Envelope repair changed semantic content.")
            else:
                response = parse(raw, response_limit)
            closed_response(response)
            attempt = next((a for a in state["attempts"] if a["reviewId"] == response["reviewId"]), None)
            require(attempt is not None and attempt["status"] == "pending", "attempt", "Unknown, revoked or consumed attempt; do not replay a response.")
            require(string(request["reviewer"]) == attempt["reviewer"], "reviewer", "Harness-supplied reviewer differs from assignment.")
            packet = by_id[attempt["packetId"]]
            validate_response(response, packet, state["plan"], by_id, expected_reading(state, attempt, by_id))
            if state["schemaVersion"] == 2:
                # A dependent answer needs the verdicts it read, through index packets too: answered first, it could be revoked with no way out.
                require(all(pid in state["finals"] or pid == packet["packetId"] or not upstream["obligationIds"] for pid, upstream in packet_closure(packet, by_id).items()), "upstream_pending",
                        "Answer or arbitrate the upstream packets first; this attempt stays pending.")
            attempt.update(status="consumed", response=response)
            active = [a for a in state["attempts"] if a["packetId"] == packet["packetId"] and a["status"] != "revoked"]
            if len(active) == 1 and attempt["role"] == "primary":
                state["finals"][packet["packetId"]] = {"reviewId": attempt["reviewId"], "reason": "Unique primary response"}
        else:
            require(request["reviewer"] is None and request["repair"] is None, "schema", "Arbitration/export without response has no reviewer or repair.")
        arbitrated = set()
        for decision in array(request["arbitration"]):
            closed(decision, "packetId reviewId reason")
            pid = decision["packetId"]
            require(pid in by_id and pid not in arbitrated, "arbitration", "Unknown or repeated packet arbitration.")
            arbitrated.add(pid); string(decision["reason"])
            active = [a for a in state["attempts"] if a["packetId"] == pid and a["status"] != "revoked"]
            require(active and all(a["status"] == "consumed" for a in active), "arbitration", "Wait for every active view or explicitly revoke/reissue.")
            require(any(a["reviewId"] == decision["reviewId"] for a in active), "arbitration", "Chosen view is not an active consumed response.")
            state["finals"][pid] = {"reviewId": decision["reviewId"], "reason": decision["reason"]}
        changed = {pid for pid, selection in state["finals"].items() if pid in previous
                   and verdict_outcomes(state, response_for(state, selection["reviewId"])["verdicts"]) != verdict_outcomes(state, previous[pid])}
        # A changed final can itself depend on another changed final in this call.
        dependents = {p["packetId"] for p in packets if any(d["packetId"] in changed for d in p["dependencies"])}
        invalidated = reverse_closure(packets, dependents)
        if state["schemaVersion"] == 2:
            check_violations_reported(state, invalidated)
        revoke(state, invalidated)
        for pid in set(state["finals"]) - invalidated:
            state["previousFinals"].pop(pid, None)
        complete = set(state["finals"]) == {p["packetId"] for p in packets if p["obligationIds"]}
        payload = None
        blocked = False
        if complete:
            payload = project_report(state, packets, request["submissionId"])
            # The answer stays in the state: refusing it here would lose a violation it may hold.
            obligations = validate_plan(state["plan"])
            if state["schemaVersion"] == 2 and (report_bytes(payload, obligations) > REPORT_LIMIT or report_elements(payload, obligations) > REPORT_ELEMENTS):
                payload, complete, blocked = None, False, True
        if payload is not None:
            payload_hash = sha(encoded(payload))
            existing = next((r for r in state["reports"] if r["submissionId"] == request["submissionId"]), None)
            if existing:
                require(existing["payloadHash"] == payload_hash and existing["generation"] == state["generation"],
                        "submission_conflict", "A new review generation requires a new submissionId; retries must preserve exact payload.")
            else:
                require(not any(r["generation"] == state["generation"] for r in state["reports"]),
                        "submission_conflict", "This generation already has a submissionId; retry that exact report.")
                state["reports"].append({"submissionId": request["submissionId"], "payload": payload,
                                         "payloadHash": payload_hash, "revision": state["revision"] + 1, "generation": state["generation"]})
        validate_lifecycle({**state, "revision": state["revision"] + 1}, packets)
        if payload is not None:
            report_path = Path(request["state"]).parent / ("report-" + request["submissionId"] + ".json")
            artifacts.append(materialize_exact(report_path, encoded(payload)))
        summary = {"complete": complete, "finalPackets": len(state["finals"]), "invalidatedCount": len(invalidated),
                   "recorded": False, "nextAction": ("Submit the exact preserved report, then pass its reportId to acknowledge." if state["schemaVersion"] == 2 else "Submit the exact preserved report; retain the server acknowledgment separately.") if complete else "Reissue the invalidated packets with invalidate, then complete or arbitrate remaining views." if invalidated else "Complete or arbitrate remaining views."}
        if blocked:
            summary["blocked"] = "report_size"
            summary["nextAction"] = f"Every answer is kept but the report exceeds what Grace can record ({REPORT_LIMIT} bytes, {REPORT_ELEMENTS} elements): reissue answered packets with invalidate (a second view, then arbitration, for a packet holding a violation) and ask for shorter justifications or less evidence, keeping every verdict; if violation_unreported still refuses, restart a new complete review."
    raw = read_bytes(request["state"])
    artifacts.insert(0, {"path": request["state"], "bytes": len(raw), "sha256": sha(raw)})
    return artifacts, summary


def acknowledge(request):
    report_id = identifier(request["reportId"])
    identifier(request["submissionId"])
    with locked_state(request) as (state, _):
        require(state["schemaVersion"] == 2, "version", "A historical state keeps its receipts outside the client.")
        report = next((r for r in state["reports"] if r["submissionId"] == request["submissionId"]), None)
        require(report is not None, "report", "No preserved report has this submissionId.")
        require(report.get("reportId", report_id) == report_id, "submission_conflict", "This report already has another server receipt.")
        report["reportId"] = report_id
    raw = read_bytes(request["state"])
    return [{"path": request["state"], "bytes": len(raw), "sha256": sha(raw)}], {"submissionId": request["submissionId"], "reportId": report_id, "acknowledged": True}


def invalidate(request):
    artifacts = []
    outgoing = []
    with locked_state(request) as (state, previous_packets):
        current, current_packets, current_hash = load_state(request["current"])
        require(current_hash == digest(request["currentStateHash"]), "state_conflict", "Current candidate state changed.")
        global_bindings(current, current_packets)
        require(current["schemaVersion"] >= state["schemaVersion"], "version", "A review cannot return to an older packet format.")
        # A new packet format restarts a complete generation: nothing previously read carries over.
        migrating = current["schemaVersion"] != state["schemaVersion"]
        before = {p["packetId"]: p for p in previous_packets}
        after = {p["packetId"]: p for p in current_packets}
        old_obligations, new_obligations = validate_plan(state["plan"]), validate_plan(current["plan"])
        changed = set(before) ^ set(after)
        for pid in set(before) & set(after):
            if before[pid]["packetHash"] != after[pid]["packetHash"] or any(old_obligations.get(oid) != new_obligations.get(oid) for oid in after[pid]["obligationIds"]):
                changed.add(pid)
        if any(state["plan"][key] != current["plan"][key] for key in ("resolutionId", "protocol")):
            changed.update(before); changed.update(after)
        if migrating:
            changed.update(before); changed.update(after)
        # In v3 a revoked response holds nothing a report still owes: violation_unreported stood guard when it was revoked.
        elif changed and any(a["response"] is not None and (state["schemaVersion"] == 1 or a["status"] != "revoked") for a in state["attempts"]):
            require(set(state["finals"]) == {p["packetId"] for p in previous_packets if p["obligationIds"]} and state["reports"], "report_required", "Preserve the exhaustive report before correction/invalidation; a report blocked by report_size is left by reissuing answered packets.")
            latest = project_report(state, previous_packets, state["reports"][-1]["submissionId"])
            require(sha(encoded(latest)) == state["reports"][-1]["payloadHash"], "report_required", "Current exhaustive report has not been preserved.")
            require(state["schemaVersion"] == 1 or "reportId" in state["reports"][-1], "report_required", "Submit the report to grace_record_validation, then pass its reportId to acknowledge before correction/invalidation.")
        for pid in ordered(request["upstreamChanges"]):
            require(pid in state["finals"] and pid in current["finals"], "upstream", "An upstream change requires both observed final responses.")
            old_response = response_for(state, state["finals"][pid]["reviewId"])
            new_response = response_for(current, current["finals"][pid]["reviewId"])
            require(verdict_outcomes(state, old_response["verdicts"]) != verdict_outcomes(state, new_response["verdicts"]), "upstream", "Upstream final verdict did not change.")
            changed.add(pid)
        attempts = array(request["attempts"])
        if migrating and not attempts:
            attempts = [{"reviewId": a["reviewId"], "attemptId": a["attemptId"], "packetId": a["packetId"], "reviewer": a["reviewer"],
                         "role": a["role"], "reason": "Complete review generation for the new packet format."}
                        for a in current["attempts"] if a["status"] == "pending"]
        reissued = set()
        for item in attempts:
            closed(item, "reviewId attemptId packetId reviewer role reason")
            require(item["packetId"] in after and after[item["packetId"]]["obligationIds"] and item["role"] in ("primary", "second"),
                    "attempt", "Unknown packet/attempt role.")
            string(item["reason"])
            if item["role"] == "primary":
                require(item["packetId"] not in reissued, "attempt", "Only one new primary per packet.")
                reissued.add(item["packetId"])
        invalidated = reverse_closure(previous_packets + [p for p in current_packets if p not in previous_packets], changed | reissued)
        # A migration keeps no answer: it also answers for a violation the old client revoked without a report.
        if current["schemaVersion"] == 2:
            check_violations_reported(state, invalidated, migrating)
        # A reissued review packet keeps as baseline the current final answer, which covered exactly its last closure: its next
        # primary view reads what changed since, with those verdicts as data. Without a current final, an earlier baseline still
        # describes what its own answer covered; a packet hash does not, since it does not bind upstream review content.
        # New criteria, a new format or a new protocol start over.
        baselines = {}
        if current["schemaVersion"] == 2 and not migrating and all(state["plan"][key] == current["plan"][key] for key in ("resolutionId", "protocol")):
            for pid in sorted(invalidated):
                packet = after.get(pid)
                if packet is None or not packet["obligationIds"] or any(old_obligations.get(oid) != new_obligations.get(oid) for oid in packet["obligationIds"]):
                    continue
                if pid in state["finals"]:
                    baselines[pid] = {"reviewId": state["finals"][pid]["reviewId"],
                                      "read": sorted({reading_key(e) for p in packet_closure(before[pid], before).values() for e in p["entries"]})}
                elif pid in state["baselines"]:
                    baselines[pid] = state["baselines"][pid]
        revoke(state, invalidated)
        if invalidated or attempts:
            state["generation"] += 1
        state.update({key: current[key] for key in ("schemaVersion", "plan", "planHash", "manifest", "manifestHash", "fullDiffHash", "packets")})
        if current["schemaVersion"] == 2:
            state["baselines"] = {**{pid: b for pid, b in state.get("baselines", {}).items() if pid in after and pid not in invalidated}, **baselines}
        for item in attempts:
            require(not any(a["reviewId"] == item["reviewId"] or a["attemptId"] == item["attemptId"] for a in state["attempts"]),
                    "attempt", "Never reuse a reviewId or execution attemptId, including revoked attempts.")
            pid = item["packetId"]
            if item["role"] == "second":
                require(any(a["packetId"] == pid and a["role"] == "primary" and a["status"] != "revoked" for a in state["attempts"]),
                        "attempt", "A second view needs an active primary.")
                if pid in state["finals"]:
                    state["previousFinals"][pid] = state["finals"].pop(pid)["reviewId"]
            packet = {**after[pid], "reviewId": string(item["reviewId"])}
            validate_packet(packet)
            state["attempts"].append({"reviewId": item["reviewId"], "attemptId": identifier(item["attemptId"]), "packetId": pid,
                                      "packetHash": packet["packetHash"], "reviewer": string(item["reviewer"]), "role": item["role"],
                                      "status": "pending", "response": None})
            path = Path(request["state"]).parent / ("attempt-" + sha(frame(item["reviewId"])) + ".json")
            outgoing.append((path, encoded(packet)))
        validate_lifecycle(state, current_packets)
        transition = {"revision": state["revision"] + 1, "invalidated": sorted(invalidated),
                      "preserved": sorted(set(after) - invalidated), "attempts": [], "baselines": []}
        for path, data in outgoing:
            transition["attempts"].append(materialize_exact(path, data))
        for pid in sorted(baselines):
            transition["baselines"].append(materialize_exact(baseline_path(request["state"], pid, baselines[pid]), baseline_verdicts(state, pid)))
        transition_path = Path(request["state"]).parent / ("transition-" + str(state["revision"] + 1) + ".json")
        artifacts.append(materialize_exact(transition_path, encoded(transition)))
        summary = {"invalidatedCount": len(invalidated), "preservedCount": len(set(after) - invalidated), "newAttempts": len(attempts),
                   "baselines": len(baselines)}
    raw = read_bytes(request["state"])
    artifacts.insert(0, {"path": request["state"], "bytes": len(raw), "sha256": sha(raw)})
    return artifacts, summary


def verify_pages(request):
    """Check retained server pages and an already materialized file; never write or fetch."""
    require(request["packet"] is None and request["state"] is None
            and request["dependencies"] == [] and request["range"] is None,
            "verify", "Page verification cannot be combined with packet/state verification.")
    expected = request["expected"]
    closed(expected, "format pages materialized resolutionId locale view target section indexDigest contentDigest rulesDigest protocolVersion")
    require(expected["format"] == "paged-v1" and expected["locale"] in ("fr", "en")
            and expected["view"] in ("index", "rules", "custom", "protocol", "deepen"),
            "page_binding", "Unknown page format, locale or view.")
    if expected["resolutionId"] is not None:
        identifier(expected["resolutionId"])
    deepen = expected["view"] == "deepen"
    require(deepen or expected["resolutionId"] is not None, "page_binding", "Review pages require a resolution.")
    if deepen or expected["view"] in ("rules", "custom"):
        string(expected["target"])
    else:
        require(expected["target"] is None, "page_binding", "This view has no target.")
    require(deepen or expected["section"] is None, "page_binding", "Only deepen has sections.")
    if expected["section"] is not None:
        string(expected["section"])
    for key in ("indexDigest", "contentDigest"):
        digest(expected[key])
    if expected["rulesDigest"] is not None:
        digest(expected["rulesDigest"])
    require((expected["view"] == "rules" or expected["rulesDigest"] is None)
            and expected["protocolVersion"] == (None if deepen else VERSION),
            "page_binding", "Unexpected rules digest or protocol version.")
    if expected["view"] == "index":
        require(expected["indexDigest"] == expected["contentDigest"], "page_binding", "Index hashes differ.")
    paths = array(expected["pages"])
    require(0 < len(paths) <= 4096 and len(set(map(string, paths))) == len(paths),
            "page_count", "Use 1..4096 distinct advancing page files.")
    offset, total, finished = 0, None, False
    materialized = bytearray()
    for path in paths:
        page = read_json(path, 16384)
        if deepen:
            closed(page, "format resolutionId locale results")
            results = array(page["results"])
            require(0 < len(results) <= 20, "page_count", "Invalid deepen batch.")
            for item in results:
                closed(item, "uri section status contentDigest indexDigest offset totalBytes fragment nextCursor complete")
            matches = [item for item in results if item["uri"] == expected["target"] and item["section"] == expected["section"]]
            require(len(matches) == 1, "page_binding", "Expected exactly one URI/section in each retained page.")
            fragment = matches[0]
            require(fragment["status"] == "ok", "page_unavailable", "Missing or inaccessible content cannot become an empty success.")
        else:
            closed(page, "format resolutionId locale view target indexDigest contentDigest rulesDigest protocolVersion offset totalBytes fragment nextCursor complete")
            require(all(page[key] == expected[key] for key in ("view", "target", "rulesDigest", "protocolVersion")),
                    "page_binding", "View, target, rules or protocol changed.")
            fragment = page
        require(all(page[key] == expected[key] for key in ("format", "resolutionId", "locale"))
                and all(fragment[key] == expected[key] for key in ("indexDigest", "contentDigest")),
                "page_binding", "Scope or content changed; revalidate remotely before reusing cache.")
        require(type(fragment["offset"]) is int and fragment["offset"] == offset and not finished
                and type(fragment["totalBytes"]) is int and 0 <= fragment["totalBytes"] <= ARTIFACT_LIMIT,
                "page_order", "Missing, repeated, reordered or oversized page.")
        if total is None:
            total = fragment["totalBytes"]
        require(fragment["totalBytes"] == total and type(fragment["fragment"]) is str,
                "page_binding", "Page length or fragment type changed.")
        raw = fragment["fragment"].encode("utf-8", "strict")
        require(raw or total == 0, "page_deferred", "Deferred pages do not advance; retain only advancing pages for this URI.")
        offset += len(raw)
        require(offset <= total and type(fragment["complete"]) is bool
                and fragment["complete"] == (offset == total), "page_completion", "Invalid completion or byte count.")
        finished = fragment["complete"]
        if finished:
            require(fragment["nextCursor"] is None, "page_completion", "Completed page has a cursor.")
        else:
            string(fragment["nextCursor"])
        materialized.extend(raw)
    require(finished and sha(materialized) == expected["contentDigest"],
            "page_digest", "Incomplete content or invalid exact UTF-8 digest.")
    require(read_bytes(expected["materialized"]) == materialized,
            "page_materialization", "Materialized bytes differ from the verified fragments.")
    return [], {"format": "paged-v1", "pages": len(paths), "bytes": offset,
                "contentDigest": expected["contentDigest"], "complete": True,
                "remoteAuthorizationVerified": False}


def verify(request):
    if type(request["expected"]) is dict and request["expected"].get("format") == "paged-v1":
        return verify_pages(request)
    require((request["state"] is None) != (request["packet"] is None), "verify", "Choose packet verification or global state verification.")
    if request["state"] is not None:
        require(request["expected"] is None and request["dependencies"] == [], "verify", "Global verification reads its bound plan.")
        state, packets, state_hash = load_state(request["state"])
        value, _ = global_bindings(state, packets)
        summary = {"packets": len(packets), "entries": len(value["entries"]), "stateHash": state_hash}
    else:
        packet = read_json(request["packet"], PACKET_LIMIT)
        closed_packet(packet)
        expected = request["expected"]
        closed(expected, "plan packetId packetHash reviewId")
        plan = read_json(expected["plan"])
        require(all(packet.get(key) == expected[key] for key in ("packetId", "packetHash", "reviewId")),
                "binding", "Packet differs from the coordinator's expected attempt/hash.")
        packets = [packet]
        for dependency in array(request["dependencies"]):
            closed(dependency, "path packetId packetHash")
            item = read_json(dependency["path"], PACKET_LIMIT)
            closed_packet(item)
            require(all(item.get(key) == dependency[key] for key in ("packetId", "packetHash")),
                    "binding", "Upstream packet differs from expected binding.")
            packets.append(item)
        by_id = validate_graph(packets)
        obligations = validate_plan(plan)
        require(all(("version" in item) == fragmented_protocol(plan) for item in packets), "version", "Packet format differs from the plan protocol.")
        for item in packets:
            check_bindings(item, plan, obligations)
        if "version" in packet and packet["obligationIds"]:
            assembled_entries(read_items(packet, by_id))
        summary = {"packetId": packet["packetId"], "packetHash": packet["packetHash"], "entries": len(packet["entries"])}
    if request["range"] is not None:
        value = request["range"]
        closed(value, "offset count")
        require(type(value["offset"]) is int and value["offset"] >= 0 and type(value["count"]) is int and 1 <= value["count"] <= 20,
                "range", "Use a nonnegative offset and count from 1 to 20.")
        all_entries = [e for p in packets for e in p["entries"]]
        end = min(value["offset"] + value["count"], len(all_entries))
        summary["range"] = [{"entryId": e["entryId"], "bytes": len(e["content"].encode())} for e in all_entries[value["offset"]:end]]
        summary["nextOffset"] = end if end < len(all_entries) else None
    return [], summary


def reading(request):
    """One bounded page of the packets an attempt requires, in diff order; never returns their content."""
    state, packets, _ = load_state(request["state"])
    _, catalog = global_bindings(state, packets)
    require(state["schemaVersion"] == 2, "version", "This state predates the reading plan.")
    by_id = {p["packetId"]: p for p in packets}
    attempt = next((a for a in state["attempts"] if a["reviewId"] == string(request["reviewId"])), None)
    require(attempt is not None and attempt["status"] != "revoked" and attempt["packetHash"] == by_id[attempt["packetId"]]["packetHash"],
            "attempt", "Name the reviewId of a current attempt of this state.")
    packet = by_id[attempt["packetId"]]
    scope = reading_scope(state, packet, by_id, attempt["role"])
    closure = packet_closure(packet, by_id)
    offset, count = request["offset"], request["count"]
    require(type(offset) is int and 0 <= offset < len(closure) and type(count) is int and 1 <= count <= 20,
            "range", "Use an offset inside the list and a count from 1 to 20.")
    def position(item):
        return (catalog[item["entries"][0]["entryId"]][0] if item["entries"] else -1, item["packetId"])
    ordered_packets = [p for p in sorted(closure.values(), key=position)]
    stored = {p["packetId"]: d for p, d in zip(packets, state["packets"])}
    # count is an upper bound: long paths shorten the page, nextOffset carries the rest.
    items, size = [], 0
    for item in ordered_packets[offset:offset + count]:
        value = {"packetId": item["packetId"], "packetHash": item["packetHash"], "path": stored[item["packetId"]]["path"],
                 "bytes": stored[item["packetId"]]["bytes"], "entries": len(item["entries"]), "required": item["packetId"] in scope["required"]}
        size += len(encoded(value))
        if items and size > READING_PAGE_BYTES:
            break
        items.append(value)
    end = offset + len(items)
    baseline = None
    if scope["baseline"] is not None:
        path = baseline_path(request["state"], packet["packetId"], state["baselines"][packet["packetId"]])
        require(path.exists() and read_bytes(str(path)) == baseline_verdicts(state, packet["packetId"]), "baseline",
                "The baseline file written by invalidate is missing or altered; restore its exact bytes from the state before reading.")
        baseline = {"reviewId": scope["baseline"], "path": str(path)}
    # The digest only comes with the last page, so that a reviewer has the list in hand before answering.
    finished = end >= len(ordered_packets)
    return [], {"packetId": packet["packetId"], "reviewId": attempt["reviewId"], "mode": scope["mode"], "baseline": baseline,
                "readingDigest": scope["digest"] if finished else None, "packets": len(ordered_packets), "required": len(scope["required"]),
                "items": items, "nextOffset": None if finished else end}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=REQUESTS)
    parser.add_argument("--input", required=True)
    args = parser.parse_args()
    response = {"schemaVersion": 1, "operation": args.operation, "ok": False, "artifacts": [], "summary": {}, "errors": []}
    try:
        require(sys.version_info >= (3, 10), "python_version", "Python 3.10 or later is required.")
        request = read_json(canonical_parent(args.input))
        closed(request, REQUESTS[args.operation])
        canonical_request(request)
        require(type(request["schemaVersion"]) is int and request["schemaVersion"] == 1, "version", "Only request schemaVersion 1 is supported.")
        response["artifacts"], response["summary"] = globals()[args.operation](request)
        response["ok"] = True
    except Refusal as error:
        response["errors"] = [{"code": error.code, "message": str(error)}]
    except (OSError, UnicodeError, ValueError, TypeError, KeyError, RecursionError):
        response["errors"] = [{"code": "input_or_io", "message": "Invalid or unavailable local input; check paths, permissions and schema. No automatic retry."}]
    output = encoded(response)
    if len(output) > OUTPUT_LIMIT:
        response.update(ok=False, artifacts=[], summary={}, errors=[{"code": "output_size", "message": "Summary exceeds output limit; use shorter paths or a smaller range."}])
        output = encoded(response)
    sys.stdout.buffer.write(output + b"\n")
    return 0 if response["ok"] else 1


if __name__ == "__main__":
    sys.exit(main())
