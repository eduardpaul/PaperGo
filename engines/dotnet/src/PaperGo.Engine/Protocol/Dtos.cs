using System.Text.Json.Serialization;
using PaperGo.Engine.Decisions;
using PaperGo.Engine.Ocr;

namespace PaperGo.Engine.Protocol;

public sealed record ErrorBody(
    [property: JsonPropertyName("code")] string Code,
    [property: JsonPropertyName("message")] string Message);

public sealed record Health([property: JsonPropertyName("status")] string Status);

public sealed record EmbeddingInfo(
    [property: JsonPropertyName("id")] string Id,
    [property: JsonPropertyName("model")] string Model,
    [property: JsonPropertyName("dimensions")] int Dimensions,
    [property: JsonPropertyName("max_tokens")] int MaxTokens);

public sealed record DecisionInfo([property: JsonPropertyName("id")] string Id);

public sealed record Info(
    [property: JsonPropertyName("protocol")] int Protocol,
    [property: JsonPropertyName("version")] string Version,
    [property: JsonPropertyName("ocr")] OcrInfo? Ocr,
    [property: JsonPropertyName("embeddings")] List<EmbeddingInfo> Embeddings,
    [property: JsonPropertyName("decisions")] DecisionInfo? Decisions);

public sealed class EmbeddingRequest
{
    [JsonPropertyName("model")] public string Model { get; set; } = "";
    [JsonPropertyName("kind")] public string Kind { get; set; } = "";
    [JsonPropertyName("texts")] public List<string> Texts { get; set; } = [];
    [JsonPropertyName("dimensions")] public int Dimensions { get; set; }
}

public sealed record IndexError(
    [property: JsonPropertyName("index")] int Index,
    [property: JsonPropertyName("code")] string Code,
    [property: JsonPropertyName("message")] string Message);

public sealed record EmbeddingResponse(
    [property: JsonPropertyName("vectors")] float[]?[] Vectors,
    [property: JsonPropertyName("errors")] List<IndexError> Errors);

public sealed class DecisionBatch
{
    [JsonPropertyName("rows")] public List<DecisionRequest> Rows { get; set; } = [];
}

public sealed record DecisionResponse(
    [property: JsonPropertyName("results")] DecisionResult?[] Results,
    [property: JsonPropertyName("errors")] List<IndexError> Errors);

[JsonSourceGenerationOptions(UnmappedMemberHandling = JsonUnmappedMemberHandling.Disallow)]
[JsonSerializable(typeof(ErrorBody))]
[JsonSerializable(typeof(Health))]
[JsonSerializable(typeof(Info))]
[JsonSerializable(typeof(Detection))]
[JsonSerializable(typeof(Page))]
[JsonSerializable(typeof(EmbeddingRequest))]
[JsonSerializable(typeof(EmbeddingResponse))]
[JsonSerializable(typeof(DecisionBatch))]
[JsonSerializable(typeof(DecisionResponse))]
[JsonSerializable(typeof(LayaEncoding))]
public sealed partial class EngineJson : JsonSerializerContext;
