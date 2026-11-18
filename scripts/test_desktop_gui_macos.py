"""Check Homebrew interpreter selection and intact argv without requiring macOS."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


@unittest.skipIf(sys.platform=='win32', 'POSIX launcher fixture')
class MacLauncherTests(unittest.TestCase):
    def test_homebrew_python_and_paths_with_spaces_preserve_arguments(self):
        with tempfile.TemporaryDirectory(prefix='entropy launcher ') as directory:
            root=Path(directory); commands=root/'commands'; commands.mkdir()
            prefix=root/'Homebrew prefix'; (prefix/'bin').mkdir(parents=True)
            app=root/'GUI source'; app.mkdir()
            launcher=app/'entropy-gui-macos'
            shutil.copyfile(Path(__file__).resolve().parents[1]/'tools/desktop-gui/entropy-gui-macos',launcher)
            log=root/'arguments.json'
            (commands/'uname').write_text("#!/bin/sh\nprintf '%s\\n' Darwin\n")
            (commands/'brew').write_text('#!/bin/sh\nprintf "%s\\n" "$TEST_BREW_PREFIX"\n')
            candidate=prefix/'bin/python3'
            candidate.write_text('#!'+sys.executable+'\nimport json,sys\nfrom pathlib import Path\n'
                                 'if sys.argv[1]=="-c": raise SystemExit(0)\n'
                                 'Path('+repr(str(log))+').write_text(json.dumps(sys.argv[1:]))\n')
            for file in [commands/'uname', commands/'brew',candidate]:file.chmod(0o700)
            arguments=['--view','server','--server-binary','/a path/server','--','-http=127.0.0.1:8080']
            result=subprocess.run(['/bin/sh',str(launcher),*arguments],env=dict(os.environ,PATH=str(commands)+os.pathsep+os.defpath,
                                   TEST_BREW_PREFIX=str(prefix)),capture_output=True,timeout=5)
            self.assertEqual(result.returncode,0,result.stderr)
            self.assertEqual(json.loads(log.read_text()),[str((app/'entropy-gui').resolve()),*arguments])

    def test_non_macos_failure_is_actionable(self):
        if sys.platform=='darwin':self.skipTest('checks unsupported host')
        launcher=Path(__file__).resolve().parents[1]/'tools/desktop-gui/entropy-gui-macos'
        result=subprocess.run(['/bin/sh',str(launcher)],capture_output=True,timeout=5)
        self.assertEqual(result.returncode,2)
        self.assertIn(b'requires macOS',result.stderr)


if __name__=='__main__':unittest.main()
