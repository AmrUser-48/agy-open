from __future__ import annotations
import json
from pathlib import Path
from .history import append,load
from .providers import GeminiProvider
from .tools import WorkspaceTools
SYSTEM="""You are agy-open, a terminal-first software engineering agent. Work only inside the supplied workspace. Be concrete and concise. Request one tool at a time using exactly one JSON object on a line: {"tool":"list_files","args":{"path":"."}}. Supported tools: list_files, read_file, search, write_file, shell. When enough information is available, return a normal answer with no tool JSON. Never invent tool results. Explain risky or destructive actions before requesting them."""
class Agent:
    def __init__(self,root:Path,cfg:dict,history_path:Path): self.root=root; self.cfg=cfg; self.history_path=history_path; self.provider=GeminiProvider(cfg["model"]); self.tools=WorkspaceTools(root,cfg.get("approvalMode","ask"))
    def _tool(self,name,args):
        fn={"list_files":self.tools.list_files,"read_file":self.tools.read_file,"search":self.tools.search,"write_file":self.tools.write_file,"shell":self.tools.shell}.get(name)
        if not fn: return "unknown tool"
        try: return fn(**args).output
        except Exception as exc: return f"tool error: {exc}"
    def run(self,user_prompt:str)->str:
        messages=[]
        for item in load(self.history_path)[-20:]:
            if item.get("role") in {"user","model"}: messages.append({"role":item["role"],"parts":[{"text":item["content"]}]})
        messages.append({"role":"user","parts":[{"text":user_prompt}]}); append(self.history_path,"user",user_prompt)
        for _ in range(int(self.cfg.get("maxTurns",12))):
            response=self.provider.generate(messages,SYSTEM); candidate=None
            try: candidate=json.loads(response) if response.startswith("{") and response.endswith("}") else None
            except json.JSONDecodeError: pass
            if candidate and candidate.get("tool"):
                result=self._tool(candidate["tool"],candidate.get("args",{})); messages += [{"role":"model","parts":[{"text":response}]},{"role":"user","parts":[{"text":"TOOL_RESULT\n"+result}]}]; continue
            append(self.history_path,"model",response); return response
        return "Agent stopped after maxTurns."
