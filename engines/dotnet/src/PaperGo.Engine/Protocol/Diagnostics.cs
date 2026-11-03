using System.Text.Json;
using System.Text.Json.Serialization;
using PaperGo.Engine.Decisions;
using PaperGo.Engine.Embeddings;
using PaperGo.Engine.Runtime;

namespace PaperGo.Engine.Protocol;

public sealed class EncodeInput
{
    [JsonPropertyName("requests")] public List<DecisionRequest> Requests { get; set; } = [];
    [JsonPropertyName("max_tokens")] public int MaxTokens { get; set; }
    [JsonPropertyName("head_tokens")] public int HeadTokens { get; set; }
}

public sealed record EncodeOutput(
    [property: JsonPropertyName("ids")][property: JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)] long[]? Ids,
    [property: JsonPropertyName("markers")][property: JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)] long[]? Markers,
    [property: JsonPropertyName("qtype")][property: JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)] long? QType,
    [property: JsonPropertyName("error")][property: JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)] string? Error);

[JsonSerializable(typeof(List<string>))]
[JsonSerializable(typeof(List<uint[]>))]
[JsonSerializable(typeof(EncodeInput))]
[JsonSerializable(typeof(List<EncodeOutput>))]
public sealed partial class DiagnosticsJson : JsonSerializerContext;

/// <summary>
/// Offline subcommands for the parity harness: they run the engine's own
/// tokenizer and Laya encoder on JSON from stdin, without a server.
/// </summary>
public static class Diagnostics
{
    public const string Usage = """
          papergo-engine tokenize TOKENIZER.json [--special] [--prompt MODEL:query|document] < ["text", ...]
          papergo-engine laya-encode LAYA_DIR < {"requests": [...], "max_tokens": 0, "head_tokens": 0}
        """;

    static readonly DiagnosticsJson Json = new(new JsonSerializerOptions { Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping });

    public static async Task<int> Run(string[] args)
    {
        using var stdin = Console.OpenStandardInput();
        using var stdout = Console.OpenStandardOutput();
        switch (args[0])
        {
            case "tokenize":
            {
                var texts = await JsonSerializer.DeserializeAsync(stdin, Json.ListString) ?? [];
                bool special = args.Contains("--special");
                int p = Array.IndexOf(args, "--prompt");
                Func<string, string> prompt = s => s;
                if (p > 0)
                {
                    var parts = args[p + 1].Split(':');
                    var model = new EmbeddingPrompt(parts[0]);
                    bool query = parts[1] == "query";
                    prompt = s => model.Prompt(s, query);
                }
                using var tk = Tokenizer.Load(args[1]);
                var ids = texts.Select(t => tk.EncodeIds(prompt(t), special)).ToList();
                await JsonSerializer.SerializeAsync(stdout, ids, Json.ListUInt32Array);
                return 0;
            }
            case "laya-encode":
            {
                var input = await JsonSerializer.DeserializeAsync(stdin, Json.EncodeInput) ?? new EncodeInput();
                using var laya = new LayaEngine(args[1], 1, input.MaxTokens == 0 ? 1024 : input.MaxTokens, input.HeadTokens == 0 ? 256 : input.HeadTokens, 0);
                var out_ = new List<EncodeOutput>();
                foreach (var r in input.Requests)
                {
                    try
                    {
                        var e = await laya.EncodeAsync(r, CancellationToken.None);
                        out_.Add(new EncodeOutput(e.Ids, e.Markers, e.QType, null));
                    }
                    catch (RowException e)
                    {
                        out_.Add(new EncodeOutput(null, null, null, e.Message));
                    }
                }
                await JsonSerializer.SerializeAsync(stdout, out_, Json.ListEncodeOutput);
                return 0;
            }
        }
        await Console.Error.WriteLineAsync(Usage);
        return 2;
    }

    /// <summary>The engine's prompt rules without loading a model.</summary>
    sealed class EmbeddingPrompt(string model)
    {
        public string Prompt(string text, bool query) => EmbeddingEngine.PromptFor(model, text, query);
    }
}
