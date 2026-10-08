from __future__ import annotations
import os,re,subprocess
from dataclasses import dataclass
from pathlib import Path
@dataclass
class ToolResult: output:str; ok:bool=True
class WorkspaceTools:
    def __init__(self,root:Path,approval_mode:str="ask"): self.root=root.resolve(); self.approval_mode=approval_mode
    def _safe(self,path:str)->Path:
        p=(self.root/path).resolve()
        if p!=self.root and self.root not in p.parents: raise ValueError("path escapes workspace")
        return p
    def list_files(self,path:str=".")->ToolResult:
        p=self._safe(path); items=[]
        for x in sorted(p.iterdir()):
            if x.name in {".git",".agy","__pycache__",".venv","venv"}: continue
            items.append(("d " if x.is_dir() else "f ")+x.relative_to(self.root).as_posix())
        return ToolResult("\n".join(items) or "(empty)")
    def read_file(self,path:str)->ToolResult:
        p=self._safe(path)
        if not p.exists() or not p.is_file(): return ToolResult(f"file not found: {path}",False)
        return ToolResult(p.read_text(encoding="utf-8",errors="replace"))
    def search(self,pattern:str,path:str=".")->ToolResult:
        base=self._safe(path); rx=re.compile(pattern,re.IGNORECASE); hits=[]
        for p in base.rglob("*"):
            if not p.is_file() or p.name.startswith(".") or ".git" in p.parts: continue
            try:
                for i,line in enumerate(p.read_text(encoding="utf-8",errors="ignore").splitlines(),1):
                    if rx.search(line): hits.append(f"{p.relative_to(self.root)}:{i}:{line[:240]}")
            except OSError: pass
        return ToolResult("\n".join(hits[:300]) or "no matches")
    def write_file(self,path:str,content:str)->ToolResult:
        if self.approval_mode=="ask": return ToolResult("APPROVAL_REQUIRED: write_file",False)
        p=self._safe(path); p.parent.mkdir(parents=True,exist_ok=True); p.write_text(content,encoding="utf-8"); return ToolResult(f"wrote {p.relative_to(self.root)}")
    def shell(self,command:str)->ToolResult:
        if self.approval_mode=="ask": return ToolResult("APPROVAL_REQUIRED: shell",False)
        proc=subprocess.run(command,cwd=self.root,shell=True,text=True,capture_output=True,env=os.environ.copy(),timeout=60)
        out=(proc.stdout+proc.stderr).strip(); return ToolResult(out[-12000:] or f"exit code {proc.returncode}",proc.returncode==0)
