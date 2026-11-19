package agent

import (
	"testing"
	"github.com/AmrUser-48/agy-open/internal/config"
)

func TestGenerationConfigTemperatureOverrideIsOptionalAndModelAware(t *testing.T) {
	value := 0.2
	tests := []struct{name,provider,model string; temperature,want *float64}{
		{name:"unset",provider:"groq",model:"qwen/qwen3.8-27b"},
		{name:"Groq override",provider:"groq",model:"qwen/qwen3.8-27b",temperature:&value,want:&value},
		{name:"NVIDIA override",provider:"nvidia",model:"nvidia/nemotron-3.5-super-vl-120b-a12b",temperature:&value,want:&value},
		{name:"Gemini 3.1 override",provider:"gemini",model:"gemini-3.1-flash-lite",temperature:&value,want:&value},
		{name:"Gemini 3.8 keeps provider default",provider:"gemini",model:"gemini-3.8-flash-medium",temperature:&value},
		{name:"Gemini 3.5 Flash-Lite keeps provider default",provider:"gemini-api",model:"gemini-3.5-flash-lite",temperature:&value},
		{name:"Gemini 4 keeps provider default",provider:"gemini-api",model:"gemini-4.1-pro",temperature:&value},
	}
	for _,tc:=range tests { t.Run(tc.name,func(t *testing.T){
		a:=&Agent{cfg:config.Config{ModelProvider:tc.provider,Model:tc.model,Temperature:tc.temperature},effort:"default"}
		got:=a.generationConfig()["temperature"]
		if tc.want==nil { if got!=nil { t.Fatalf("temperature=%#v, want omitted",got) }; return }
		if got!=*tc.want { t.Fatalf("temperature=%#v, want %v",got,*tc.want) }
	})}
}
