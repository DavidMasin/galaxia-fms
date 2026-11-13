"""Builds every Arduino example (examples/* and sdk/arduino/examples/*) for the ESP32 with PlatformIO.

    pip install platformio
    python examples/build_all.py [--workspace DIR] [--strict]

--workspace keeps PlatformIO's build output out of the repository. --strict adds -Wall -Wextra and fails if a
warning points into the Galaxia library or an example sketch (third-party libraries are not held to it).
"""

import argparse
import os
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LIBRARY = os.path.join(ROOT, "sdk", "arduino")
PLATFORM = "espressif32@8.0.0"
# Extra libraries for the library's own examples.
LIBRARY_EXAMPLE_DEPS = {"EthernetW5500": ["arduino-libraries/Ethernet@~2.0.2"]}
STRICT_FLAGS = "-Wall -Wextra -Wshadow -Wsign-compare -Wunused-parameter"


def pio():
    found = shutil.which("pio")
    return [found] if found else [sys.executable, "-m", "platformio"]


def projects(workspace):
    for name in sorted(os.listdir(os.path.join(ROOT, "examples"))):
        folder = os.path.join(ROOT, "examples", name)
        if os.path.isfile(os.path.join(folder, "platformio.ini")):
            yield "examples/" + name, folder
    for name in sorted(os.listdir(os.path.join(LIBRARY, "examples"))):
        sketch = os.path.join(LIBRARY, "examples", name)
        folder = os.path.join(workspace, "library-" + name)
        os.makedirs(folder, exist_ok=True)
        deps = ["symlink://" + LIBRARY.replace("\\", "/")] + LIBRARY_EXAMPLE_DEPS.get(name, [])
        with open(os.path.join(folder, "platformio.ini"), "w", newline="\n") as ini:
            ini.write("[platformio]\nsrc_dir = %s\n\n" % sketch.replace("\\", "/"))
            ini.write("[env:esp32dev]\nplatform = %s\nboard = esp32dev\nframework = arduino\n" % PLATFORM)
            ini.write("lib_deps =\n" + "".join("  %s\n" % dep for dep in deps))
        yield "sdk/arduino/examples/" + name, folder


def ours(line):
    """A compiler warning in the Galaxia library or in an example sketch."""
    normalized = line.replace("\\", "/")
    if "warning:" not in line or "/libdeps/" in normalized:
        return False
    return "/sdk/arduino/src/" in normalized or ".ino:" in normalized


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--workspace", default=os.path.join(tempfile.gettempdir(), "galaxia-build"))
    parser.add_argument("--strict", action="store_true")
    args = parser.parse_args()
    os.makedirs(args.workspace, exist_ok=True)
    results = []
    for label, folder in projects(args.workspace):
        env = dict(os.environ)
        env["PLATFORMIO_WORKSPACE_DIR"] = os.path.join(args.workspace, label.replace("/", "-"))
        if args.strict:
            env["PLATFORMIO_BUILD_FLAGS"] = STRICT_FLAGS
        print("==> " + label, flush=True)
        run = subprocess.run(pio() + ["run", "-d", folder], env=env, capture_output=True, text=True)
        output = run.stdout + run.stderr
        warnings = sorted({line.strip() for line in output.splitlines() if ours(line)})
        size = [line.strip() for line in output.splitlines() if line.startswith(("RAM:", "Flash:"))]
        ok = run.returncode == 0 and not (args.strict and warnings)
        results.append((label, ok))
        for line in size + warnings:
            print("    " + line)
        if run.returncode != 0:
            print(output[-4000:])
    print()
    for label, ok in results:
        print("%-45s %s" % (label, "ok" if ok else "FAILED"))
    return 0 if all(ok for _, ok in results) else 1


if __name__ == "__main__":
    sys.exit(main())
