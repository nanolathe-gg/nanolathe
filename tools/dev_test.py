#!/usr/bin/env python3
"""Exercise the native dev launcher against a tiny Go command, without assets."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SOURCE = """package main
import ("encoding/json"; "os"; "strconv")
const version = 1
func main() {
    cwd, _ := os.Getwd()
    json.NewEncoder(os.Stdout).Encode(struct {
        Args []string
        Cwd string
        Version int
    }{os.Args[1:], cwd, version})
    status, _ := strconv.Atoi(os.Getenv("DEV_TEST_EXIT"))
    os.Exit(status)
}
"""


class DevLauncherTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="nanolathe-dev-")
        self.addCleanup(self.temp.cleanup)
        self.cwd = Path(self.temp.name)
        self.repo = self.cwd / "checkout with spaces"
        (self.repo / "tools").mkdir(parents=True)
        (self.repo / "cmd" / "nanolathe").mkdir(parents=True)
        (self.repo / "go.mod").write_text("module dev-launcher-test\n", encoding="utf-8")
        self.source = self.repo / "cmd" / "nanolathe" / "main.go"
        self.source.write_text(SOURCE, encoding="utf-8")
        name = "dev.cmd" if os.name == "nt" else "dev"
        self.script = self.repo / "tools" / name
        shutil.copy2(Path(__file__).resolve().parent / name, self.script)

    def launch(self, *args, status=0):
        env = dict(os.environ, DEV_TEST_EXIT=str(status), GOWORK="off")
        command = [str(self.script), *args]
        if os.name == "nt":
            command = " ".join(f'"{arg}"' for arg in command)
        return subprocess.run(command, cwd=self.cwd, env=env, shell=os.name == "nt",
                              capture_output=True, text=True, timeout=60)

    def test_arguments_working_directory_and_exit_status(self):
        args = ["--root", "a folder with spaces", "", "bang!", "a&b", "a=b"]
        run = self.launch(*args, status=37)
        self.assertEqual(run.returncode, 37, run.stderr)
        report = json.loads(run.stdout)
        self.assertEqual(report["Args"], args)
        self.assertEqual(Path(report["Cwd"]).resolve(), self.repo.resolve())

    def test_rebuilds_and_never_runs_stale_binary_after_failure(self):
        run = self.launch()
        self.assertEqual(run.returncode, 0, run.stderr)
        self.assertEqual(json.loads(run.stdout)["Version"], 1)
        self.source.write_text(SOURCE.replace("version = 1", "version = 2"), encoding="utf-8")
        run = self.launch()
        self.assertEqual(run.returncode, 0, run.stderr)
        self.assertEqual(json.loads(run.stdout)["Version"], 2)
        self.source.write_text("not valid Go\n", encoding="utf-8")
        run = self.launch()
        self.assertNotEqual(run.returncode, 0)
        self.assertEqual(run.stdout, "", "failed build launched the old executable")


if __name__ == "__main__":
    unittest.main()
