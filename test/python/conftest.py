"""Stack fixtures: spawn nats (docker), mockllm, and gateway as subprocesses.

Why subprocess for mockllm and gateway: the ghcr.io/andyjmorgan/sluice-mockllm
image is not published yet (v0.1 task), so we build the binaries from source.
NATS comes from the official 2.10 image because there's no need to compile it.

If docker is unreachable, the gateway is started with reporting disabled — the
non-NATS path still exercises wire compat end-to-end, which is what this suite
verifies.
"""

from __future__ import annotations

import contextlib
import json
import os
import shutil
import socket
import subprocess
import sys
import time
from collections.abc import Iterator
from pathlib import Path

import pytest
import requests

from helpers import API_KEY, clear_responses

REPO_ROOT = Path(__file__).resolve().parents[2]
CONFIG_DEV = REPO_ROOT / "config-dev"

GATEWAY_BIN = Path(os.environ.get("SLUICE_GATEWAY_BIN", "/tmp/sluice-gateway"))
MOCKLLM_BIN = Path(os.environ.get("SLUICE_MOCKLLM_BIN", "/tmp/sluice-mockllm"))

STARTUP_TIMEOUT = 30.0
HEALTH_INTERVAL = 0.1


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_for_http(url: str, timeout: float = STARTUP_TIMEOUT) -> None:
    deadline = time.time() + timeout
    last_err: Exception | None = None
    while time.time() < deadline:
        try:
            resp = requests.get(url, timeout=2)
            if resp.status_code < 500:
                return
            last_err = RuntimeError(f"status {resp.status_code}")
        except requests.RequestException as e:
            last_err = e
        time.sleep(HEALTH_INTERVAL)
    raise RuntimeError(f"timed out waiting for {url}: {last_err}")


def _ensure_binary(bin_path: Path, source_pkg: str) -> Path:
    if bin_path.exists():
        return bin_path
    print(f"[stack] building {bin_path} from {source_pkg}", file=sys.stderr)
    subprocess.run(
        ["go", "build", "-o", str(bin_path), source_pkg],
        cwd=REPO_ROOT,
        check=True,
    )
    return bin_path


@contextlib.contextmanager
def _docker_nats() -> Iterator[str | None]:
    """Best-effort NATS via docker. Yields URL or None if docker is unavailable."""
    if not shutil.which("docker"):
        yield None
        return

    name = f"sluice-pycompat-nats-{os.getpid()}"
    host_port = _free_port()
    try:
        subprocess.run(
            [
                "docker", "run", "-d", "--rm",
                "--name", name,
                "-p", f"{host_port}:4222",
                "nats:2.10",
                "-js",
            ],
            check=True,
            capture_output=True,
        )
    except subprocess.CalledProcessError as e:
        print(f"[stack] docker run nats failed: {e.stderr.decode()}", file=sys.stderr)
        yield None
        return

    url = f"nats://127.0.0.1:{host_port}"
    try:
        deadline = time.time() + STARTUP_TIMEOUT
        ready = False
        while time.time() < deadline:
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
                s.settimeout(0.5)
                try:
                    s.connect(("127.0.0.1", host_port))
                    ready = True
                    break
                except OSError:
                    time.sleep(HEALTH_INTERVAL)
        if not ready:
            raise RuntimeError(f"nats {url} never became reachable")
        yield url
    finally:
        subprocess.run(["docker", "rm", "-f", name], capture_output=True, check=False)


def _materialize_config(target_dir: Path, mockllm_host: str) -> None:
    target_dir.mkdir(parents=True, exist_ok=True)
    for entry in CONFIG_DEV.iterdir():
        if entry.suffix not in (".yaml", ".yml"):
            continue
        raw = entry.read_text()
        raw = raw.replace("mockllm:5555", mockllm_host)
        (target_dir / entry.name).write_text(raw)


@pytest.fixture(scope="session")
def stack(tmp_path_factory: pytest.TempPathFactory) -> Iterator[dict[str, str]]:
    _ensure_binary(MOCKLLM_BIN, "./cmd/mockllm")
    _ensure_binary(GATEWAY_BIN, "./cmd/gateway")

    mockllm_port = _free_port()
    gateway_port = _free_port()

    mock_proc = subprocess.Popen(
        [str(MOCKLLM_BIN), "--port", str(mockllm_port)],
        cwd=REPO_ROOT,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env={**os.environ, "LOG_LEVEL": "warn"},
    )

    mockllm_url = f"http://127.0.0.1:{mockllm_port}"
    try:
        _wait_for_http(f"{mockllm_url}/healthz")
    except Exception:
        mock_proc.terminate()
        raise

    with _docker_nats() as nats_url:
        config_dir = tmp_path_factory.mktemp("sluice-config")
        mockllm_host = f"127.0.0.1:{mockllm_port}"
        _materialize_config(config_dir, mockllm_host)

        prom_port = _free_port()
        gateway_env = {
            **os.environ,
            "SLUICE_CONFIG_DIR": str(config_dir),
            "SLUICE_HTTP_BIND": f"127.0.0.1:{gateway_port}",
            "SLUICE_PROMETHEUS_BIND": f"127.0.0.1:{prom_port}",
            "SLUICE_LOG_LEVEL": "warn",
        }
        if nats_url:
            gateway_env["SLUICE_NATS_URL"] = nats_url

        gateway_proc = subprocess.Popen(
            [str(GATEWAY_BIN)],
            cwd=REPO_ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=gateway_env,
        )

        gateway_url = f"http://127.0.0.1:{gateway_port}"
        try:
            _wait_for_http(f"{gateway_url}/healthz")
        except Exception:
            _dump_proc("gateway", gateway_proc)
            gateway_proc.terminate()
            mock_proc.terminate()
            raise

        try:
            yield {
                "gateway_url": gateway_url,
                "mockllm_url": mockllm_url,
                "nats_url": nats_url or "",
                "api_key": API_KEY,
            }
        finally:
            for proc in (gateway_proc, mock_proc):
                proc.terminate()
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    proc.kill()


def _dump_proc(name: str, proc: subprocess.Popen) -> None:
    try:
        out, err = proc.communicate(timeout=1)
    except subprocess.TimeoutExpired:
        proc.kill()
        out, err = proc.communicate()
    if out:
        print(f"[{name} stdout] {out.decode(errors='replace')}", file=sys.stderr)
    if err:
        print(f"[{name} stderr] {err.decode(errors='replace')}", file=sys.stderr)


@pytest.fixture(autouse=True)
def _reset_canned_responses(stack: dict[str, str]) -> Iterator[None]:
    clear_responses(stack["mockllm_url"])
    yield
    clear_responses(stack["mockllm_url"])
