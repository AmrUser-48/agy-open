package gemini

import (
	"strings"
	"testing"
)

func TestOnboardProject(t *testing.T) {
	project := "test-project-123"
	resp := caOnboardResponse{
		Response: &struct {
			CloudAICompanionProject *struct {
				ID            string `json:"id,omitempty"`
				Name          string `json:"name,omitempty"`
				ProjectNumber string `json:"projectNumber,omitempty"`
			} `json:"cloudaicompanionProject,omitempty"`
			Status *struct {
				StatusCode     string `json:"statusCode,omitempty"`
				DisplayMessage string `json:"displayMessage,omitempty"`
			} `json:"status,omitempty"`
		}{
			CloudAICompanionProject: &struct {
				ID            string `json:"id,omitempty"`
				Name          string `json:"name,omitempty"`
				ProjectNumber string `json:"projectNumber,omitempty"`
			}{ID: project},
		},
	}
	if got := onboardProject(&resp); got != project {
		t.Fatalf("onboardProject() = %q, want %q", got, project)
	}
}

func TestOnboardingStateErrorIncludesServerState(t *testing.T) {
	loaded := caLoadResponse{
		CurrentTier: &caTier{ID: "free-tier"},
		AllowedTiers: []caTier{
			{ID: "free-tier", IsDefault: true},
			{ID: "standard-tier"},
		},
		IneligibleTiers: []struct {
			ReasonCode    string `json:"reasonCode,omitempty"`
			ReasonMessage string `json:"reasonMessage,omitempty"`
			TierID        string `json:"tierId,omitempty"`
			TierName      string `json:"tierName,omitempty"`
			ValidationURL string `json:"validationUrl,omitempty"`
		}{
			{ReasonMessage: "account not eligible"},
		},
	}
	msg := onboardingStateError("onboardUser", caOnboardResponse{Done: true}, loaded).Error()
	for _, want := range []string{"free-tier", "allowed tiers=free-tier,standard-tier", "account not eligible", "operation=done"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain %q", msg, want)
		}
	}
}
