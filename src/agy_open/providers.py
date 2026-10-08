from __future__ import annotations
import json,os,urllib.request
class GeminiProvider:
    def __init__(self,model:str):
        self.model=model; self.api_key=os.environ.get("GEMINI_API_KEY")
        if not self.api_key: raise RuntimeError("GEMINI_API_KEY is required")
        self.base_url=os.environ.get("GOOGLE_GEMINI_BASE_URL","https://generativelanguage.googleapis.com")
    def generate(self,contents:list[dict],system:str)->str:
        url=f"{self.base_url.rstrip('/')}/v1beta/models/{self.model}:generateContent?key={self.api_key}"
        body={"systemInstruction":{"parts":[{"text":system}]},"contents":contents,"generationConfig":{"temperature":0.2}}
        req=urllib.request.Request(url,data=json.dumps(body).encode(),headers={"Content-Type":"application/json"},method="POST")
        try:
            with urllib.request.urlopen(req,timeout=120) as r: data=json.loads(r.read().decode())
        except Exception as exc: raise RuntimeError(f"Gemini request failed: {exc}") from exc
        parts=data.get("candidates",[{}])[0].get("content",{}).get("parts",[])
        return "".join(p.get("text","") for p in parts).strip()
