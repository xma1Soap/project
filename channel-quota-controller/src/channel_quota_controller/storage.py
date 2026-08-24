from __future__ import annotations

import json
import os
from contextlib import AbstractContextManager
from pathlib import Path
from typing import Any

from .models import Decision, RouteRuntimeState


def atomic_write_json(path: Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    with temporary.open("w", encoding="utf-8", newline="\n") as handle:
        json.dump(value, handle, ensure_ascii=False, indent=2, sort_keys=True)
        handle.write("\n")
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


class StateStore:
    def __init__(self, path: str | Path):
        self.path = Path(path)

    def load(self) -> dict[str, RouteRuntimeState]:
        if not self.path.exists():
            return {}
        with self.path.open("r", encoding="utf-8") as handle:
            raw = json.load(handle)
        return {key: RouteRuntimeState.from_dict(value) for key, value in raw.items()}

    def save(self, state: dict[str, RouteRuntimeState]) -> None:
        atomic_write_json(self.path, {key: value.to_dict() for key, value in state.items()})


class AuditLogger:
    def __init__(self, path: str | Path):
        self.path = Path(path)

    def write(self, decision: Decision) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        with self.path.open("a", encoding="utf-8", newline="\n") as handle:
            json.dump(decision.to_dict(), handle, ensure_ascii=False, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())


class SingleInstanceLock(AbstractContextManager["SingleInstanceLock"]):
    def __init__(self, path: str | Path):
        self.path = Path(path)
        self._fd: int | None = None
        self._uses_flock = False

    def __enter__(self) -> "SingleInstanceLock":
        self.path.parent.mkdir(parents=True, exist_ok=True)
        if os.name == "posix":
            import fcntl

            self._fd = os.open(self.path, os.O_CREAT | os.O_RDWR, 0o600)
            try:
                fcntl.flock(self._fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError as exc:
                os.close(self._fd)
                self._fd = None
                raise RuntimeError(
                    f"another controller instance is running: {self.path}"
                ) from exc
            self._uses_flock = True
            os.ftruncate(self._fd, 0)
        else:
            try:
                self._fd = os.open(
                    self.path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600
                )
            except FileExistsError as exc:
                raise RuntimeError(
                    f"another controller instance may be running: {self.path}"
                ) from exc
        os.write(self._fd, str(os.getpid()).encode("ascii"))
        os.fsync(self._fd)
        return self

    def __exit__(self, exc_type, exc_value, traceback) -> None:
        if self._fd is not None:
            if self._uses_flock:
                import fcntl

                fcntl.flock(self._fd, fcntl.LOCK_UN)
            os.close(self._fd)
            self._fd = None
        if not self._uses_flock:
            try:
                self.path.unlink()
            except FileNotFoundError:
                pass
