from __future__ import annotations
import json, os
from pathlib import Path
DEFAULTS={"modelProvider":"gemini","model":"gemini-3.8-flash","maxTurns":12,"approvalMode":"ask","theme":"default"}
def config_path()->Path:
    return Path(os.environ.get("XDG_CONFIG_HOME",Path.home()/".config"))/"agy"/"settings.json"
def load_config()->dict:
    path=config_path()
    if not path.exists(): return dict(DEFAULTS)
    try: data=json.loads(path.read_text())
    except (OSError,json.JSONDecodeError): return dict(DEFAULTS)
    cfg=dict(DEFAULTS); cfg.update(data if isinstance(data,dict) else {}); return cfg
def save_config(cfg:dict)->None:
    path=config_path(); path.parent.mkdir(parents=True,exist_ok=True); path.write_text(json.dumps(cfg,indent=2)+"\n")
