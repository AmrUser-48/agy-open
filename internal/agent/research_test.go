package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/roles"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

func TestResearchToolRunsDedicatedAntigravityWorkerForOpenAIParent(t *testing.T) {
	root := t.TempDir()
	parentRole, ok := roles.Find(roles.Defaults(), "default")
	if !ok {
		t.Fatal("default role is missing")
	}
	parent := &Agent{
		cfg: config.Config{
			ModelProvider: "openai-api",
			Model:         "gpt-4.1",
			MaxWorkers:    config.DefaultMaxWorkers,
			ApprovalMode:  tools.ModeReadOnly,
		},
		tools:          tools.New(root, tools.ModeReadOnly),
		roleDefinitions: roles.Defaults(),
		activeRole:      parentRole,
	}

	model := &groundingPrefetchModelStub{
		response: gemini.Content{
			Parts: []gemini.Part{{Text: "The official documentation confirms the reported behavior."}},
			Grounding: &gemini.GroundingMetadata{
				WebSearchQueries: []string{"official documentation reported behavior"},
				GroundingChunks: []gemini.GroundingChunk{{
					Web: &gemini.GroundingWeb{URI: "https://docs.example.com/guide", Title: "Official guide"},
				}},
			},
		},
	}
	var workerConfig config.Config
	var workerAgent *Agent
	parent.teamWorkerFactory = func(workerRoot string, cfg config.Config) (*Agent, error) {
		workerConfig = cfg
		workerHistory, err := session.New("none")
		if err != nil {
			return nil, err
		}
		researcher, found := roles.Find(roles.Defaults(), "researcher")
		if !found {
			t.Fatal("researcher role is missing")
		}
		workerAgent = &Agent{
			cfg:            cfg,
			model:          model,
			tools:          tools.New(workerRoot, cfg.ApprovalMode),
			history:        workerHistory,
			effort:         cfg.Effort,
			roleDefinitions: roles.Defaults(),
			activeRole:      researcher,
			toolDefs:        declarations(),
		}
		return workerAgent, nil
	}

	result := parent.callTool(context.Background(), "research", map[string]any{
		"query": "Verify the behavior against official documentation and provide source links.",
	})
	if !result.OK {
		t.Fatalf("research tool failed: %s", result.Output)
	}
	if !strings.Contains(result.Output, "official documentation confirms") ||
		!strings.Contains(result.Output, "https://docs.example.com/guide") {
		t.Fatalf("research result omitted the grounded report or citation: %s", result.Output)
	}
	if workerConfig.ModelProvider != "gemini" || workerConfig.Model != "gemini-3.1-flash-lite" {
		t.Fatalf("research worker backend = %q/%q", workerConfig.ModelProvider, workerConfig.Model)
	}
	if got := workerConfig.ModelSearchMode("gemini", "gemini-3.1-flash-lite"); got != config.SearchModeGoogle {
		t.Fatalf("research worker Search mode = %q, want google", got)
	}
	if workerConfig.MCPEnabled || len(workerConfig.MCPServers) != 0 || workerConfig.AllowNonWorkspaceAccess {
		t.Fatalf("research worker inherited unsafe integrations or workspace access: %#v", workerConfig)
	}
	if workerAgent == nil || workerAgent.tools.ApprovalMode != tools.ModeReadOnly ||
		!workerAgent.tools.TeamWorkerMode || !workerAgent.tools.EnforceWorkspaceBoundary ||
		!workerAgent.forcedReadOnly || !workerAgent.blockFileMutations {
		t.Fatalf("research worker was not constrained to read-only workspace access: %#v", workerAgent)
	}
	if len(model.requests) != 2 {
		t.Fatalf("researcher sent %d model requests, want Search preflight and report generation", len(model.requests))
	}
	if len(model.requests[0].Tools) != 1 {
		t.Fatalf("first request was not an isolated native Search preflight: %#v", model.requests[0].Tools)
	}
	if _, ok := model.requests[0].Tools[0]["googleSearch"]; !ok {
		t.Fatalf("research preflight omitted native Google Search: %#v", model.requests[0].Tools)
	}
	if _, ok := model.requests[0].Tools[0]["functionDeclarations"]; ok {
		t.Fatalf("research preflight combined Google Search with function declarations: %#v", model.requests[0].Tools)
	}
	if len(model.requests[1].Tools) == 0 {
		t.Fatal("research report generation did not retain the researcher's function tools")
	}
	for _, group := range model.requests[1].Tools {
		if declarations, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration); ok {
			for _, declaration := range declarations {
				if declaration.Name == "research" {
					t.Fatal("researcher was allowed to recursively invoke research")
				}
			}
		}
	}
	if parent.cfg.ModelProvider != "openai-api" || parent.cfg.Model != "gpt-4.1" {
		t.Fatalf("research delegation changed the primary provider/model: %q/%q", parent.cfg.ModelProvider, parent.cfg.Model)
	}
}
