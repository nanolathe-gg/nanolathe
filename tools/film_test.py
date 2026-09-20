"""Run with python3 -m unittest discover -s tools -p film_test.py."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


@unittest.skipUnless(shutil.which("ffmpeg"), "ffmpeg required for encoder integration")
class FilmPipelineTests(unittest.TestCase):
    def run_capture(self, failure):
        with tempfile.TemporaryDirectory() as folder:
            directory = Path(folder)
            script = directory / "shot.json"
            script.write_text(json.dumps(dict(width=16, height=16, fps=30, shots=[dict(ticks=1)])))
            target = directory / "output.mp4"
            target.write_bytes(b"previous good output")
            # A build stand-in creates a producer which can fail AFTER sending a
            # valid frame. ffmpeg succeeds in this case; the wrapper must not.
            producer = (f"#!{sys.executable}\nimport sys\n"
                        "sys.stdout.buffer.write(bytes([64,128,192,255])*16*16)\n"
                        f"sys.exit({9 if failure else 0})\n")
            builder = directory / "go"
            builder.write_text(f"#!{sys.executable}\nimport sys,pathlib\n"
                               f"p=pathlib.Path(sys.argv[sys.argv.index('-o')+1]);p.write_text({producer!r});p.chmod(0o755)\n")
            builder.chmod(0o755)
            result = subprocess.run([sys.executable, str(Path(__file__).with_name("film")), str(script), str(target)],
                                    env=dict(os.environ, PATH=str(directory)+os.pathsep+os.environ["PATH"]),
                                    capture_output=True, text=True)
            if failure:
                self.assertNotEqual(result.returncode, 0, result.stderr)
                self.assertEqual(target.read_bytes(), b"previous good output")
                self.assertIn("capture exited 9", result.stderr)
            else:
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(b"ftyp", target.read_bytes()[:32])
            self.assertEqual(list(directory.glob(".nanolathe-film-*")), [])

    def test_capture_failure_does_not_publish_an_encoder_success(self):
        self.run_capture(True)

    def test_success_replaces_output_and_cleans_staging(self):
        self.run_capture(False)


if __name__ == "__main__":
    unittest.main()
