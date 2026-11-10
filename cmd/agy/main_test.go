package main

import "testing"

func TestParseLoginOptionsBrowserRequestsChatGPTPlan(t *testing.T) {
	for _, args := range [][]string{
		{"openai", "--browser"},
		{"--browser", "openai"},
	} {
		options, err := parseLoginOptions(args)
		if err != nil {
			t.Fatalf("parseLoginOptions(%v): %v", args, err)
		}
		if len(options.providerArgs) != 1 || options.providerArgs[0] != "openai" {
			t.Fatalf("providerArgs = %v, want [openai]", options.providerArgs)
		}
		if !options.withChatGPTPlan || options.withAPIKey {
			t.Fatalf("options = %#v, want browser plan login", options)
		}
	}
}

func TestParseLoginOptionsKeepsLegacyPlanFlag(t *testing.T) {
	options, err := parseLoginOptions([]string{"openai", "--with-chatgpt-plan"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.withChatGPTPlan || options.withAPIKey {
		t.Fatalf("options = %#v, want ChatGPT-plan login", options)
	}
}

func TestParseLoginOptionsRejectsAPIKeyAndBrowserTogether(t *testing.T) {
	if _, err := parseLoginOptions([]string{"openai", "--with-api-key", "--browser"}); err == nil {
		t.Fatal("expected API-key/browser conflict")
	}
}

func TestParseLoginOptionsKeepsAPIKeyFlow(t *testing.T) {
	options, err := parseLoginOptions([]string{"openai", "--with-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.withAPIKey || options.withChatGPTPlan {
		t.Fatalf("options = %#v, want API-key login", options)
	}
}
