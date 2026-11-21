package agent

import (
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestGoogleSearchGroundingAddsNativeToolOnlyWhenEnabledForGemini3(t *testing.T) {
	custom := []map[string]any{{"functionDeclarations": []gemini.FunctionDeclaration{{Name: "read_file"}}}}
	tests := []struct {
		name, provider, model string
		enabled bool
		wantSearch bool
		wantError bool
	}{
		{name:"disabled by default", provider:"gemini", model:"gemini-3.8-flash-medium"},
		{name:"Antigravity OAuth supported model", provider:"gemini", model:"gemini-3.8-flash-medium", enabled:true, wantSearch:true},
		{name:"Antigravity OAuth 3.1 Flash-Lite", provider:"gemini", model:"gemini-3.1-flash-lite", enabled:true, wantSearch:true},
		{name:"Antigravity rejects public API-only 3.5 alias", provider:"gemini", model:"gemini-3.5-flash-lite", enabled:true, wantError:true},
		{name:"Antigravity rejects 2.5 fallback", provider:"gemini", model:"gemini-2.5-flash-thinking", enabled:true, wantError:true},
		{name:"Gemini Developer API 3.8", provider:"gemini-api", model:"gemini-3.8-flash", enabled:true, wantSearch:true},
		{name:"Gemini Developer API 3.5 Flash-Lite", provider:"gemini-api", model:"gemini-3.5-flash-lite", enabled:true, wantSearch:true},
		{name:"non-Gemini provider", provider:"openai-api", model:"gpt-4.1", enabled:true},
		{name:"reject unknown Gemini API model", provider:"gemini-api", model:"unknown-gemini-model", enabled:true, wantError:true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{cfg: config.Config{ModelProvider:tc.provider, Model:tc.model, GoogleSearchGrounding:tc.enabled}}
			err := a.validateGoogleSearchGrounding()
			if (err != nil) != tc.wantError { t.Fatalf("validation error = %v, wantError=%v", err, tc.wantError) }
			got := a.requestTools(custom)
			found := false
			for _, group := range got {
				if _, ok := group["googleSearch"]; ok { found = true }
			}
			if found != tc.wantSearch { t.Fatalf("googleSearch present=%v, want %v; tools=%#v", found, tc.wantSearch, got) }
			if len(custom) != 1 { t.Fatalf("requestTools mutated original tool groups: %#v", custom) }
			if len(got) != 1+btoi(tc.wantSearch) { t.Fatalf("tool group count=%d, want %d", len(got), 1+btoi(tc.wantSearch)) }
		})
	}
}

func btoi(value bool) int {
	if value { return 1 }
	return 0
}
