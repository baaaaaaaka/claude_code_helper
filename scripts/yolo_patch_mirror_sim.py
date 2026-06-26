#!/usr/bin/env python3
"""Simulate a YOLO patched-binary mirror cache under crossed launch scenarios.

The script intentionally does not call the real claude-proxy code.  It models
the proposed mirror protocol with real files, concurrent launch workers, active
lease files, LRU cleanup, cache corruption, stale leases, and disk-usage
tracking.  The goal is to validate the shape of the mechanism before it is
ported into Go tests and implementation code.
"""

from __future__ import annotations

import argparse
import concurrent.futures
import dataclasses
import hashlib
import itertools
import json
import os
import random
import shutil
import stat
import tempfile
import threading
import time
import uuid
from pathlib import Path
from typing import Iterable


MODE_OFF = "off"
MODE_BYPASS = "bypass"
MODE_RULES = "rules"

SPECS = ("policy-v1", "policy-v2")
DURATIONS = ("short", "long")
MODES = (MODE_OFF, MODE_BYPASS, MODE_RULES)

OS_LINUX = "linux"
OS_DARWIN = "darwin"
OS_WINDOWS = "windows"
OS_SHARED = "shared"


@dataclasses.dataclass(frozen=True)
class OSProfile:
    name: str
    exe_name: str
    host_id: str
    host_scoped_cache: bool
    requires_codesign: bool
    active_delete_blocks: bool
    notes: tuple[str, ...]


OS_PROFILES = {
    OS_LINUX: OSProfile(
        name=OS_LINUX,
        exe_name="claude",
        host_id="linux-host",
        host_scoped_cache=False,
        requires_codesign=False,
        active_delete_blocks=False,
        notes=("posix-unlink", "pid-starttime-identity"),
    ),
    OS_DARWIN: OSProfile(
        name=OS_DARWIN,
        exe_name="claude",
        host_id="darwin-host",
        host_scoped_cache=False,
        requires_codesign=True,
        active_delete_blocks=False,
        notes=("posix-unlink", "codesign-required", "pid-starttime-identity"),
    ),
    OS_WINDOWS: OSProfile(
        name=OS_WINDOWS,
        exe_name="claude.exe",
        host_id="windows-host",
        host_scoped_cache=False,
        requires_codesign=False,
        active_delete_blocks=True,
        notes=("active-exe-delete-blocked", "pid-creation-time-identity"),
    ),
    OS_SHARED: OSProfile(
        name=OS_SHARED,
        exe_name="claude",
        host_id="shared-host-a",
        host_scoped_cache=True,
        requires_codesign=False,
        active_delete_blocks=False,
        notes=("host-scoped-cache", "foreign-host-cache-preserved"),
    ),
}


@dataclasses.dataclass(frozen=True)
class LaunchCase:
    version: str
    mode: str
    spec: str
    duration: str


@dataclasses.dataclass
class MirrorRef:
    mode: str
    key: str
    source_path: Path
    launch_path: Path
    lease_path: Path | None = None
    process: "ProcessHandle | None" = None


@dataclasses.dataclass(frozen=True)
class ProcessHandle:
    pid: int
    start_token: int


@dataclasses.dataclass
class Stats:
    launches: int = 0
    yolo_launches: int = 0
    off_launches: int = 0
    mirrors_created: int = 0
    mirrors_reused: int = 0
    mirrors_rebuilt: int = 0
    cleanup_runs: int = 0
    pressure_cleanup_runs: int = 0
    cleanup_removed: int = 0
    cleanup_remove_blocked: int = 0
    stale_leases_removed: int = 0
    pid_identity_active: int = 0
    pid_identity_stale: int = 0
    codesign_runs: int = 0
    foreign_host_dirs_preserved: int = 0
    startup_failures_recorded: int = 0
    soft_budget_exceeded_after_cleanup: int = 0
    invariant_checks: int = 0
    peak_cache_bytes: int = 0
    peak_cache_base_bytes: int = 0
    peak_mirror_dirs: int = 0

    def snapshot(self) -> dict[str, int]:
        return dataclasses.asdict(self)


class ProcessTable:
    """Small deterministic process table for PID-reuse simulation."""

    def __init__(self, first_pid: int = 10000) -> None:
        self.lock = threading.RLock()
        self.next_pid = first_pid
        self.next_start_token = 1
        self.alive: dict[int, int] = {}

    def start(self, *, reuse_pid: int | None = None) -> ProcessHandle:
        with self.lock:
            if reuse_pid is None:
                pid = self.next_pid
                self.next_pid += 1
            else:
                pid = reuse_pid
            token = self.next_start_token
            self.next_start_token += 1
            self.alive[pid] = token
            return ProcessHandle(pid=pid, start_token=token)

    def exit(self, handle: ProcessHandle | None) -> None:
        if handle is None:
            return
        with self.lock:
            if self.alive.get(handle.pid) == handle.start_token:
                del self.alive[handle.pid]

    def is_alive(self, pid: int, start_token: int) -> bool:
        with self.lock:
            return self.alive.get(pid) == start_token


class MirrorManager:
    def __init__(
        self,
        source_root: Path,
        cache_base: Path,
        *,
        profile: OSProfile,
        max_mirrors: int,
        max_cache_bytes: int,
        grace_seconds: float,
        lease_ttl_seconds: float,
    ) -> None:
        self.source_root = source_root
        self.profile = profile
        self.cache_base = cache_base
        if profile.host_scoped_cache:
            self.cache_root = cache_base / profile.host_id
        else:
            self.cache_root = cache_base
        self.max_mirrors = max_mirrors
        self.max_cache_bytes = max_cache_bytes
        self.grace_seconds = grace_seconds
        self.lease_ttl_seconds = lease_ttl_seconds
        self.lock = threading.RLock()
        self.stats = Stats()
        self.processes = ProcessTable()
        self.source_paths: dict[str, Path] = {}
        self.source_hashes: dict[str, str] = {}
        self.source_logical_sizes: dict[str, int] = {}

    def seed_sources(
        self,
        versions: Iterable[str],
        physical_source_size: int,
        logical_source_size: int,
        source_file: Path | None = None,
    ) -> None:
        self.source_root.mkdir(parents=True, exist_ok=True)
        for version in versions:
            path = self.source_root / version / self.profile.exe_name
            path.parent.mkdir(parents=True, exist_ok=True)
            source_label = version if source_file is None else f"{version}:{source_file}"
            path.write_bytes(make_source_bytes(source_label, physical_source_size))
            path.chmod(0o755)
            self.source_paths[version] = path
            self.source_hashes[version] = sha256_file(path)
            self.source_logical_sizes[version] = logical_source_size

    def prepare(self, case: LaunchCase) -> MirrorRef:
        with self.lock:
            self.stats.launches += 1
            source_path = self.source_paths[case.version]
            if case.mode == MODE_OFF:
                self.stats.off_launches += 1
                ref = MirrorRef(case.mode, "", source_path, source_path)
                self._record_peak_locked()
                return ref

            self.stats.yolo_launches += 1
            source_data = source_path.read_bytes()
            source_logical_size = self.source_logical_sizes[case.version]
            source_sha = sha256_bytes(source_data)
            patched = patch_bytes(source_data, case.spec)
            patched_sha = sha256_bytes(patched)
            patched_logical_size = source_logical_size
            specs_hash = sha256_text(case.spec)
            key = f"yolo-{case.version}-{case.spec}-{source_sha[:12]}-{patched_sha[:12]}"
            mirror_dir = self.cache_root / key
            mirror_path = mirror_dir / self.profile.exe_name
            manifest_path = mirror_dir / "manifest.json"

            valid = self._valid_mirror_locked(
                mirror_path,
                manifest_path,
                source_sha=source_sha,
                specs_hash=specs_hash,
                patched_sha=patched_sha,
            )
            if not valid:
                was_present = mirror_path.exists() or manifest_path.exists()
                self._cleanup_locked(
                    keep_key=key,
                    pressure=True,
                    needed_bytes=patched_logical_size,
                )
                mirror_dir.mkdir(parents=True, exist_ok=True)
                tmp_path = mirror_dir / f"claude.tmp-{uuid.uuid4().hex}"
                tmp_path.write_bytes(patched)
                tmp_path.chmod(0o755)
                os.replace(tmp_path, mirror_path)
                signed = self._codesign_if_needed_locked(mirror_dir, mirror_path)
                write_json_atomic(
                    manifest_path,
                    {
                        "osProfile": self.profile.name,
                        "hostId": self.profile.host_id,
                        "sourceSha256": source_sha,
                        "specsSha256": specs_hash,
                        "patchedSha256": patched_sha,
                        "exeName": self.profile.exe_name,
                        "mirrorLogicalBytes": patched_logical_size,
                        "version": case.version,
                        "spec": case.spec,
                        "signed": signed,
                        "createdAt": time.time(),
                    },
                )
                if was_present:
                    self.stats.mirrors_rebuilt += 1
                else:
                    self.stats.mirrors_created += 1
            else:
                self.stats.mirrors_reused += 1

            process = self.processes.start()
            lease_path = mirror_dir / f"active.{process.pid}.{process.start_token}.{uuid.uuid4().hex}"
            write_json_atomic(
                lease_path,
                {
                    "pid": process.pid,
                    "startToken": process.start_token,
                    "startedAt": time.time(),
                    "heartbeatAt": time.time(),
                    "mode": case.mode,
                },
            )
            touch(mirror_path)
            touch(mirror_dir)
            self._cleanup_locked(keep_key=key, pressure=False, needed_bytes=0)
            self._record_peak_locked()
            return MirrorRef(case.mode, key, source_path, mirror_path, lease_path, process)

    def release(self, ref: MirrorRef) -> None:
        if ref.lease_path is None:
            return
        with self.lock:
            try:
                ref.lease_path.unlink()
            except FileNotFoundError:
                pass
            self.processes.exit(ref.process)
            self._cleanup_locked(keep_key=ref.key, pressure=False, needed_bytes=0)
            self._record_peak_locked()

    def heartbeat(self, ref: MirrorRef) -> None:
        if ref.lease_path is None:
            return
        with self.lock:
            if not ref.lease_path.exists():
                return
            try:
                data = json.loads(ref.lease_path.read_text())
            except (OSError, json.JSONDecodeError):
                data = {}
            data["heartbeatAt"] = time.time()
            write_json_atomic(ref.lease_path, data)

    def record_startup_failure(self, ref: MirrorRef) -> None:
        with self.lock:
            if ref.lease_path is not None:
                try:
                    ref.lease_path.unlink()
                except FileNotFoundError:
                    pass
            self.processes.exit(ref.process)
            if ref.key:
                self._remove_mirror_dir_locked(self.cache_root / ref.key)
            self.stats.startup_failures_recorded += 1
            self._record_peak_locked()

    def corrupt_mirror(self, ref: MirrorRef) -> None:
        if not ref.key:
            raise AssertionError("cannot corrupt a non-yolo launch")
        with self.lock:
            ref.launch_path.write_bytes(b"corrupt mirror")
            touch(ref.launch_path)

    def force_cleanup(self, keep_key: str = "") -> None:
        with self.lock:
            self._cleanup_locked(keep_key=keep_key, pressure=True, needed_bytes=0)
            self._record_peak_locked()

    def seed_foreign_host_cache(self) -> Path:
        foreign_root = self.cache_base / "foreign-host"
        foreign_dir = foreign_root / "yolo-foreign-host-cache-entry"
        foreign_dir.mkdir(parents=True, exist_ok=True)
        (foreign_dir / self.profile.exe_name).write_bytes(b"foreign host mirror")
        (foreign_dir / "manifest.json").write_text('{"hostId":"foreign-host"}\n')
        return foreign_dir

    def assert_sources_unchanged(self) -> None:
        for version, path in self.source_paths.items():
            got = sha256_file(path)
            want = self.source_hashes[version]
            if got != want:
                raise AssertionError(f"source mutated for {version}: {got} != {want}")

    def cache_bytes(self) -> int:
        return cache_tree_size(self.cache_root)

    def cache_base_bytes(self) -> int:
        return cache_tree_size(self.cache_base)

    def mirror_dir_count(self) -> int:
        if not self.cache_root.exists():
            return 0
        return sum(1 for p in self.cache_root.iterdir() if p.is_dir() and p.name.startswith("yolo-"))

    def _valid_mirror_locked(
        self,
        mirror_path: Path,
        manifest_path: Path,
        *,
        source_sha: str,
        specs_hash: str,
        patched_sha: str,
    ) -> bool:
        if not mirror_path.is_file() or not manifest_path.is_file():
            return False
        try:
            manifest = json.loads(manifest_path.read_text())
        except (OSError, json.JSONDecodeError):
            return False
        if manifest.get("sourceSha256") != source_sha:
            return False
        if manifest.get("osProfile") != self.profile.name:
            return False
        if manifest.get("hostId") != self.profile.host_id:
            return False
        if manifest.get("specsSha256") != specs_hash:
            return False
        if manifest.get("patchedSha256") != patched_sha:
            return False
        if self.profile.requires_codesign and not manifest.get("signed"):
            return False
        if self.profile.requires_codesign and not (mirror_path.parent / ".codesign").is_file():
            return False
        if sha256_file(mirror_path) != patched_sha:
            return False
        mode = mirror_path.stat().st_mode
        return bool(mode & (stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH))

    def _cleanup_locked(self, *, keep_key: str, pressure: bool, needed_bytes: int) -> None:
        self.stats.cleanup_runs += 1
        if pressure:
            self.stats.pressure_cleanup_runs += 1
        if not self.cache_root.exists():
            return

        now = time.time()
        entries = self._mirror_entries_locked(now)
        if not entries:
            return

        keep: set[str] = set()
        if keep_key:
            keep.add(keep_key)
        for entry in entries:
            if entry["active"]:
                keep.add(entry["key"])

        newest = sorted(entries, key=lambda e: e["mtime"], reverse=True)
        for entry in newest:
            if len(keep) >= self.max_mirrors:
                break
            keep.add(entry["key"])

        total_size = sum(int(e["size"]) for e in entries)
        total_count = len(entries)
        for entry in sorted(entries, key=lambda e: e["mtime"]):
            if entry["key"] in keep:
                continue
            if not pressure and entry["age"] < self.grace_seconds:
                continue
            if total_count <= self.max_mirrors and total_size + needed_bytes <= self.max_cache_bytes:
                break
            if self._remove_mirror_dir_locked(entry["path"]):
                self.stats.cleanup_removed += 1
                total_size -= int(entry["size"])
                total_count -= 1

        if total_size + needed_bytes > self.max_cache_bytes:
            self.stats.soft_budget_exceeded_after_cleanup += 1

    def _codesign_if_needed_locked(self, mirror_dir: Path, mirror_path: Path) -> bool:
        if not self.profile.requires_codesign:
            return False
        signature = {
            "signedPath": str(mirror_path),
            "signedSha256": sha256_file(mirror_path),
            "signedAt": time.time(),
        }
        write_json_atomic(mirror_dir / ".codesign", signature)
        self.stats.codesign_runs += 1
        return True

    def _remove_mirror_dir_locked(self, path: Path) -> bool:
        if self.profile.active_delete_blocks and self._path_has_live_process_locked(path):
            self.stats.cleanup_remove_blocked += 1
            return False
        shutil.rmtree(path, ignore_errors=True)
        return True

    def _path_has_live_process_locked(self, path: Path) -> bool:
        for lease in path.glob("active.*"):
            try:
                data = json.loads(lease.read_text())
                pid = int(data["pid"])
                start_token = int(data["startToken"])
            except (OSError, ValueError, KeyError, TypeError, json.JSONDecodeError):
                continue
            if self.processes.is_alive(pid, start_token):
                return True
        return False

    def _mirror_entries_locked(self, now: float) -> list[dict[str, object]]:
        entries: list[dict[str, object]] = []
        for path in self.cache_root.iterdir():
            if not path.is_dir() or not path.name.startswith("yolo-"):
                continue
            active = False
            for lease in path.glob("active.*"):
                try:
                    age = now - lease.stat().st_mtime
                except FileNotFoundError:
                    continue
                identity_known = False
                identity_active = False
                try:
                    data = json.loads(lease.read_text())
                    pid = int(data["pid"])
                    start_token = int(data["startToken"])
                    identity_known = True
                    identity_active = self.processes.is_alive(pid, start_token)
                except (OSError, ValueError, KeyError, TypeError, json.JSONDecodeError):
                    identity_known = False
                if identity_known and identity_active:
                    self.stats.pid_identity_active += 1
                    active = True
                    continue
                if identity_known:
                    self.stats.pid_identity_stale += 1
                if not identity_known and age < self.lease_ttl_seconds:
                    active = True
                    continue
                if age > self.grace_seconds:
                    try:
                        lease.unlink()
                        self.stats.stale_leases_removed += 1
                    except FileNotFoundError:
                        pass
                    continue
            try:
                mtime = path.stat().st_mtime
            except FileNotFoundError:
                continue
            entries.append(
                {
                    "key": path.name,
                    "path": path,
                    "size": mirror_dir_size(path),
                    "mtime": mtime,
                    "age": now - mtime,
                    "active": active,
                }
            )
        return entries

    def _record_peak_locked(self) -> None:
        cache_bytes = self.cache_bytes()
        cache_base_bytes = self.cache_base_bytes()
        self.stats.peak_cache_bytes = max(self.stats.peak_cache_bytes, cache_bytes)
        self.stats.peak_cache_base_bytes = max(
            self.stats.peak_cache_base_bytes, cache_base_bytes
        )
        self.stats.peak_mirror_dirs = max(self.stats.peak_mirror_dirs, self.mirror_dir_count())


def make_source_bytes(version: str, size: int) -> bytes:
    header = f"CLAUDE-CODE {version}\n".encode()
    block = (
        header
        + b"POLICY=ASK___\n"
        + b"REMOTE=ON____\n"
        + b"ROOT=BLOCK__\n"
        + b"PAYLOAD="
        + sha256_text(version).encode()
        + b"\n"
    )
    repeated = block * ((size // len(block)) + 1)
    return repeated[:size]


def patch_bytes(data: bytes, spec: str) -> bytes:
    patched = data.replace(b"POLICY=ASK___", b"POLICY=ALLOW_")
    if spec == "policy-v2":
        patched = patched.replace(b"REMOTE=ON____", b"REMOTE=OFF___")
        patched = patched.replace(b"ROOT=BLOCK__", b"ROOT=ALLOW__")
    if patched == data and data:
        patched = patch_opaque_bytes(data, spec)
    return patched


def patch_opaque_bytes(data: bytes, spec: str) -> bytes:
    patched = bytearray(data)
    digest = hashlib.sha256(spec.encode()).digest()
    for offset, byte in enumerate(digest):
        index = (((offset + 1) * 104729) + byte) % len(patched)
        patched[index] ^= byte or 1
    return bytes(patched)


def write_json_atomic(path: Path, data: dict[str, object]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f"{path.name}.tmp-{uuid.uuid4().hex}")
    tmp.write_text(json.dumps(data, sort_keys=True) + "\n")
    os.replace(tmp, path)


def touch(path: Path) -> None:
    now = time.time()
    os.utime(path, (now, now))


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_text(data: str) -> str:
    return hashlib.sha256(data.encode()).hexdigest()


def dir_size(path: Path) -> int:
    if not path.exists():
        return 0
    total = 0
    for root, _, files in os.walk(path):
        for name in files:
            try:
                total += (Path(root) / name).stat().st_size
            except FileNotFoundError:
                pass
    return total


def cache_tree_size(path: Path) -> int:
    if not path.exists():
        return 0
    if path.name.startswith("yolo-") and path.is_dir():
        return mirror_dir_size(path)

    total = 0
    for root, dirs, files in os.walk(path):
        root_path = Path(root)
        if root_path.name.startswith("yolo-"):
            total += mirror_dir_size(root_path)
            dirs[:] = []
            continue
        for name in files:
            try:
                total += (root_path / name).stat().st_size
            except FileNotFoundError:
                pass
    return total


def mirror_dir_size(path: Path) -> int:
    total = dir_size(path)
    manifest_path = path / "manifest.json"
    if not manifest_path.is_file():
        return total
    try:
        manifest = json.loads(manifest_path.read_text())
        logical_size = int(manifest["mirrorLogicalBytes"])
        exe_name = str(manifest["exeName"])
    except (OSError, ValueError, KeyError, TypeError, json.JSONDecodeError):
        return total

    exe_path = path / exe_name
    try:
        total -= exe_path.stat().st_size
    except FileNotFoundError:
        pass
    return total + logical_size


def run_launch(manager: MirrorManager, case: LaunchCase, rng_seed: int) -> None:
    rng = random.Random(rng_seed)
    ref = manager.prepare(case)
    try:
        with manager.lock:
            manager.stats.invariant_checks += 1
        if case.mode == MODE_OFF:
            if ref.launch_path != ref.source_path:
                raise AssertionError(f"off launch used mirror: {ref}")
        else:
            if ref.launch_path == ref.source_path:
                raise AssertionError(f"yolo launch used source: {ref}")
            if not ref.launch_path.is_file():
                raise AssertionError(f"mirror missing before launch: {ref.launch_path}")
            if ref.lease_path is None or not ref.lease_path.is_file():
                raise AssertionError(f"missing active lease for {ref.key}")

        if case.duration == "long":
            deadline = time.time() + rng.uniform(0.03, 0.08)
            while time.time() < deadline:
                time.sleep(0.01)
                manager.heartbeat(ref)
        else:
            time.sleep(rng.uniform(0.001, 0.008))

        if case.mode != MODE_OFF and not ref.launch_path.is_file():
            raise AssertionError(f"mirror removed while launch was active: {ref.launch_path}")
    finally:
        manager.release(ref)


def run_corruption_case(manager: MirrorManager, version: str) -> None:
    case = LaunchCase(version=version, mode=MODE_BYPASS, spec="policy-v1", duration="short")
    ref = manager.prepare(case)
    manager.release(ref)
    manager.corrupt_mirror(ref)
    ref2 = manager.prepare(case)
    try:
        if sha256_file(ref2.launch_path) == sha256_bytes(b"corrupt mirror"):
            raise AssertionError("corrupted mirror was reused")
    finally:
        manager.release(ref2)


def run_startup_failure_case(manager: MirrorManager, version: str) -> None:
    case = LaunchCase(version=version, mode=MODE_RULES, spec="policy-v2", duration="short")
    ref = manager.prepare(case)
    manager.record_startup_failure(ref)
    if ref.launch_path.exists():
        raise AssertionError("startup failure did not remove failed mirror")


def run_stale_lease_case(manager: MirrorManager, version_a: str, version_b: str) -> None:
    stale = manager.prepare(LaunchCase(version_a, MODE_BYPASS, "policy-v1", "long"))
    if stale.lease_path is None:
        raise AssertionError("expected stale case to create lease")
    manager.processes.exit(stale.process)
    old = time.time() - manager.lease_ttl_seconds - manager.grace_seconds - 10
    os.utime(stale.lease_path, (old, old))
    os.utime(stale.launch_path.parent, (old, old))

    current = manager.prepare(LaunchCase(version_b, MODE_RULES, "policy-v2", "short"))
    manager.release(current)
    manager.force_cleanup(keep_key=current.key)


def run_pid_reuse_case(manager: MirrorManager, version_a: str, version_b: str) -> None:
    stale = manager.prepare(LaunchCase(version_a, MODE_RULES, "policy-v1", "long"))
    if stale.lease_path is None or stale.process is None:
        raise AssertionError("expected pid reuse case to create process lease")
    old_pid = stale.process.pid
    old_token = stale.process.start_token
    stale_dir = stale.launch_path.parent
    manager.processes.exit(stale.process)
    reused = manager.processes.start(reuse_pid=old_pid)
    if reused.start_token == old_token:
        raise AssertionError("pid reuse simulation did not change start token")
    old = time.time() - manager.lease_ttl_seconds - manager.grace_seconds - 10
    os.utime(stale.lease_path, (old, old))
    os.utime(stale_dir, (old, old))

    current = manager.prepare(LaunchCase(version_b, MODE_BYPASS, "policy-v2", "short"))
    manager.release(current)
    manager.force_cleanup(keep_key=current.key)
    if stale.lease_path.exists():
        raise AssertionError("pid-reused stale lease was not removed")


def run_unknown_identity_case(manager: MirrorManager, version_a: str, version_b: str) -> None:
    unknown = manager.prepare(LaunchCase(version_a, MODE_BYPASS, "policy-v2", "long"))
    if unknown.lease_path is None:
        raise AssertionError("expected unknown identity case to create lease")
    unknown.lease_path.write_text("{invalid json\n")

    current = manager.prepare(LaunchCase(version_b, MODE_RULES, "policy-v1", "short"))
    manager.release(current)
    manager.force_cleanup(keep_key=current.key)
    if not unknown.launch_path.exists():
        raise AssertionError("fresh unknown-identity lease was cleaned too aggressively")

    manager.processes.exit(unknown.process)
    old = time.time() - manager.lease_ttl_seconds - manager.grace_seconds - 10
    os.utime(unknown.lease_path, (old, old))
    os.utime(unknown.launch_path.parent, (old, old))
    manager.force_cleanup(keep_key=current.key)
    if unknown.lease_path.exists():
        raise AssertionError("expired unknown-identity lease was not removed")


def run_darwin_codesign_case(manager: MirrorManager, version: str) -> None:
    if not manager.profile.requires_codesign:
        return

    case = LaunchCase(version=version, mode=MODE_BYPASS, spec="policy-v1", duration="short")
    ref = manager.prepare(case)
    sign_path = ref.launch_path.parent / ".codesign"
    try:
        if not sign_path.is_file():
            raise AssertionError("darwin mirror was not codesigned")
        sign_runs_before = manager.stats.codesign_runs
    finally:
        manager.release(ref)

    sign_path.unlink()
    ref2 = manager.prepare(case)
    try:
        if not sign_path.is_file():
            raise AssertionError("darwin mirror did not rebuild missing codesign marker")
        if manager.stats.codesign_runs <= sign_runs_before:
            raise AssertionError("darwin codesign rebuild was not counted")
    finally:
        manager.release(ref2)


def run_windows_active_delete_case(manager: MirrorManager, version: str) -> None:
    if not manager.profile.active_delete_blocks:
        return

    case = LaunchCase(version=version, mode=MODE_RULES, spec="policy-v2", duration="long")
    ref = manager.prepare(case)
    try:
        blocked_before = manager.stats.cleanup_remove_blocked
        with manager.lock:
            removed = manager._remove_mirror_dir_locked(ref.launch_path.parent)
        if removed:
            raise AssertionError("windows active mirror removal unexpectedly succeeded")
        if manager.stats.cleanup_remove_blocked <= blocked_before:
            raise AssertionError("windows blocked removal was not counted")
        if not ref.launch_path.is_file():
            raise AssertionError("windows blocked removal still removed the active mirror")
    finally:
        manager.release(ref)


def run_shared_cache_case(manager: MirrorManager) -> None:
    if not manager.profile.host_scoped_cache:
        return

    foreign_dir = manager.seed_foreign_host_cache()
    manager.force_cleanup()
    if not foreign_dir.is_dir():
        raise AssertionError("host-scoped cleanup removed a foreign host cache")
    manager.stats.foreign_host_dirs_preserved += 1


def run_os_specific_cases(manager: MirrorManager, versions: list[str]) -> None:
    run_darwin_codesign_case(manager, versions[0])
    run_windows_active_delete_case(manager, versions[min(1, len(versions) - 1)])
    run_shared_cache_case(manager)


def build_cases(versions: list[str], repeats: int) -> list[LaunchCase]:
    base = [
        LaunchCase(version, mode, spec, duration)
        for version, mode, spec, duration in itertools.product(versions, MODES, SPECS, DURATIONS)
    ]
    cases: list[LaunchCase] = []
    for _ in range(repeats):
        cases.extend(base)
    return cases


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--seed", type=int, default=20260626)
    parser.add_argument(
        "--os-profile",
        choices=tuple(OS_PROFILES) + ("all",),
        default="all",
        help="OS constraint profile to simulate; all runs linux, darwin, windows, and shared-cache profiles.",
    )
    parser.add_argument("--versions", type=int, default=8)
    parser.add_argument("--repeats", type=int, default=2)
    parser.add_argument("--workers", type=int, default=32)
    parser.add_argument("--source-size", type=int, default=128 * 1024)
    parser.add_argument(
        "--logical-source-size",
        type=int,
        default=None,
        help="Logical executable size used for disk-budget accounting without writing that many bytes.",
    )
    parser.add_argument(
        "--materialized-source-size",
        type=int,
        default=128 * 1024,
        help="Physical placeholder size when --source-file or --logical-source-size supplies logical bytes.",
    )
    parser.add_argument(
        "--source-file",
        type=Path,
        default=None,
        help="Use this executable's size for logical accounting without copying its full contents.",
    )
    parser.add_argument("--max-mirrors", type=int, default=3)
    parser.add_argument("--max-cache-bytes", type=int, default=384 * 1024)
    parser.add_argument("--grace-seconds", type=float, default=0.02)
    parser.add_argument("--lease-ttl-seconds", type=float, default=30.0)
    parser.add_argument("--workdir", type=Path, default=None)
    parser.add_argument("--keep-workdir", action="store_true")
    args = parser.parse_args()
    if args.versions < 1:
        parser.error("--versions must be >= 1")
    if args.repeats < 1:
        parser.error("--repeats must be >= 1")
    if args.workers < 1:
        parser.error("--workers must be >= 1")
    if args.source_size < 1:
        parser.error("--source-size must be >= 1")
    if args.logical_source_size is not None and args.logical_source_size < 1:
        parser.error("--logical-source-size must be >= 1")
    if args.materialized_source_size < 1:
        parser.error("--materialized-source-size must be >= 1")
    if args.source_file is not None and not args.source_file.is_file():
        parser.error(f"--source-file must point to a file: {args.source_file}")
    return args


def source_sizes_from_args(args: argparse.Namespace) -> tuple[int, int]:
    logical_source_size = args.logical_source_size
    if logical_source_size is None and args.source_file is not None:
        logical_source_size = args.source_file.stat().st_size
    if logical_source_size is None:
        logical_source_size = args.source_size

    if args.source_file is not None or args.logical_source_size is not None:
        physical_source_size = args.materialized_source_size
    else:
        physical_source_size = args.source_size
    return physical_source_size, logical_source_size


def run_targeted_case(
    errors: list[str],
    name: str,
    fn,
    *fn_args,
) -> None:
    try:
        fn(*fn_args)
    except Exception as exc:  # noqa: BLE001 - keep collecting simulation failures.
        errors.append(f"{name}: {exc!r}")


def run_simulation(args: argparse.Namespace, profile_name: str, workdir: Path, seed: int) -> dict[str, object]:
    profile = OS_PROFILES[profile_name]
    if workdir.exists():
        shutil.rmtree(workdir)
    workdir.mkdir(parents=True)

    rng = random.Random(seed)
    source_root = workdir / "sources"
    cache_base = workdir / "cache"
    versions = [f"2.1.{190 + i}" for i in range(args.versions)]
    physical_source_size, logical_source_size = source_sizes_from_args(args)
    manager = MirrorManager(
        source_root,
        cache_base,
        profile=profile,
        max_mirrors=args.max_mirrors,
        max_cache_bytes=args.max_cache_bytes,
        grace_seconds=args.grace_seconds,
        lease_ttl_seconds=args.lease_ttl_seconds,
    )
    manager.seed_sources(versions, physical_source_size, logical_source_size, args.source_file)

    cases = build_cases(versions, args.repeats)
    rng.shuffle(cases)

    errors: list[str] = []
    run_targeted_case(errors, "corruption", run_corruption_case, manager, versions[0])
    run_targeted_case(
        errors,
        "startup_failure",
        run_startup_failure_case,
        manager,
        versions[min(1, len(versions) - 1)],
    )
    run_targeted_case(errors, "stale_lease", run_stale_lease_case, manager, versions[0], versions[-1])
    run_targeted_case(
        errors,
        "pid_reuse",
        run_pid_reuse_case,
        manager,
        versions[min(2, len(versions) - 1)],
        versions[-1],
    )
    run_targeted_case(
        errors,
        "unknown_identity",
        run_unknown_identity_case,
        manager,
        versions[min(3, len(versions) - 1)],
        versions[-1],
    )
    run_targeted_case(errors, "os_specific", run_os_specific_cases, manager, versions)

    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = [
            pool.submit(run_launch, manager, case, seed + i)
            for i, case in enumerate(cases)
        ]
        for future in concurrent.futures.as_completed(futures):
            try:
                future.result()
            except Exception as exc:  # noqa: BLE001 - report all simulation failures.
                errors.append(repr(exc))

    run_targeted_case(errors, "final_cleanup", manager.force_cleanup)
    run_targeted_case(errors, "source_integrity", manager.assert_sources_unchanged)

    physical_source_bytes = dir_size(source_root)
    source_bytes = sum(manager.source_logical_sizes.values())
    final_current_host_cache_bytes = manager.cache_bytes()
    final_total_cache_base_bytes = manager.cache_base_bytes()
    final_mirror_dirs = manager.mirror_dir_count()
    combo_count = len({(c.version, c.mode, c.spec, c.duration) for c in cases})
    return {
        "seed": seed,
        "os_profile": profile.name,
        "host_id": profile.host_id,
        "host_scoped_cache": profile.host_scoped_cache,
        "os_notes": list(profile.notes),
        "workdir": str(workdir),
        "versions": len(versions),
        "crossed_combinations": combo_count,
        "parallel_cases": len(cases),
        "workers": args.workers,
        "source_kind": "file" if args.source_file is not None else "generated",
        "source_file": str(args.source_file) if args.source_file is not None else None,
        "source_file_bytes": args.source_file.stat().st_size if args.source_file is not None else None,
        "logical_source_size": logical_source_size,
        "materialized_source_size": physical_source_size,
        "source_bytes": source_bytes,
        "physical_source_bytes": physical_source_bytes,
        "peak_extra_cache_bytes": manager.stats.peak_cache_base_bytes,
        "peak_current_host_cache_bytes": manager.stats.peak_cache_bytes,
        "final_extra_cache_bytes": final_total_cache_base_bytes,
        "final_current_host_cache_bytes": final_current_host_cache_bytes,
        "final_total_cache_base_bytes": final_total_cache_base_bytes,
        "peak_mirror_dirs": manager.stats.peak_mirror_dirs,
        "final_mirror_dirs": final_mirror_dirs,
        "stats": manager.stats.snapshot(),
        "errors": errors,
        "verdict": "FAIL" if errors else "PASS",
    }


def main() -> int:
    args = parse_args()
    temp_ctx = None
    if args.workdir is None:
        temp_ctx = tempfile.TemporaryDirectory(prefix="yolo-mirror-sim-")
        workdir = Path(temp_ctx.name)
    else:
        workdir = args.workdir
        if workdir.exists():
            shutil.rmtree(workdir)
        workdir.mkdir(parents=True)

    profile_names = list(OS_PROFILES) if args.os_profile == "all" else [args.os_profile]
    summaries = [
        run_simulation(
            args,
            profile_name,
            workdir / profile_name if len(profile_names) > 1 else workdir,
            args.seed + (1000003 * i),
        )
        for i, profile_name in enumerate(profile_names)
    ]

    if len(summaries) == 1:
        result: dict[str, object] = summaries[0]
        errors = list(summaries[0]["errors"])
    else:
        errors = [
            f"{summary['os_profile']}: {error}"
            for summary in summaries
            for error in summary["errors"]
        ]
        result = {
            "seed": args.seed,
            "os_profile": "all",
            "profiles": summaries,
            "profile_count": len(summaries),
            "max_profile_peak_extra_cache_bytes": max(
                int(summary["peak_extra_cache_bytes"]) for summary in summaries
            ),
            "errors": errors,
            "verdict": "FAIL" if errors else "PASS",
        }

    print(json.dumps(result, indent=2, sort_keys=True))
    print(f"VERDICT: {'FAIL' if errors else 'PASS'}")
    if temp_ctx is not None and not args.keep_workdir:
        temp_ctx.cleanup()
    return 1 if errors else 0


if __name__ == "__main__":
    raise SystemExit(main())
