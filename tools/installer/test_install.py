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
while [ "$#" -gt 0 ]; do
  case "$1" in --output) target=$2; shift 2 ;; https://*) url=$1; shift ;; *) shift ;; esac
done
printf '%s\\n' "$url" >> "$FIXTURE_DIR/downloads"
case "$url" in
  */release.txt) cp "$FIXTURE_DIR/release.txt" "$target" ;;
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
        self.manifest()

    def write_command(self, name, data):
        path = self.bin / name
        path.write_text(data)
        path.chmod(0o755)

    def manifest(self, **overrides):
        values = dict(version="alpha.1", source_revision=REVISION, source_tar_sha256=self.source_hash,
                      source_zip_sha256="b" * 64, go_version="1.25.0")
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

    def test_spaces_and_launch_without_downloads(self):
        self.install("--no-run", "--root", str(self.root))
        before = (self.path / "downloads").read_text()
        result = subprocess.run(["/bin/bash", str(self.base / "launch.sh")], env=self.env, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.path / "launch-args").read_text().splitlines(),
                         ["--root", str(self.root), "--save-dir", str(self.base / "saves")])
        self.assertEqual((self.path / "settings-path").read_text().strip(), str(self.base / "settings.json"))
        self.assertEqual((self.path / "downloads").read_text(), before)
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

    def test_piped_installer_reads_prompt_from_terminal(self):
        # Fork establishes a controlling terminal, but bash itself reads a pipe.
        # An accidental read from installer stdin would get EOF rather than this answer.
        pid, terminal = pty.fork()
        if pid == 0:
            command = 'cat "$INSTALLER_FIXTURE" | /bin/bash'
            os.execve("/bin/bash", ["/bin/bash", "-c", command],
                      dict(self.env, INSTALLER_FIXTURE=str(INSTALLER)))
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
                    if b"blank cancels" in output and not answered:
                        os.write(terminal, (str(self.root) + "\n").encode())
                        answered = True
            else:
                os.kill(pid, 9)
                self.fail("installer prompt timed out: " + output.decode(errors="replace"))
        finally:
            os.close(terminal)
            _, status = os.waitpid(pid, 0)
        self.assertTrue(answered, output)
        self.assertEqual(os.waitstatus_to_exitcode(status), 0, output)
        self.assertEqual((self.base / "game-root").read_text().strip(), str(self.root))


if __name__ == "__main__":
    unittest.main()
