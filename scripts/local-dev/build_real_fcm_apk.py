#!/usr/bin/env python3
"""Build the local Android test APK without modifying the mobile checkout."""

import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def main(ca, artifact, fingerprint, mobile):
    google_services = mobile / "secrets/google-services.json"
    if not google_services.is_file():
        raise RuntimeError("Place the Firebase test Android config in hello-fcm-smoke/secrets/google-services.json first.")
    if not ca.is_file():
        raise RuntimeError("The LAN pairing CA has not been initialized yet.")
    env = os.environ.copy()
    for key in list(env):
        if key.startswith("EXPO_PUBLIC_HELLO_"):
            env.pop(key)
    node = sorted((Path.home() / ".nvm/versions/node").glob("v26.*/bin/node"),
                  key=lambda path: tuple(map(int, path.parts[-3][1:].split("."))))
    if node:
        env["PATH"] = str(node[-1].parent) + os.pathsep + env["PATH"]
    if not env.get("ANDROID_HOME") and (Path.home() / "Library/Android/sdk").is_dir():
        env["ANDROID_HOME"] = str(Path.home() / "Library/Android/sdk")
    if not env.get("JAVA_HOME") and sys.platform == "darwin":
        env["JAVA_HOME"] = subprocess.check_output(["/usr/libexec/java_home", "-v", "17"], text=True).strip()
    names = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=mobile).split(b"\0")
    with tempfile.TemporaryDirectory(prefix="hello-lan-apk-") as directory:
        build = Path(directory)
        for name in names:
            relative = Path(os.fsdecode(name))
            if not name or relative.parts[0] in ("android", "ios", "node_modules", "secrets", ".expo"):
                continue
            source = mobile / relative
            if source.is_file() and not source.is_symlink():
                target = build / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, target)
        (build / "secrets").mkdir(mode=0o700)
        shutil.copy2(google_services, build / "secrets/google-services.json")
        copied_ca = build / "hello-lan-ca.crt"
        shutil.copy2(ca, copied_ca)
        env["HELLO_LAN_CA_FILE"] = str(copied_ca)
        subprocess.run(["npm", "ci", "--no-audit", "--no-fund"], cwd=build, env=env, check=True, timeout=300)
        subprocess.run(["npm", "run", "build:lan-apk"], cwd=build, env=env, check=True, timeout=1200)
        temporary = artifact.with_suffix(".new")
        shutil.copyfile(build / "android/app/build/outputs/apk/debug/app-debug.apk", temporary)
        temporary.chmod(0o600)
        temporary.replace(artifact)
        temporary = fingerprint.with_suffix(".new")
        temporary.write_text(hashlib.sha256(copied_ca.read_bytes()).hexdigest() + "\n")
        temporary.replace(fingerprint)


if __name__ == "__main__":
    try:
        main(*map(Path, sys.argv[1:]))
    except (RuntimeError, OSError, subprocess.SubprocessError) as error:
        print(f"Android APK build failed: {error}", file=sys.stderr)
        sys.exit(1)
