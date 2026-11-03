using System.Text;
using Microsoft.ML.OnnxRuntime;
using Microsoft.ML.OnnxRuntime.Tensors;
using PaperGo.Engine.Runtime;

namespace PaperGo.Engine.Embeddings;

/// <summary>An input over the model's token limit.</summary>
public sealed class TooManyTokensException(string message) : Exception(message);

/// <summary>
/// A local embedding model of internal/embedding: EmbeddingGemma 2 or
/// Harrier, Q4 ONNX exports, one input at a time. The tokenizer and session
/// load on first use and unload after five idle minutes.
/// </summary>
public sealed class EmbeddingEngine : IDisposable
{
    public const string Gemma2 = "embeddinggemma-2";
    public const string Harrier = "harrier-oss-v1-0.6b";

    readonly string dir;
    readonly int threads;
    Tokenizer? tokenizer;
    InferenceSession? session;

    public string Model { get; }
    public int Dimensions { get; }
    public int MaxTokens { get; }
    public ModelGate Gate { get; }

    public EmbeddingEngine(string model, string dir, int threads, int maxTokens, int queue)
    {
        int limit;
        if (model == Gemma2)
        {
            (limit, Dimensions) = (8192, 768);
        }
        else if (model == Harrier)
        {
            (limit, Dimensions) = (32768, 1024);
        }
        else
        {
            throw new ArgumentException($"unknown embedding model \"{model}\"");
        }
        if (threads is < 1 or > 32)
        {
            throw new ArgumentException("embedding threads must be between 1 and 32");
        }
        if (maxTokens < 2 || maxTokens > limit)
        {
            throw new ArgumentException($"embedding token limit must be between 2 and {limit}");
        }
        foreach (var file in new[] { "tokenizer.json", Path.Combine("onnx", "model_q4.onnx") })
        {
            if (!File.Exists(Path.Combine(dir, file)))
            {
                throw new FileNotFoundException($"embedding model file {Path.Combine(dir, file)} not found");
            }
        }
        Model = model;
        this.dir = dir;
        this.threads = threads;
        MaxTokens = maxTokens;
        Gate = new ModelGate(queue, TimeSpan.FromMinutes(5), Unload);
    }

    /// <summary>Whether the model supports a vector size.</summary>
    public bool SupportsDimensions(int d) => Model == Gemma2 ? d is 128 or 256 or 512 or 768 : d == 1024;

    /// <summary>
    /// embedding.Engine.ID: the vector space, including quantization,
    /// dimensions and input limit.
    /// </summary>
    public string Id(int dimensions)
    {
        var rev = Model == Harrier ? "e4daffa011e666dcd2ff2a3c6c05084090ac314d" : "daa72c51243991dfcaf9f9137d2c573d8f7790c0";
        return $"{Model}:{rev}:q4:d{dimensions}:t{MaxTokens}";
    }

    /// <summary>The prompt EmbedQuery or an untitled EmbedDocuments passage gives the model.</summary>
    public string Prompt(string text, bool query) => (Model, query) switch
    {
        (Harrier, true) => "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery: " + text,
        (Harrier, false) => text,
        (_, true) => "task: search result | query: " + text,
        _ => "title: none | text: " + text,
    };

    /// <summary>validateText: nonempty UTF-8, at most 128 KiB, without NUL.</summary>
    public static string? Validate(string text)
    {
        if (Ocr.Crop.Blank(text) || Encoding.UTF8.GetByteCount(text) > 128 * 1024 || text.Contains('\0'))
        {
            return "embedding text must be nonempty UTF-8, at most 128 KiB, without NUL";
        }
        return null;
    }

    void Unload()
    {
        session?.Dispose();
        session = null;
        tokenizer?.Dispose();
        tokenizer = null;
    }

    public void Dispose() => Gate.Dispose();

    void Load()
    {
        if (session != null)
        {
            return;
        }
        var tk = Tokenizer.Load(Path.Combine(dir, "tokenizer.json"));
        InferenceSession s;
        try
        {
            s = Onnx.Open(Path.Combine(dir, "onnx", "model_q4.onnx"), threads);
        }
        catch
        {
            tk.Dispose();
            throw;
        }
        string[] inputs = Model == Harrier
            ? ["input_ids", "attention_mask"]
            : ["input_ids", "attention_mask", "image_features", "video_features", "audio_features"];
        if (!s.InputNames.SequenceEqual(inputs) || !s.OutputNames.Contains("sentence_embedding"))
        {
            s.Dispose();
            tk.Dispose();
            throw new InvalidOperationException("unexpected embedding ONNX graph; use the pinned export");
        }
        tokenizer = tk;
        session = s;
        Gate.MarkLoaded();
    }

    /// <summary>
    /// Embeds prompts one at a time. A prompt over the token limit gets a
    /// null vector and its index in tooMany; the others are still embedded.
    /// </summary>
    public async Task<(float[]?[] Vectors, List<(int Index, string Message)> TooMany)> EmbedAsync(IReadOnlyList<string> prompts, int dimensions, CancellationToken cancel)
    {
        var vectors = new float[]?[prompts.Count];
        var tooMany = new List<(int, string)>();
        if (prompts.Count == 0)
        {
            return (vectors, tooMany);
        }
        using var lease = await Gate.EnterAsync(cancel).ConfigureAwait(false);
        Load();
        int native = Model == Harrier ? 1024 : 768;
        for (int i = 0; i < prompts.Count; i++)
        {
            cancel.ThrowIfCancellationRequested();
            var ids = tokenizer!.EncodeIds(prompts[i], addSpecialTokens: true);
            if (ids.Length > MaxTokens)
            {
                tooMany.Add((i, $"input {i} has {ids.Length} tokens; limit is {MaxTokens}: chunk the passage: too many tokens"));
                continue;
            }
            var input = new long[ids.Length];
            var mask = new long[ids.Length];
            for (int j = 0; j < ids.Length; j++)
            {
                input[j] = ids[j];
                mask[j] = 1;
            }
            long[] shape = [1, ids.Length];
            var values = new List<OrtValue>
            {
                OrtValue.CreateTensorValueFromMemory(input, shape),
                OrtValue.CreateTensorValueFromMemory(mask, shape),
            };
            try
            {
                if (Model == Gemma2)
                {
                    for (int k = 0; k < 3; k++)
                    {
                        values.Add(OrtValue.CreateAllocatedTensorValue(OrtAllocator.DefaultInstance, TensorElementType.Float, [0, 512]));
                    }
                }
                using var outputs = Onnx.Run(session!, session!.InputNames, values, ["sentence_embedding"], cancel);
                var shapeOut = Onnx.Shape(outputs[0]);
                var data = outputs[0].GetTensorDataAsSpan<float>();
                if (shapeOut.Length != 2 || shapeOut[0] != 1 || shapeOut[1] != native || data.Length != native)
                {
                    throw new InvalidOperationException("unexpected embedding output shape");
                }
                vectors[i] = Normalize(data[..dimensions].ToArray());
            }
            finally
            {
                foreach (var v in values)
                {
                    v.Dispose();
                }
            }
        }
        return (vectors, tooMany);
    }

    /// <summary>normalize: L2 normalization in float64.</summary>
    public static float[] Normalize(float[] vector)
    {
        double sum = 0;
        foreach (var v in vector)
        {
            if (!float.IsFinite(v))
            {
                throw new InvalidOperationException("embedding contains non-finite values");
            }
            sum += (double)v * v;
        }
        if (sum == 0)
        {
            throw new InvalidOperationException("embedding has zero norm");
        }
        double norm = Math.Sqrt(sum);
        for (int i = 0; i < vector.Length; i++)
        {
            vector[i] = (float)(vector[i] / norm);
        }
        return vector;
    }
}
