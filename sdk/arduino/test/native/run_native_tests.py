"""Builds and runs test_native.cpp on this computer.

    python sdk/arduino/test/native/run_native_tests.py --arduinojson <folder that contains ArduinoJson.h>

The compiler is $CXX, else c++ / g++ / clang++ from the PATH, else "python -m ziglang c++" (pip install ziglang).
ArduinoJson is the copy PlatformIO downloads for any example, for instance
examples/beam-break-counter/.pio/libdeps/esp32dev/ArduinoJson/src.
"""

import argparse
import os
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.normpath(os.path.join(HERE, "..", "..", "src"))
CASES = os.path.normpath(os.path.join(HERE, "..", "..", "..", "testdata", "led_cases.json"))


def compiler():
    if os.environ.get("CXX"):
        return os.environ["CXX"].split()
    for name in ("c++", "g++", "clang++"):
        if shutil.which(name):
            return [name]
    return [sys.executable, "-m", "ziglang", "c++"]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--arduinojson", required=True, help="folder that contains ArduinoJson.h")
    args = parser.parse_args()
    out_dir = tempfile.mkdtemp(prefix="galaxia-native-")
    binary = os.path.join(out_dir, "test_native.exe" if os.name == "nt" else "test_native")
    command = compiler() + [
        "-std=c++17", "-O1", "-Wall", "-Wextra", "-Wshadow", "-Wsign-compare",
        "-I", SRC, "-I", args.arduinojson,
        os.path.join(HERE, "test_native.cpp"),
        os.path.join(SRC, "GalaxiaLed.cpp"),
        os.path.join(SRC, "GalaxiaJson.cpp"),
        "-o", binary,
    ]
    print(" ".join(command))
    subprocess.run(command, check=True)
    result = subprocess.run([binary, CASES])
    shutil.rmtree(out_dir, ignore_errors=True)
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
