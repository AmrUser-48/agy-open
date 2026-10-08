from __future__ import annotations
import json
from datetime import datetime,timezone
from pathlib import Path
def history_dir()->Path: return Path.home()/".agy"/"history"
def new_session()->Path:
    history_dir().mkdir(parents=True,exist_ok=True)
    return history_dir()/(datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")+".jsonl")
def append(path:Path,role:str,content:str)->None:
    with path.open("a",encoding="utf-8") as f: f.write(json.dumps({"role":role,"content":content},ensure_ascii=False)+"\n")
def load(path:Path)->list[dict]:
    if not path.exists(): return []
    out=[]
    for line in path.read_text(encoding="utf-8").splitlines():
        try: item=json.loads(line)
        except json.JSONDecodeError: continue
        if isinstance(item,dict): out.append(item)
    return out
