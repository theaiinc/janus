import os
import platform
import stat
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path


VERSION = "0.1.1"
REPOSITORY = "theaiinc/janus"


def target_for(system=None, machine=None):
    system = (system or platform.system()).lower()
    machine = (machine or platform.machine()).lower()
    systems = {"darwin": "darwin", "linux": "linux", "windows": "windows"}
    machines = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}
    target_system = systems.get(system)
    target_machine = machines.get(machine)
    if not target_system or not target_machine or (target_system == "windows" and target_machine != "amd64"):
        raise RuntimeError(f"unsupported Janus platform: {system}/{machine}")
    return f"{target_system}-{target_machine}"


def asset_name(target):
    return f"janus-{target}{'.exe' if target.startswith('windows-') else ''}"


def binary_path(target):
    cache = Path(os.environ.get("JANUS_CACHE_DIR", Path.home() / ".cache" / "janus"))
    return cache / VERSION / asset_name(target)


def ensure_binary(target):
    destination = binary_path(target)
    if not destination.exists():
        destination.parent.mkdir(parents=True, exist_ok=True)
        url = f"https://github.com/{REPOSITORY}/releases/download/v{VERSION}/{asset_name(target)}"
        temporary = destination.with_suffix(destination.suffix + ".tmp")
        urllib.request.urlretrieve(url, temporary)
        temporary.replace(destination)
        destination.chmod(destination.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return destination


def main():
    try:
        binary = ensure_binary(target_for())
    except (OSError, RuntimeError, urllib.error.URLError) as error:
        print(f"janus: {error}", file=sys.stderr)
        return 1
    result = subprocess.run([str(binary), *sys.argv[1:]], check=False)
    return result.returncode


if __name__ == "__main__":
    raise SystemExit(main())
