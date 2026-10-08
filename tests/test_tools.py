from pathlib import Path
from agy_open.tools import WorkspaceTools
def test_path_escape_is_blocked(tmp_path:Path):
    tools=WorkspaceTools(tmp_path,"auto")
    try: tools.read_file("../secret.txt")
    except ValueError: pass
    else: raise AssertionError("path escape was not blocked")
def test_write_and_read(tmp_path:Path):
    tools=WorkspaceTools(tmp_path,"auto"); tools.write_file("hello.txt","world"); assert tools.read_file("hello.txt").output=="world"
