package workflowai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"papergo/internal/ai"
	"papergo/internal/doctext"
	"papergo/internal/forms"
	"papergo/internal/workflow"
)

const itemIDProp = `"item_id": {"type": "string", "title": "Item", "description": "The item; defaults to the run's item.", "x-papergo": {"kind": "item"}}`

func init() {
	workflow.RegisterActivity(workflow.ActivitySpec{
		Key:         "ai.complete",
		Kind:        "action",
		Description: "Asks the configured chat model to do something with text or an image: summarize it, extract values, clean it up. The input is text given directly, the text_id of document.read_text, or an image file version. With an output_schema the answer is a JSON object checked against it (json), else text. execution immediate calls the model in this step; batch parks the request on a named queue for the installation batch coordinator. Answers are cached: the same model, prompt, schema and input reuse the answer (cached).",
		InputSchema: inputSchema(itemIDProp+`,
			"prompt": {"type": "string", "title": "Prompt", "description": "What to do with the input."},
			"input": {"type": "string", "title": "Input", "description": "The text to work on."},
			"text_id": {"type": "string", "title": "Text", "description": "Instead of input: the text_id of document.read_text for the item's file."},
			"blob_id": {"type": "string", "title": "File version", "description": "With text_id: the file version it was read from; default the current one."},
			"image": {"type": "string", "title": "Image", "description": "File version the model sees. It can stand alone or accompany input."},
			"max_chars": {"type": "integer", "title": "Input size", "minimum": 100, "maximum": 200000, "default": 20000, "description": "Characters of the input sent to the model."},
			"output_schema": {"type": "object", "title": "Output schema", "description": "A form schema of an object: the answer is JSON matching it."},
			"execution": {"type": "string", "title": "Execution", "enum": ["immediate", "batch"], "description": "immediate calls the model now. batch waits for the installation coordinator. The deployment default applies when this is empty."},
			"queue": {"type": "string", "title": "Queue", "description": "Named batch queue. Empty uses shared.", "minLength": 1, "maxLength": 64}`, "prompt"),
		OutputSchema: forms.MustParse(`{"type": "object", "properties": {
			"text": {"type": "string"}, "json": {"type": "object"}, "cached": {"type": "boolean"}, "model": {"type": "string"}}}`),
		Prepare: prepareComplete, Work: workAIComplete, Commit: commitAIComplete,
	})
}

type completeJob struct {
	Key       string         `json:"key"`
	Model     string         `json:"model"`
	Prompt    string         `json:"prompt"`
	Input     string         `json:"input,omitempty"`
	ObjectKey string         `json:"object_key,omitempty"`
	MaxChars  int            `json:"max_chars"`
	Schema    map[string]any `json:"schema,omitempty"`
	Images    []imageRef     `json:"images,omitempty"`
}

type completeResult struct {
	Answer cachedAnswer `json:"answer"`
	Cached bool         `json:"cached"`
}

type storedText struct {
	Pages []doctext.Page `json:"pages"`
}

func prepareComplete(step workflow.Step, in map[string]any) (workflow.Prepared, error) {
	mode, err := executionMode(step, in)
	if err != nil {
		return workflow.Prepared{}, err
	}
	queue, err := queueName(step, in)
	if err != nil {
		return workflow.Prepared{}, err
	}
	tools := step.Tools()
	var model, connection, providerModel string
	if mode == "batch" {
		if tools.Batch == nil {
			return workflow.Prepared{}, ErrNoBatch
		}
		model, connection, providerModel = tools.Batch.Key(), tools.Batch.Connection(), tools.Batch.Model()
	} else {
		if tools.Chat == nil {
			return workflow.Prepared{}, ErrNoChat
		}
		model, connection = tools.Chat.Key(), chatConnection(tools)
	}
	job := completeJob{Model: model, MaxChars: defaultInputChars}
	if job.Prompt, err = step.OptionalText(in, "prompt"); err != nil {
		return workflow.Prepared{}, err
	}
	if job.Prompt == "" || len([]rune(job.Prompt)) > maxPromptChars {
		return workflow.Prepared{}, fmt.Errorf("prompt needs 1 to %d characters", maxPromptChars)
	}
	if err = step.Whole(in, "max_chars", &job.MaxChars, 100, maxInputChars); err != nil {
		return workflow.Prepared{}, err
	}
	if raw, ok := in["output_schema"]; ok && raw != nil {
		schema, ok := raw.(map[string]any)
		if !ok {
			return workflow.Prepared{}, errors.New("output_schema must be an object")
		}
		if err = forms.Validate(schema, "output_schema"); err != nil {
			return workflow.Prepared{}, err
		}
		if schema["type"] != "object" {
			return workflow.Prepared{}, errors.New("output_schema must describe an object")
		}
		job.Schema = schema
	}
	textID, err := step.OptionalText(in, "text_id")
	if err != nil {
		return workflow.Prepared{}, err
	}
	if raw, ok := in["input"]; ok {
		if job.Input, err = step.Text(raw); err != nil {
			return workflow.Prepared{}, err
		}
	}
	imageID, err := step.OptionalText(in, "image")
	if err != nil {
		return workflow.Prepared{}, err
	}
	var filePins []blobPin
	var source string
	switch {
	case textID != "" && job.Input != "":
		return workflow.Prepared{}, errors.New("give input or text_id, not both")
	case textID != "":
		itemID, err := step.Target(in)
		if err != nil {
			return workflow.Prepared{}, err
		}
		blobID, err := step.OptionalText(in, "blob_id")
		if err != nil {
			return workflow.Prepared{}, err
		}
		blob, err := step.DMS().GetBlob(step.Context(), step.Author(), itemID, blobID)
		if err != nil {
			return workflow.Prepared{}, err
		}
		text, err := step.DMS().BlobText(step.Context(), textID)
		if err != nil {
			return workflow.Prepared{}, err
		}
		if text.Sha256 != blob.Sha256 {
			return workflow.Prepared{}, errors.New("text_id is the text of another file")
		}
		job.ObjectKey, source = text.ObjectKey, "text_id:"+textID
		filePins = append(filePins, blobPin{ItemID: itemID, BlobID: blob.ID})
	default:
		if strings.TrimSpace(job.Input) == "" && imageID == "" {
			return workflow.Prepared{}, errors.New("give input, text_id, or an image")
		}
		if r := []rune(job.Input); len(r) > job.MaxChars {
			job.Input = string(r[:job.MaxChars])
		}
		if job.Input != "" {
			source = "input:" + hashText(job.Input)
		} else {
			source = "image"
		}
	}
	if imageID != "" {
		itemID, err := step.Target(in)
		if err != nil {
			return workflow.Prepared{}, err
		}
		blob, err := step.DMS().GetBlob(step.Context(), step.Author(), itemID, imageID)
		if err != nil {
			return workflow.Prepared{}, err
		}
		if !strings.HasPrefix(blob.ContentType, "image/") {
			return workflow.Prepared{}, errors.New("image must be an image file")
		}
		job.Images = append(job.Images, imageRef{ObjectKey: blob.ObjectKey, ContentType: blob.ContentType, ItemID: itemID, BlobID: blob.ID})
		filePins = append(filePins, blobPin{ItemID: itemID, BlobID: blob.ID})
	}
	schema, err := json.Marshal(job.Schema)
	if err != nil {
		return workflow.Prepared{}, err
	}
	job.Key = contentHash(model, "ai.complete", connection, queue, job.Prompt, source, schema, job.MaxChars, job.Images)
	if answer, found, err := findAnswerStep(step.Context(), step.DMS().Client, tools, "ai.complete", job.Key); err != nil {
		return workflow.Prepared{}, err
	} else if found {
		return workflow.Prepared{Output: answer.output(model, true)}, nil
	}
	if mode == "immediate" {
		raw, err := json.Marshal(job)
		if err != nil {
			return workflow.Prepared{}, err
		}
		return workflow.Prepared{Job: raw, Output: map[string]any{"model": model}}, nil
	}
	body := requestBody{
		Activity: "ai.complete", ModelKey: model, Model: providerModel, Connection: connection, Queue: queue,
		Prompt: job.Prompt, Input: job.Input, ObjectKey: job.ObjectKey, MaxChars: job.MaxChars, Schema: job.Schema, Images: job.Images,
	}
	if err = putRequest(step.Context(), step.DMS().Client, tools, job.Key, body); err != nil {
		return workflow.Prepared{}, err
	}
	data, err := json.Marshal(requestData{Hash: job.Key, Queue: queue, Activity: "ai.complete", Connection: connection, Model: model, Blobs: pins(filePins...)})
	if err != nil {
		return workflow.Prepared{}, err
	}
	if step.ExecutionID() == "" {
		return workflow.Prepared{}, errors.New("the batch request has no execution id")
	}
	return workflow.Prepared{Park: &workflow.Park{
		Kind: kindRequest, Key: step.ExecutionID(), Data: data, Deadline: deadlineAt(tools, tools.Clock()),
	}}, nil
}

func workAIComplete(ctx context.Context, tools workflow.Tools, raw json.RawMessage) (json.RawMessage, error) {
	var job completeJob
	if err := json.Unmarshal(raw, &job); err != nil {
		return nil, err
	}
	if answer, found, err := findAnswer(ctx, tools, "ai.complete", job.Key); err != nil {
		return nil, err
	} else if found {
		return json.Marshal(completeResult{Answer: answer, Cached: true})
	}
	if tools.Chat == nil {
		return nil, ErrNoChat
	}
	if tools.Chat.Key() != job.Model {
		return nil, errors.New("the chat model changed before the request ran")
	}
	input := job.Input
	if job.ObjectKey != "" {
		data, err := readObject(ctx, tools, job.ObjectKey)
		if err != nil {
			return nil, err
		}
		var stored storedText
		if err = json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("the stored text is unreadable: %w", err)
		}
		texts := make([]string, len(stored.Pages))
		for i, page := range stored.Pages {
			texts[i] = page.Text
		}
		input = shortText(strings.Join(texts, "\n\n"), job.MaxChars)
	}
	images, err := loadImages(ctx, tools, job.Images)
	if err != nil {
		return nil, err
	}
	answer, err := tools.Chat.Complete(ctx, ai.Completion{Instructions: job.Prompt, Input: input, Schema: job.Schema, Images: images})
	if err != nil {
		return nil, err
	}
	cached, err := acceptAnswer(ctx, tools, "ai.complete", job.Key, job.Model, job.Schema, answer)
	if err != nil {
		return nil, err
	}
	return json.Marshal(completeResult{Answer: cached})
}

func acceptAnswer(ctx context.Context, tools workflow.Tools, namespace, key, model string, schema map[string]any, answer ai.Answer) (cachedAnswer, error) {
	out := cachedAnswer{Text: answer.Text}
	if schema != nil {
		values, err := forms.Values(schema, answer.JSON, "answer")
		if err != nil {
			return cachedAnswer{}, fmt.Errorf("the model's answer does not match output_schema: %w", err)
		}
		out = cachedAnswer{JSON: values}
	}
	output, err := json.Marshal(out.output(model, false))
	if err != nil {
		return cachedAnswer{}, err
	}
	if len(output) > workflow.MaxOutputBytes {
		return cachedAnswer{}, errors.New("the AI answer exceeds the workflow output limit")
	}
	return saveAnswer(ctx, tools, namespace, key, out)
}

func loadImages(ctx context.Context, tools workflow.Tools, refs []imageRef) ([]ai.Image, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]ai.Image, len(refs))
	for i, ref := range refs {
		data, err := readObject(ctx, tools, ref.ObjectKey)
		if err != nil {
			return nil, err
		}
		out[i] = ai.Image{MediaType: ref.ContentType, Bytes: data}
	}
	return out, nil
}

func commitAIComplete(_ workflow.Step, rawJob, rawResult json.RawMessage) (workflow.Prepared, error) {
	var job completeJob
	var result completeResult
	if err := json.Unmarshal(rawJob, &job); err != nil {
		return workflow.Prepared{}, err
	}
	if err := json.Unmarshal(rawResult, &result); err != nil {
		return workflow.Prepared{}, err
	}
	return workflow.Prepared{Output: result.Answer.output(job.Model, result.Cached)}, nil
}
