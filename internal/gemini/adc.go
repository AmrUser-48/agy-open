package gemini

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const googleCloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

func (c *LiveClient) agentPlatformCredentials(ctx context.Context) (projectID, location, token, endpoint string, err error) {
	projectID = strings.TrimSpace(c.ProjectID)
	location = strings.TrimSpace(c.Location)
	if location == "" { location = "us-central1" }
	endpoint = strings.TrimSpace(c.AgentPlatformEndpoint)
	if endpoint == "" {
		host := "aiplatform.googleapis.com"
		if location != "global" { host = location + "-aiplatform.googleapis.com" }
		endpoint = "wss://" + host + "/ws/google.cloud.aiplatform.v1beta1.LlmBidiService/BidiGenerateContent"
	}
	parsed, parseErr := url.Parse(endpoint)
	if parseErr != nil || (parsed.Scheme != "wss" && parsed.Scheme != "ws") || parsed.Host == "" {
		return "", "", "", "", errors.New("Gemini Agent Platform Live WebSocket endpoint must be an absolute ws:// or wss:// URL")
	}

	tokenSource := c.TokenSource
	if tokenSource == nil {
		credentials, credentialsErr := google.FindDefaultCredentials(ctx, googleCloudPlatformScope)
		if credentialsErr != nil {
			return "", "", "", "", fmt.Errorf("Google Application Default Credentials are unavailable: %w; run 'gcloud auth application-default login' and set GOOGLE_CLOUD_PROJECT", credentialsErr)
		}
		if projectID == "" { projectID = strings.TrimSpace(credentials.ProjectID) }
		tokenSource = credentials.TokenSource
	}
	if projectID == "" {
		return "", "", "", "", errors.New("Google Cloud project is missing; set GOOGLE_CLOUD_PROJECT or configure a project in Application Default Credentials")
	}
	if tokenSource == nil {
		return "", "", "", "", errors.New("Google Application Default Credentials did not provide an OAuth token source")
	}
	oauthToken, tokenErr := tokenSource.Token()
	if tokenErr != nil {
		return "", "", "", "", fmt.Errorf("refresh Google Application Default Credentials: %w", tokenErr)
	}
	if oauthToken == nil || strings.TrimSpace(oauthToken.AccessToken) == "" {
		return "", "", "", "", errors.New("Google Application Default Credentials returned an empty access token")
	}
	if !oauthToken.Valid() {
		return "", "", "", "", errors.New("Google Application Default Credentials returned an expired access token")
	}
	return projectID, location, oauthToken.AccessToken, endpoint, nil
}

func agentPlatformModelResource(projectID, location, model string) string {
	return fmt.Sprintf("projects/%s/locations/%s/publishers/google/models/%s", projectID, location, normalizeLiveModel(model))
}

func (c *LiveClient) openAgentPlatformSocket(ctx context.Context) (*liveWebSocket, string, error) {
	projectID, location, token, endpoint, err := c.agentPlatformCredentials(ctx)
	if err != nil { return nil, "", err }
	socket, err := dialLiveWebSocketAuth(ctx, endpoint, "", token)
	if err != nil { return nil, "", err }
	return socket, agentPlatformModelResource(projectID, location, c.Model), nil
}

// ValidateADC checks credentials, project/model access, and the Agent Platform
// Live setup handshake without generating a user response.
func (c *LiveClient) ValidateADC(ctx context.Context) error {
	if c == nil { return errors.New("Gemini Live client is nil") }
	socket, modelResource, err := c.openAgentPlatformSocket(ctx)
	if err != nil { return fmt.Errorf("initialize Gemini Live Agent Platform credentials: %w", err) }
	defer socket.close()
	setup := buildLiveSetup(c.Model, Request{})
	setup["model"] = modelResource
	if err := socket.writeJSON(map[string]any{"setup": setup}); err != nil {
		return fmt.Errorf("send Gemini Live Agent Platform setup: %w", err)
	}
	for {
		message, err := socket.readJSON()
		if err != nil { return fmt.Errorf("wait for Gemini Live Agent Platform setup: %w", err) }
		if err := liveMessageError(message); err != nil { return err }
		if _, ok := message["setupComplete"]; ok { return nil }
	}
}

var _ oauth2.TokenSource = oauth2.StaticTokenSource(&oauth2.Token{})
