#!/usr/bin/env python3
"""Build the local Android test APK without modifying the mobile checkout."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import zipfile


def apk_cache_key(ca):
    return "standalone-v2:" + hashlib.sha256(ca.read_bytes()).hexdigest()


def configure_standalone_android(project):
    package = project / "package.json"
    manifest = json.loads(package.read_text())
    android = manifest.setdefault("expo", {}).setdefault("autolinking", {}).setdefault("android", {})
    android["exclude"] = sorted(set(android.get("exclude", [])) | {
        "expo-dev-client", "expo-dev-launcher", "expo-dev-menu", "expo-dev-menu-interface",
    })
    package.write_text(json.dumps(manifest, indent=2) + "\n")


def disable_metro(project):
    applications = list((project / "android/app/src/main").rglob("MainApplication.kt"))
    if len(applications) != 1:
        raise RuntimeError("Could not locate the generated Android MainApplication.kt.")
    application = applications[0]
    source = application.read_text()
    host = "ExpoReactHostFactory.getDefaultReactHost(\n"
    if source.count(host) != 1:
        raise RuntimeError("Could not locate the Expo React host in the Android application.")
    application.write_text(source.replace(host, host + "      useDevSupport = false,\n"))


def check_standalone_apk(path):
    with zipfile.ZipFile(path) as apk:
        bundle = "assets/index.android.bundle"
        if bundle not in apk.namelist() or apk.getinfo(bundle).file_size == 0:
            raise RuntimeError("The LAN APK is missing its embedded JavaScript bundle.")
        dex = [name for name in apk.namelist() if name.startswith("classes") and name.endswith(".dex")]
        if not dex or any(b"Lexpo/modules/devlauncher/DevLauncherController;" in apk.read(name) for name in dex):
            raise RuntimeError("The LAN APK must contain Android app code without the Expo development launcher.")


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
        env.update(HELLO_LAN_CA_FILE=str(copied_ca), HELLO_STANDALONE_LAN="1",
                   EXPO_PUBLIC_HELLO_STANDALONE="1", HELLO_STAGING_BUILD="0")
        subprocess.run(["npm", "ci", "--no-audit", "--no-fund"], cwd=build, env=env, check=True, timeout=300)
        configure_standalone_android(build)
        subprocess.run(["npx", "--no-install", "expo", "prebuild", "--platform", "android", "--no-install"],
                       cwd=build, env=env, check=True, timeout=300)
        disable_metro(build)
        subprocess.run(["./android/gradlew", "-p", "android", ":app:assembleDebug"],
                       cwd=build, env=env, check=True, timeout=1200)
        built_apk = build / "android/app/build/outputs/apk/debug/app-debug.apk"
        check_standalone_apk(built_apk)
        temporary = artifact.with_suffix(".new")
        shutil.copyfile(built_apk, temporary)
        temporary.chmod(0o600)
        temporary.replace(artifact)
        temporary = fingerprint.with_suffix(".new")
        temporary.write_text(apk_cache_key(copied_ca) + "\n")
        temporary.replace(fingerprint)


if __name__ == "__main__":
    try:
        main(*map(Path, sys.argv[1:]))
    except (RuntimeError, OSError, subprocess.SubprocessError, ValueError, zipfile.BadZipFile) as error:
        print(f"Android APK build failed: {error}", file=sys.stderr)
        sys.exit(1)
