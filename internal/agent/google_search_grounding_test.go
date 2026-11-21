package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestGoogleSearchGroundingProviderEligibilityAndToolSeparation(t *testing.T) {
	custom := []map[string]any{{"functionDeclarations": []gemini.FunctionDeclaration{{Name: "read_file"}}}}
	tests := []struct {
		name, provider, model string
		enabled bool
		wantSearch bool
		wantError bool
	}{
		{name:"disabled by default", provider:"gemini", model:"gemini-3.8-flash-medium"},
		{name:"Antigravity Search is not advertised for 3.8", provider:"gemini", model:"gemini-3.8-flash-medium", enabled:true, wantError:true},
		{name:"Antigravity 3.1 Flash-Lite uses separate Search preflight", provider:"gemini", model:"gemini-3.1-flash-lite", enabled:true},
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


type groundingPrefetchModelStub struct {
	requests []gemini.Request
	response gemini.Content
}

func (m *groundingPrefetchModelStub) ModelName() string { return "grounding-prefetch-test" }
func (m *groundingPrefetchModelStub) Generate(_ context.Context, request gemini.Request) (gemini.Content, error) {
	m.requests = append(m.requests, request)
	return m.response, nil
}
func (m *groundingPrefetchModelStub) GenerateStream(ctx context.Context, request gemini.Request, _ func(string)) (gemini.Content, error) {
	return m.Generate(ctx, request)
}
func (m *groundingPrefetchModelStub) ListModelOptions(context.Context) ([]gemini.ModelOption, error) {
	return nil, nil
}
func (m *groundingPrefetchModelStub) ListModels(context.Context) ([]string, error) {
	return nil, nil
}

func TestAntigravityGoogleSearchPreflightUsesSearchOnlyRequest(t *testing.T) {
	model := &groundingPrefetchModelStub{
		response: gemini.Content{
			Parts: []gemini.Part{{Text: "AGY is an open-source agent project."}},
			Grounding: &gemini.GroundingMetadata{
				WebSearchQueries: []string{"AGY agent"},
				GroundingChunks: []gemini.GroundingChunk{{
					Web: &gemini.GroundingWeb{URI: "https://example.com/agy", Title: "AGY"},
				}},
			},
		},
	}
	a := &Agent{
		cfg: config.Config{
			ModelProvider: "gemini",
			Model: "gemini-3.1-flash-lite",
			GoogleSearchGrounding: true,
		},
		model: model,
		runGeneration: 7,
		messages: []gemini.Content{{
			Role: "user",
			Parts: []gemini.Part{{Text: "Search for AGY."}},
		}},
	}

	metadata, err := a.prefetchAntigravityGoogleSearch(context.Background(), 7, "You are AGY.")
	if err != nil {
		t.Fatal(err)
	}
	if metadata == nil || len(metadata.GroundingChunks) != 1 {
		t.Fatalf("grounding metadata = %#v", metadata)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model received %d requests, want one Search preflight", len(model.requests))
	}
	request := model.requests[0]
	if len(request.Tools) != 1 {
		t.Fatalf("Search preflight tools = %#v; want exactly one built-in tool group", request.Tools)
	}
	if _, ok := request.Tools[0]["googleSearch"]; !ok {
		t.Fatalf("Search preflight omitted googleSearch: %#v", request.Tools)
	}
	if _, ok := request.Tools[0]["functionDeclarations"]; ok {
		t.Fatalf("Search preflight mixed function declarations with built-in Search: %#v", request.Tools)
	}
	if len(a.messages) != 2 {
		t.Fatalf("active context contains %d messages, want original prompt plus Search context", len(a.messages))
	}
	searchContext := a.messages[1].Parts[0].Text
	if !strings.Contains(searchContext, "https://example.com/agy") ||
		!strings.Contains(searchContext, "AGY is an open-source agent project") {
		t.Fatalf("Search context omitted grounded answer or source URL: %s", searchContext)
	}
}

func btoi(value bool) int {
	if value { return 1 }
	return 0
}
