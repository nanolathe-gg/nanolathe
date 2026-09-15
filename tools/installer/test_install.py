#!/usr/bin/env python3
"""Offline Unix installer contract checks; no real network, toolchain or game."""
import hashlib
import io
import os
from pathlib import Path
import pty
import select
import subprocess
import tarfile
import tempfile
import time
import unittest

INSTALLER = Path(__file__).with_name("install.sh").resolve()
REVISION = "a" * 40


def archive(path, files):
    with tarfile.open(path, "w:gz") as out:
        for name, data in files.items():
            entry = tarfile.TarInfo(name)
            entry.mode = 0o755
            data = data.encode()
            entry.size = len(data)
            out.addfile(entry, io.BytesIO(data))
    return hashlib.sha256(path.read_bytes()).hexdigest()


class InstallerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="nanolathe installer ")
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name).resolve()
        self.home = self.path / "home"
        self.base = self.home / "Game files $literal `literal`"
        self.root = self.path / "TA data $literal `literal`"
        self.root.mkdir()
        self.bin = self.path / "bin"
        self.bin.mkdir()
        self.env = dict(os.environ, HOME=str(self.home), NANOLATHE_INSTALL_DIR=str(self.base),
                        XDG_DATA_HOME=str(self.home / "share"), FIXTURE_DIR=str(self.path),
                        PATH=str(self.bin) + os.pathsep + os.environ["PATH"])
        self.write_command("uname", '#!/bin/bash\ncase "$1" in -s) echo Linux ;; -m) echo x86_64 ;; esac\n')
        if os.uname().sysname == "Darwin":
            self.write_command("mv", '#!/bin/bash\nif [ "$1" = -fT ]; then shift; exec /bin/mv -fh "$@"; fi\nexec /bin/mv "$@"\n')
        self.write_command("curl", '''#!/bin/bash
printf '%s\\n' "$*" >> "$FIXTURE_DIR/curl-options"
while [ "$#" -gt 0 ]; do
  case "$1" in --output) target=$2; shift 2 ;; https://*) url=$1; shift ;; *) shift ;; esac
done
printf '%s\\n' "$url" >> "$FIXTURE_DIR/downloads"
case "$url" in
  */release.txt) [ "${OFFLINE:-0}" = 0 ] || exit 28; cp "$FIXTURE_DIR/release.txt" "$target" ;;
  https://nanolathe.gg/install.sh) cp "$FIXTURE_DIR/install.sh" "$target" ;;
  https://go.dev/dl/*) cp "$FIXTURE_DIR/go.tar.gz" "$target" ;;
  https://codeload.github.com/*) cp "$FIXTURE_DIR/source.tar.gz" "$target" ;;
  *) exit 12 ;;
esac
''')
        engine = '''#!/bin/bash
case "$1" in
  --help) exit "${HELP_FAIL:-0}" ;;
  --check-install) test "$2" = --root && test -d "$3"; exit $? ;;
  --list-installs) [ -f "$FIXTURE_DIR/candidates" ] && cat "$FIXTURE_DIR/candidates"; exit 0 ;;
esac
printf '%s\\n' "$@" > "$FIXTURE_DIR/launch-args"
printf '%s\\n' "$NANOLATHE_SETTINGS" > "$FIXTURE_DIR/settings-path"
printf '%s\\n' "$0" >> "$FIXTURE_DIR/launched-binaries"
printf '%s\\n' "${NANOLATHE_SKIP_UPDATE_CHECK:-unset}" > "$FIXTURE_DIR/skip-check"
exit "${RUN_FAIL:-0}"
'''
        (self.path / "engine").write_text(engine)
        go = '''#!/bin/bash
[ "$CGO_ENABLED" = 0 ] && [ "$GOTOOLCHAIN" = local ] && [ "$GOENV" = off ] || exit 20
printf built >> "$FIXTURE_DIR/builds"
[ "${BUILD_FAIL:-0}" = 0 ] || exit 21
while [ "$#" -gt 0 ]; do if [ "$1" = -o ]; then output=$2; break; fi; shift; done
cp "$FIXTURE_DIR/engine" "$output"
chmod +x "$output"
'''
        self.go_hash = archive(self.path / "go.tar.gz", {"go/bin/go": go})
        self.source_hash = archive(self.path / "source.tar.gz", {f"nanolathe-{REVISION}/go.sum": "authored fixture\n"})
        (self.path / "install.sh").write_bytes(INSTALLER.read_bytes())
        self.manifest()

    def write_command(self, name, data):
        path = self.bin / name
        path.write_text(data)
        path.chmod(0o755)

    def manifest(self, **overrides):
        values = dict(version="alpha.1", source_revision=REVISION, source_tar_sha256=self.source_hash,
                      source_zip_sha256="b" * 64, go_version="1.25.0",
                      installer_sh_sha256=hashlib.sha256((self.path / "install.sh").read_bytes()).hexdigest())
        for platform in ("darwin_arm64", "darwin_amd64", "linux_amd64", "linux_arm64", "windows_amd64"):
            values[f"go_{platform}_sha256"] = self.go_hash
        values.update(overrides)
        (self.path / "release.txt").write_text("".join(f"{k}={v}\n" for k, v in values.items()))

    def install(self, *args, success=True, **environment):
        result = subprocess.run(["/bin/bash", str(INSTALLER), *args], env=dict(self.env, **environment),
                                stdin=subprocess.DEVNULL, capture_output=True, text=True)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        return result

    def test_help_has_no_download(self):
        self.install("--help")
        self.assertFalse((self.path / "downloads").exists())

    def test_checksum_mismatch_never_executes_payload(self):
        self.manifest(go_linux_amd64_sha256="0" * 64)
        self.install("--no-run", success=False)
        self.assertFalse((self.path / "builds").exists())
        self.assertFalse((self.base / "current").exists())
        self.manifest(source_tar_sha256="0" * 64)
        self.install("--no-run", success=False)
        self.assertFalse((self.path / "builds").exists())
        self.assertFalse((self.base / "current").exists())

    def test_failed_update_preserves_release(self):
        self.install("--no-run", "--root", str(self.root))
        before = (self.base / "current").resolve()
        self.manifest(version="alpha.2")
        for environment in ({"BUILD_FAIL": "1"}, {"HELP_FAIL": "1"}):
            self.install("--no-run", success=False, **environment)
            self.assertEqual((self.base / "current").resolve(), before)
        downloads = (self.path / "downloads").read_text()
        self.assertEqual(downloads.count("https://go.dev/"), 1)

    def test_manifest_is_data_and_rejects_duplicates(self):
        self.manifest(version="$(touch injected)")
        self.install("--no-run", success=False)
        self.assertFalse((self.path / "builds").exists())
        self.manifest()
        with (self.path / "release.txt").open("a") as out:
            out.write("version=alpha.2\n")
        self.install("--no-run", success=False)
        self.assertFalse((self.path / "builds").exists())

    def test_manifest_accepts_validated_installer_hashes(self):
        self.manifest(installer_sh_sha256="c" * 64, installer_ps1_sha256="d" * 64)
        self.install("--no-run")
        before = (self.base / "current").resolve()
        self.manifest(installer_sh_sha256="invalid")
        self.install("--no-run", success=False)
        self.assertEqual((self.base / "current").resolve(), before)

    def test_spaces_and_launch_only_downloads_manifest(self):
        self.install("--no-run", "--root", str(self.root))
        before = (self.path / "downloads").read_text()
        result = subprocess.run(["/bin/bash", str(self.base / "launch.sh")], env=self.env, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.path / "launch-args").read_text().splitlines(),
                         ["--root", str(self.root), "--save-dir", str(self.base / "saves")])
        self.assertEqual((self.path / "settings-path").read_text().strip(), str(self.base / "settings.json"))
        self.assertEqual((self.path / "downloads").read_text(),
                         before + "https://nanolathe.gg/install/release.txt\n")
        desktop = (self.home / "share/applications/nanolathe.desktop").read_text()
        self.assertIn("Terminal=true", desktop)
        self.assertIn(r"\\$literal", desktop)
        self.assertIn(r"\\`literal\\`", desktop)

    def test_no_run_skips_selection_and_runtime_failure_shows_log(self):
        self.install("--no-run")
        self.assertFalse((self.base / "game-root").exists())
        (self.path / "candidates").write_text(str(self.root) + "\n")
        result = subprocess.run(["/bin/bash", str(self.base / "launch.sh")],
                                env=dict(self.env, RUN_FAIL="1"), capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Details:", result.stderr)

    @unittest.skipUnless(os.uname().sysname == "Darwin", "native macOS shortcut test")
    def test_macos_shortcut_stores_path_as_data(self):
        self.write_command("uname", '#!/bin/bash\ncase "$1" in -s) echo Darwin ;; -m) echo arm64 ;; esac\n')
        self.write_command("codesign", '#!/bin/bash\nprintf signed > "$FIXTURE_DIR/signed"\n')
        self.install("--no-run", "--root", str(self.root))
        app = self.home / "Applications/Nanolathe.app/Contents"
        self.assertEqual((app / "Resources/install-dir").read_text().strip(), str(self.base))
        self.assertTrue((self.path / "signed").exists())
        result = subprocess.run(["/bin/bash", str(app / "MacOS/Nanolathe")], env=self.env, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.path / "launch-args").exists())

    def run_terminal(self, command, prompt, answer, **environment):
        # Establish a controlling terminal while the command may have piped stdin.
        pid, terminal = pty.fork()
        if pid == 0:
            os.execve("/bin/bash", ["/bin/bash", "-c", command],
                      dict(self.env, INSTALLER_FIXTURE=str(INSTALLER), **environment))
        output = b""
        answered = False
        deadline = time.monotonic() + 20
        try:
            while time.monotonic() < deadline:
                ready, _, _ = select.select([terminal], [], [], 0.2)
                if ready:
                    try:
                        block = os.read(terminal, 65536)
                    except OSError:
                        break
                    if not block:
                        break
                    output += block
                    if prompt in output and not answered:
                        os.write(terminal, (answer + "\n").encode())
                        answered = True
            else:
                os.kill(pid, 9)
                self.fail("launcher prompt timed out: " + output.decode(errors="replace"))
        finally:
            os.close(terminal)
            _, status = os.waitpid(pid, 0)
        self.assertEqual(os.waitstatus_to_exitcode(status), 0, output)
        return answered, output

    def test_piped_installer_reads_prompt_from_terminal(self):
        answered, output = self.run_terminal('cat "$INSTALLER_FIXTURE" | /bin/bash',
                                             b"blank cancels", str(self.root))
        self.assertTrue(answered, output)
        self.assertEqual((self.base / "game-root").read_text().strip(), str(self.root))
        # Initial installation already fetched the manifest; play skips another check.
        self.assertEqual((self.path / "downloads").read_text().count("/release.txt"), 1)
        self.assertEqual((self.path / "skip-check").read_text().strip(), "unset")

    def prepare_update(self):
        self.install("--no-run", "--root", str(self.root))
        previous = (self.base / "current").resolve()
        revision = "c" * 40
        self.source_hash = archive(self.path / "source.tar.gz",
                                   {f"nanolathe-{revision}/go.sum": "authored update fixture\n"})
        self.manifest(source_revision=revision)  # Deliberately retain the version label.
        (self.path / "downloads").write_text("")
        (self.path / "curl-options").write_text("")
        return previous

    def launch_update(self, answer="n", **environment):
        return self.run_terminal('/bin/bash "$NANOLATHE_INSTALL_DIR/launch.sh" '
                                 '--desktop --root "$UPDATE_ROOT" --map "$UPDATE_MAP" < /dev/null',
                                 b"Update & play?", answer, UPDATE_ROOT=str(self.root),
                                 UPDATE_MAP='Map $literal `literal`', **environment)

    def assert_played(self, release):
        self.assertEqual((self.path / "launched-binaries").read_text().splitlines(),
                         [str(release / "nanolathe")])
        self.assertEqual((self.path / "launch-args").read_text().splitlines(),
                         ["--root", str(self.root), "--save-dir", str(self.base / "saves"),
                          "--map", 'Map $literal `literal`'])
        self.assertEqual((self.path / "skip-check").read_text().strip(), "unset")
        self.assertEqual(list(self.base.glob(".update-*")), [])

    def test_same_version_changed_revision_accepts_verified_update(self):
        previous = self.prepare_update()
        # Explicit root overrides must survive the updated launcher handoff.
        self.root = self.path / "Other TA $data `data`"
        self.root.mkdir()
        answered, output = self.launch_update("y")
        self.assertTrue(answered, output)
        selected = (self.base / "current").resolve()
        self.assertNotEqual(selected, previous)
        self.assertTrue(previous.is_dir())
        self.assert_played(selected)
        self.assertEqual((self.base / "game-root").read_text().strip(), str(self.root))
        downloads = (self.path / "downloads").read_text().splitlines()
        self.assertEqual(downloads, ["https://nanolathe.gg/install/release.txt",
                                     "https://nanolathe.gg/install.sh",
                                     "https://nanolathe.gg/install/release.txt",
                                     "https://codeload.github.com/nanolathe-gg/nanolathe/tar.gz/" + "c" * 40])
        options = (self.path / "curl-options").read_text().splitlines()
        self.assertTrue(options[0].startswith("--disable "))
        self.assertIn("--max-time 3", options[0])
        self.assertIn("--proto-redir =https", options[0])
        self.assertIn("--max-time 60", options[1])

    def test_declined_update_keeps_current_without_payload_download(self):
        previous = self.prepare_update()
        answered, output = self.launch_update("n")
        self.assertTrue(answered, output)
        self.assert_played(previous)
        self.assertEqual((self.path / "downloads").read_text().splitlines(),
                         ["https://nanolathe.gg/install/release.txt"])
        self.assertEqual((self.path / "builds").read_text(), "built")

    def test_unattended_update_keeps_current_without_prompt(self):
        previous = self.prepare_update()
        result = subprocess.run(["/bin/bash", str(self.base / "launch.sh")],
                                env=self.env, stdin=subprocess.DEVNULL, capture_output=True,
                                text=True, start_new_session=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("Update & play", result.stdout + result.stderr)
        self.assertEqual((self.path / "launched-binaries").read_text().splitlines(),
                         [str(previous / "nanolathe")])
        self.assertEqual((self.path / "downloads").read_text().splitlines(),
                         ["https://nanolathe.gg/install/release.txt"])

    def test_failed_check_never_prompts_and_plays_current(self):
        previous = self.prepare_update()
        valid = (self.path / "release.txt").read_text()
        cases = [
            (valid.replace("c" * 40, REVISION), {}),
            (valid, {"OFFLINE": "1"}),
            (valid + "version=duplicate\n", {}),
            (valid.replace("version=alpha.1", "version=$(touch injected)"), {}),
            (valid.replace("source_revision=" + "c" * 40 + "\n", ""), {}),
            (valid.replace("go_version=1.25.0", "go_version=bad"), {}),
            ("\n" + valid, {}),
            (valid.replace("version=alpha.1", "version=alpha.\x001"), {}),
            ("\n".join(line for line in valid.splitlines() if not line.startswith("installer_sh_sha256=")), {}),
        ]
        for manifest, environment in cases:
            with self.subTest(manifest=manifest, environment=environment):
                (self.path / "release.txt").write_text(manifest)
                (self.path / "downloads").write_text("")
                (self.path / "launched-binaries").unlink(missing_ok=True)
                answered, output = self.launch_update(**environment)
                self.assertFalse(answered, output)
                self.assert_played(previous)
                self.assertEqual((self.path / "downloads").read_text().splitlines(),
                                 ["https://nanolathe.gg/install/release.txt"])

    def test_update_checksum_or_build_failure_plays_previous(self):
        previous = self.prepare_update()
        for corrupt, environment in [(True, {}), (False, {"BUILD_FAIL": "1"})]:
            with self.subTest(corrupt=corrupt, environment=environment):
                (self.path / "install.sh").write_bytes(INSTALLER.read_bytes())
                self.manifest(source_revision="c" * 40)
                if corrupt:
                    (self.path / "install.sh").write_text('touch "$FIXTURE_DIR/unverified-executed"\n')
                (self.path / "launched-binaries").unlink(missing_ok=True)
                answered, output = self.launch_update("y", **environment)
                self.assertTrue(answered, output)
                self.assertEqual((self.base / "current").resolve(), previous)
                self.assert_played(previous)
                self.assertFalse((self.path / "unverified-executed").exists())
                self.assertIn(b"playing the current version", output)


if __name__ == "__main__":
    unittest.main()
