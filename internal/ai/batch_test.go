package ai_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"papergo/internal/ai"
	"papergo/internal/ai/aitest"
)

func TestBatchProtocolAndAzureBase(t *testing.T) {
	server := aitest.NewBatchServer(t)
	client, err := ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderOpenAI, Endpoint: server.AzureEndpoint(), Model: "gpt-batch"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	chat, err := ai.NewChat(ai.ChatConfig{Provider: ai.ProviderOpenAI, Endpoint: server.Endpoint(), Model: "gpt-batch"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if chat.Key() == client.Key() {
		t.Fatal("an Azure batch base hashed as the chat base")
	}
	same, err := ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderOpenAI, Endpoint: server.Endpoint() + "/chat/completions", Model: "gpt-batch"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if same.Key() != chat.Key() || same.Connection() != server.Endpoint() {
		t.Fatalf("batch key %s connection %s, chat %s", same.Key(), same.Connection(), chat.Key())
	}
	ctx := context.Background()
	fileID, err := client.Upload(ctx, []byte("{\"custom_id\":\"abc\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	tag := strings.Repeat("ab", 32)
	batchID, err := client.Create(ctx, fileID, tag)
	if err != nil || batchID == "" {
		t.Fatal(err)
	}
	found, ok, err := client.FindByTag(ctx, tag)
	if err != nil || !ok || found != batchID {
		t.Fatalf("find: %s %v %v", found, ok, err)
	}
	batch, err := client.Get(ctx, batchID)
	if err != nil || !batch.Terminal() || batch.OutputFileID == "" {
		t.Fatalf("batch: %+v %v", batch, err)
	}
	body, err := client.Download(ctx, batch.OutputFileID)
	if err != nil || !bytesContains(body, "answer:") {
		t.Fatalf("download: %s %v", body, err)
	}
	var sawFile, sawBatch bool
	for _, call := range server.Calls() {
		if !strings.HasPrefix(call.Path, "/openai/v1/") {
			t.Fatalf("path %s", call.Path)
		}
		if strings.Contains(call.Path, "/v1/v1/") {
			t.Fatalf("doubled version: %s", call.Path)
		}
		if call.Method == "POST" && strings.HasSuffix(call.Path, "/files") {
			sawFile = true
			if !strings.HasPrefix(call.ContentType, "multipart/form-data") || !bytesContains(call.Body, "application/octet-stream") || !bytesContains(call.Body, "name=\"purpose\"") {
				t.Fatalf("upload: %s %s", call.ContentType, call.Body)
			}
		}
		if call.Method == "POST" && strings.HasSuffix(call.Path, "/batches") {
			sawBatch = true
			text := string(call.Body)
			for _, want := range []string{`"input_file_id"`, `"/v1/chat/completions"`, `"24h"`, ai.MetadataKey} {
				if !strings.Contains(text, want) {
					t.Fatalf("create body missing %s: %s", want, text)
				}
			}
		}
	}
	if !sawFile || !sawBatch {
		t.Fatal("missing upload or create")
	}
}

func TestAmbiguousBatchCreateIsNotRetried(t *testing.T) {
	server := aitest.NewBatchServer(t)
	client, err := ai.NewBatch(ai.BatchConfig{Provider: ai.ProviderOpenAI, Endpoint: server.Endpoint(), Model: "gpt-batch"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fileID, err := client.Upload(ctx, []byte("{\"custom_id\":\"abc\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	server.FailCreates(1, true)
	tag := strings.Repeat("cd", 32)
	if _, err = client.Create(ctx, fileID, tag); !errors.Is(err, ai.ErrUncertain) {
		t.Fatalf("create: %v", err)
	}
	if server.Creates() != 1 {
		t.Fatalf("creates %d", server.Creates())
	}
	found, ok, err := client.FindByTag(ctx, tag)
	if err != nil || !ok || found == "" {
		t.Fatalf("landed batch not found: %s %v %v", found, ok, err)
	}
	server.FailCreates(1, false)
	if _, err = client.Create(ctx, fileID, "other"); !errors.Is(err, ai.ErrUncertain) {
		t.Fatalf("missed create: %v", err)
	}
	if _, ok, err = client.FindByTag(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing tag: %v %v", ok, err)
	}
	server.RejectNextCreate()
	_, err = client.Create(ctx, fileID, "rejected")
	var provider *ai.Error
	if !errors.As(err, &provider) || errors.Is(err, ai.ErrUncertain) || provider.Status != 400 {
		t.Fatalf("definite failure: %v", err)
	}
}

func bytesContains(raw []byte, part string) bool {
	return strings.Contains(string(raw), part)
}
