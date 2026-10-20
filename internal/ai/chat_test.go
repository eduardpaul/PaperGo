package ai_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"papergo/internal/ai"
	"papergo/internal/ai/aitest"
)

func TestChatAnswersTextAndJSON(t *testing.T) {
	server := aitest.New(t)
	chat, err := ai.NewChat(ai.ChatConfig{Provider: ai.ProviderOpenAI, Endpoint: server.Endpoint(), Model: "fake-chat"}, nil)
	if err != nil || chat == nil || chat.Key() == "" {
		t.Fatal(chat, err)
	}
	ctx := context.Background()
	answer, err := chat.Complete(ctx, ai.Completion{Instructions: "Summarize in two sentences.", Input: "Receipt from the bakery, total 12.50"})
	if err != nil || answer.Text != "Summarize: Receipt from the bakery, total 12.50" || answer.JSON != nil {
		t.Fatalf("%+v %v", answer, err)
	}
	// JSON answers, also in a Markdown fence, are objects.
	schema := map[string]any{"type": "object", "properties": map[string]any{"vendor": map[string]any{"type": "string"}, "total": map[string]any{"type": "number"}}}
	answer, err = chat.Complete(ctx, ai.Completion{Instructions: "Extract the receipt.", Input: "Bakery 12.50", Schema: schema})
	if err != nil || answer.JSON["vendor"] != "vendor Bakery" || answer.JSON["total"] != float64(42) {
		t.Fatalf("%+v %v", answer, err)
	}
	// A busy provider is worth trying again; a refusal is not.
	server.Fail(1, http.StatusTooManyRequests)
	if _, err = chat.Complete(ctx, ai.Completion{Instructions: "x", Input: "y"}); err == nil {
		t.Fatal("a busy provider answered")
	} else if _, retry := ai.RetryAfter(err); !retry {
		t.Fatal("a busy provider is not worth trying again:", err)
	}
	server.Fail(1, http.StatusUnauthorized)
	var failure *ai.Error
	if _, err = chat.Complete(ctx, ai.Completion{Instructions: "x", Input: "y"}); !errors.As(err, &failure) || failure.Status != http.StatusUnauthorized || failure.Model != "chat model" || failure.Retry {
		t.Fatal(err)
	}
	if c, err := ai.NewChat(ai.ChatConfig{Provider: ai.ProviderNone}, nil); c != nil || err != nil {
		t.Fatal(c, err)
	}
	for _, c := range []ai.ChatConfig{{Provider: "local", Endpoint: ai.DefaultEndpoint, Model: "m"}, {Provider: ai.ProviderOpenAI, Endpoint: ai.DefaultEndpoint}, {Provider: ai.ProviderOpenAI, Endpoint: "x", Model: "m"}} {
		if c.Validate() == nil {
			t.Fatalf("%+v is valid", c)
		}
	}
}

func TestChatIdentitySeparatesEndpoints(t *testing.T) {
	chat := func(endpoint, model, apiKey string) ai.Chat {
		t.Helper()
		c, err := ai.NewChat(ai.ChatConfig{Provider: ai.ProviderOpenAI, Endpoint: endpoint, Model: model, APIKey: apiKey}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	first := chat("https://one.example/v1", "m", "secret")
	if first.Key() == chat("https://two.example/v1", "m", "secret").Key() {
		t.Fatal("different endpoints share cached answers")
	}
	if first.Key() == chat("https://one.example/v1", "other-model", "secret").Key() {
		t.Fatal("different models share cached answers")
	}
	if first.Key() != chat("https://one.example/v1/chat/completions/", "m", "new-secret").Key() {
		t.Fatal("equivalent routes or rotated credentials change model identity")
	}
}
