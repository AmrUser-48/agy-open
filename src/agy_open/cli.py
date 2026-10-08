from __future__ import annotations
import argparse,os,sys
from pathlib import Path
from .agent import Agent
from .config import load_config
from .history import new_session
HELP="""Commands:\n  /help /clear /config /model <name> /agents /add-dir <path> /approve /ask /quit\n\nHeadless: agy -p "your task"\n"""
def build_parser():
    p=argparse.ArgumentParser(prog="agy",description="agy-open terminal AI coding agent"); p.add_argument("-p","--prompt","--print",dest="prompt"); p.add_argument("--model"); p.add_argument("--dangerously-skip-permissions",action="store_true"); p.add_argument("--workspace",default=os.getcwd()); return p
def main(argv=None):
    args=build_parser().parse_args(argv); cfg=load_config()
    if args.model: cfg["model"]=args.model
    if args.dangerously_skip_permissions: cfg["approvalMode"]="auto"
    root=Path(args.workspace).resolve(); session=new_session()
    if args.prompt:
        try: print(Agent(root,cfg,session).run(args.prompt)); return 0
        except Exception as exc: print(f"agy: {exc}",file=sys.stderr); return 1
    print("agy-open 0.1.0 — terminal agent"); print(f"workspace: {root}"); print("Type /help for commands.\n"); agent=None
    while True:
        try: raw=input("you › ").strip()
        except (EOFError,KeyboardInterrupt): print(); return 0
        if not raw: continue
        if raw in {"/quit","/exit"}: return 0
        if raw=="/help": print(HELP); continue
        if raw=="/clear": session=new_session(); agent=None; print("started a new session"); continue
        if raw=="/config": print(load_config()); continue
        if raw.startswith("/model "): cfg["model"]=raw.split(" ",1)[1].strip(); agent=None; print(f"model={cfg['model']}"); continue
        if raw=="/approve": cfg["approvalMode"]="auto"; agent=None; print("approvalMode=auto"); continue
        if raw=="/ask": cfg["approvalMode"]="ask"; agent=None; print("approvalMode=ask"); continue
        if raw=="/agents": print("subagents: 0 active (MVP)"); continue
        if raw.startswith("/add-dir "): print(f"additional directory: {raw.split(' ',1)[1]}"); continue
        try: agent=agent or Agent(root,cfg,session); print(agent.run(raw))
        except Exception as exc: print(f"error: {exc}",file=sys.stderr)
if __name__=="__main__": raise SystemExit(main())
