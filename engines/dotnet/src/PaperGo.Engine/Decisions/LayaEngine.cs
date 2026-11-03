// The lossless marker serialization below is ported from internal/decision,
// which adapts Supersonic Labs' Apache-2.0 sequence and Collator functions
// (julia/data.py), independently checked against Laya's official Python
// encoder:
// https://huggingface.co/SupersonicLabs/Julia-1/blob/a85b127321d580d65176c89ced8273f305745d85/julia/data.py
// Laya multilingual is published by convaiinnovations under Apache-2.0. See
// internal/decision/NOTICE.

using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using Microsoft.ML.OnnxRuntime;
using Microsoft.ML.OnnxRuntime.Tensors;
using PaperGo.Engine.Runtime;

namespace PaperGo.Engine.Decisions;

/// <summary>decision.Request.</summary>
public sealed class DecisionRequest
{
    [JsonPropertyName("state")] public string State { get; set; } = "";
    [JsonPropertyName("question")] public string Question { get; set; } = "";
    [JsonPropertyName("options")] public List<string> Options { get; set; } = [];
    [JsonPropertyName("type")][JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)] public string Type { get; set; } = "";
}

/// <summary>decision.Encoding: the upstream marker serialization before padding.</summary>
public sealed record LayaEncoding(
    [property: JsonPropertyName("ids")] long[] Ids,
    [property: JsonPropertyName("markers")] long[] Markers,
    [property: JsonPropertyName("qtype")] long QType);

/// <summary>decision.Result.</summary>
public sealed class DecisionResult
{
    [JsonPropertyName("index")] public int Index { get; set; }
    [JsonPropertyName("act_logits")] public float[] ActLogits { get; set; } = [];
    [JsonPropertyName("logits")] public float[] Logits { get; set; } = [];
    [JsonPropertyName("probabilities")] public double[] Probabilities { get; set; } = [];
    [JsonPropertyName("score")][JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)] public double? Score { get; set; }
    [JsonPropertyName("noul")][JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)] public double? Noul { get; set; }
    [JsonPropertyName("tokens")] public int Tokens { get; set; }
}

/// <summary>A row that fails validation (invalid_input) or a lossless budget (too_many_tokens).</summary>
public sealed class RowException(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}

/// <summary>
/// Laya multilingual FP32 as internal/decision runs it: verified bundle,
/// strict lossless encoding and deterministic batches of up to 8 rows and
/// 2048 padded tokens.
/// </summary>
public sealed class LayaEngine : IDisposable
{
    public const string Revision = "1720e3e3357cfe1e281542e223f8273b0890ca34";
    const string ModelSha = "74dc1d3dccd214c105832b195f191f18c1172b799c78301fdd88f3925608d46e";

    readonly string dir;
    readonly int threads, maxTokens, headTokens;
    readonly JsonDocument manifest;
    Tokenizer? tokenizer;
    InferenceSession? session;
    long cls, sep, mask, pad;
    bool verified;

    public ModelGate Gate { get; }

    public LayaEngine(string dir, int threads, int maxTokens, int headTokens, int queue)
    {
        if (threads is < 1 or > 32 || maxTokens < 37 || maxTokens > 1024 || headTokens < 32 || headTokens > 256 || headTokens > maxTokens - 5)
        {
            throw new ArgumentException("laya requires 1–32 threads, 32–256 head tokens, and head+4 < max_tokens <= 1024");
        }
        this.dir = dir;
        this.threads = threads;
        this.maxTokens = maxTokens;
        this.headTokens = headTokens;
        using (var stream = typeof(LayaEngine).Assembly.GetManifestResourceStream("laya-manifest.json")!)
        {
            manifest = JsonDocument.Parse(stream);
        }
        // The startup check compares the manifest and finds the files; their
        // digests are verified on first use, as Go does.
        CheckManifest();
        foreach (var file in manifest.RootElement.GetProperty("files").EnumerateObject())
        {
            if (!File.Exists(Path.Combine(dir, file.Name)))
            {
                throw new FileNotFoundException($"laya {file.Name} not found");
            }
        }
        Gate = new ModelGate(queue, TimeSpan.FromMinutes(5), Unload);
    }

    /// <summary>decision.Engine.ID.</summary>
    public string Id => $"laya:{Revision}:{ModelSha}:fp32:t{maxTokens}:h{headTokens}:markers-v1:temperature1:batch8-2048-v1:threads{threads}";

    void CheckManifest()
    {
        using var installed = JsonDocument.Parse(File.ReadAllBytes(Path.Combine(dir, "manifest.json")));
        if (!JsonElement.DeepEquals(manifest.RootElement, installed.RootElement))
        {
            throw new InvalidOperationException("unsupported Laya manifest; install the pinned bundle");
        }
    }

    void VerifyBundle()
    {
        if (verified)
        {
            return;
        }
        CheckManifest();
        foreach (var file in manifest.RootElement.GetProperty("files").EnumerateObject())
        {
            using var f = File.OpenRead(Path.Combine(dir, file.Name));
            if (Convert.ToHexStringLower(SHA256.HashData(f)) != file.Value.GetString())
            {
                throw new InvalidOperationException($"laya {file.Name} checksum mismatch");
            }
        }
        verified = true;
    }

    void Unload()
    {
        verified = false;
        session?.Dispose();
        session = null;
        tokenizer?.Dispose();
        tokenizer = null;
    }

    public void Dispose()
    {
        Gate.Dispose();
        manifest.Dispose();
    }

    void LoadTokenizer()
    {
        if (tokenizer != null)
        {
            return;
        }
        var tk = Tokenizer.Load(Path.Combine(dir, "tokenizer.json"));
        var specials = new long[4];
        string[] names = ["<bos>", "<eos>", "<mask>", "<pad>"];
        for (int i = 0; i < names.Length; i++)
        {
            var ids = tk.EncodeIds(names[i], false);
            if (ids.Length != 1)
            {
                tk.Dispose();
                throw new InvalidOperationException($"laya tokenizer lacks {names[i]}");
            }
            specials[i] = ids[0];
        }
        (cls, sep, mask, pad) = (specials[0], specials[1], specials[2], specials[3]);
        tokenizer = tk;
        Gate.MarkLoaded();
    }

    void LoadModel()
    {
        if (session != null)
        {
            return;
        }
        var s = Onnx.Open(Path.Combine(dir, "model.onnx"), threads);
        static string Type(NodeMetadata m) => m.ElementDataType switch
        {
            TensorElementType.Int64 => "int64",
            TensorElementType.Bool => "bool",
            TensorElementType.Float => "float32",
            var t => t.ToString(),
        };
        if (!s.InputNames.SequenceEqual(["input_ids", "attention_mask", "marker_pos", "marker_mask", "qtype"])
            || !s.OutputNames.SequenceEqual(["logits", "act_logits"])
            || !s.InputNames.Select(n => Type(s.InputMetadata[n])).SequenceEqual(["int64", "int64", "int64", "bool", "int64"])
            || !s.OutputNames.Select(n => Type(s.OutputMetadata[n])).SequenceEqual(["float32", "float32"]))
        {
            s.Dispose();
            throw new InvalidOperationException("unexpected Laya ONNX graph; install the pinned export");
        }
        session = s;
    }

    static readonly string[] Reserved = ["<mask>", "<bos>", "<eos>", "<pad>"];

    /// <summary>validate: returns an error message, or null.</summary>
    public static string? Validate(DecisionRequest r)
    {
        if (r.State is null || r.Question is null || r.Options is null || r.Type is null || r.Options.Any(o => o is null))
        {
            return "laya state, question, options and type must be strings";
        }
        if (r.Options.Count < 2 || r.Options.Count > 20)
        {
            return "laya needs 2–20 options";
        }
        if (r.Type is not ("" or "choice" or "score" or "noul"))
        {
            return "laya type must be choice, score or noul";
        }
        if (r.Type == "noul" && r.Options.Count != 2)
        {
            return "noul needs false then true options";
        }
        if (Ocr.Crop.Blank(r.State) || Ocr.Crop.Blank(r.Question))
        {
            return "state and question must not be empty";
        }
        foreach (var text in r.Options.Prepend(r.Question).Prepend(r.State))
        {
            if (Encoding.UTF8.GetByteCount(text) > 128 * 1024 || text.Contains('\0') || Reserved.Any(m => text.Contains(m, StringComparison.Ordinal)))
            {
                return "laya text must be UTF-8, at most 128 KiB, without NUL or reserved markers";
            }
        }
        foreach (var option in r.Options)
        {
            if (Ocr.Crop.Blank(option))
            {
                return "laya options must not be empty";
            }
        }
        return null;
    }

    long[] Tokens(string s)
    {
        var ids = tokenizer!.EncodeIds(s, false);
        var out_ = new long[ids.Length];
        for (int i = 0; i < ids.Length; i++)
        {
            out_[i] = ids[i];
        }
        return out_;
    }

    /// <summary>encode: the verified markers-v1 lossless serialization.</summary>
    LayaEncoding EncodeRow(DecisionRequest r)
    {
        var kind = r.Type == "" ? "choice" : r.Type;
        var head = Tokens(kind + " question: " + r.Question);
        var options = new List<long[]>(r.Options.Count);
        int budget = headTokens;
        foreach (var option in r.Options)
        {
            var v = Tokens(" " + option);
            if (v.Length > 48)
            {
                throw new RowException("too_many_tokens", "laya option exceeds 48 tokens");
            }
            var o = new long[v.Length + 1];
            o[0] = mask;
            v.CopyTo(o, 1);
            options.Add(o);
            budget -= o.Length;
        }
        // Upstream truncates options when the remaining head budget falls
        // below 16. Strict encoding rejects any such loss.
        if (budget < 16)
        {
            int perOption = Math.Max(4, (headTokens - 16) / options.Count);
            if (options.Any(o => o.Length > perOption))
            {
                throw new RowException("too_many_tokens", "laya options exceed lossless head budget");
            }
        }
        if (head.Length > budget)
        {
            throw new RowException("too_many_tokens", "laya question/options exceed lossless head budget");
        }
        var ids = new List<long> { cls };
        ids.AddRange(head);
        ids.Add(sep);
        var markers = new List<long>();
        foreach (var o in options)
        {
            markers.Add(ids.Count);
            ids.AddRange(o);
        }
        ids.Add(sep);
        var state = Tokens(r.State);
        int room = maxTokens - ids.Count - 1;
        if (room < 1 || state.Length > room)
        {
            throw new RowException("too_many_tokens", "laya state exceeds lossless context budget; split the document");
        }
        ids.AddRange(state);
        ids.Add(sep);
        long qtype = kind switch { "score" => 1, "noul" => 2, _ => 0 };
        return new LayaEncoding([.. ids], [.. markers], qtype);
    }

    /// <summary>Encode: exact token IDs, loading only the tokenizer.</summary>
    public async Task<LayaEncoding> EncodeAsync(DecisionRequest r, CancellationToken cancel)
    {
        if (Validate(r) is { } error)
        {
            throw new RowException("invalid_input", error);
        }
        using var lease = await Gate.EnterAsync(cancel).ConfigureAwait(false);
        VerifyBundle();
        LoadTokenizer();
        return EncodeRow(r);
    }

    /// <summary>
    /// Decide: validates and encodes every row, then runs the valid rows in
    /// Go's deterministic batches. Failed rows get a null result and an error.
    /// </summary>
    public async Task<(DecisionResult?[] Results, List<(int Index, string Code, string Message)> Errors)> DecideAsync(IReadOnlyList<DecisionRequest> rows, CancellationToken cancel)
    {
        if (rows.Count < 1 || rows.Count > 50)
        {
            throw new ArgumentException("1–50 Laya questions per call");
        }
        var results = new DecisionResult?[rows.Count];
        var errors = new List<(int, string, string)>();
        var valid = new List<int>();
        for (int i = 0; i < rows.Count; i++)
        {
            if (Validate(rows[i]) is { } error)
            {
                errors.Add((i, "invalid_input", error));
            }
            else
            {
                valid.Add(i);
            }
        }
        if (valid.Count == 0)
        {
            return (results, errors);
        }
        using var lease = await Gate.EnterAsync(cancel).ConfigureAwait(false);
        VerifyBundle();
        LoadTokenizer();
        var encoded = new List<(int Row, LayaEncoding Enc)>();
        foreach (int i in valid)
        {
            cancel.ThrowIfCancellationRequested();
            try
            {
                encoded.Add((i, EncodeRow(rows[i])));
            }
            catch (RowException e)
            {
                errors.Add((i, e.Code, e.Message));
            }
        }
        errors.Sort((a, b) => a.Item1.CompareTo(b.Item1));
        if (encoded.Count == 0)
        {
            return (results, errors);
        }
        cancel.ThrowIfCancellationRequested();
        try
        {
            LoadModel();
        }
        catch
        {
            Unload();
            throw;
        }
        for (int start = 0; start < encoded.Count;)
        {
            int end = start, length = 0, k = 0;
            while (end < encoded.Count && end - start < 8)
            {
                int nextLength = Math.Min(maxTokens, (Math.Max(length, encoded[end].Enc.Ids.Length) + 7) / 8 * 8);
                if (end > start && (end - start + 1) * nextLength > 2048)
                {
                    break;
                }
                length = nextLength;
                k = Math.Max(k, encoded[end].Enc.Markers.Length);
                end++;
            }
            int n = end - start;
            var ids = new long[n * length];
            var attention = new long[n * length];
            var markers = new long[n * k];
            var markerMask = new bool[n * k];
            var qtype = new long[n];
            Array.Fill(ids, pad);
            for (int i = 0; i < n; i++)
            {
                var enc = encoded[start + i].Enc;
                enc.Ids.CopyTo(ids, i * length);
                enc.Markers.CopyTo(markers, i * k);
                Array.Fill(attention, 1, i * length, enc.Ids.Length);
                Array.Fill(markerMask, true, i * k, enc.Markers.Length);
                qtype[i] = enc.QType;
            }
            using var vIds = OrtValue.CreateTensorValueFromMemory(ids, [n, length]);
            using var vAttention = OrtValue.CreateTensorValueFromMemory(attention, [n, length]);
            using var vMarkers = OrtValue.CreateTensorValueFromMemory(markers, [n, k]);
            using var vMask = OrtValue.CreateTensorValueFromMemory(markerMask, [n, k]);
            using var vQtype = OrtValue.CreateTensorValueFromMemory(qtype, [n]);
            using var out_ = Onnx.Run(session!, session!.InputNames, [vIds, vAttention, vMarkers, vMask, vQtype], session.OutputNames, cancel);
            var logits = out_[0].GetTensorDataAsSpan<float>();
            var act = out_[1].GetTensorDataAsSpan<float>();
            if (out_.Count != 2 || !Onnx.Shape(out_[0]).SequenceEqual([n, k]) || !Onnx.Shape(out_[1]).SequenceEqual([n, 2]) || logits.Length != n * k || act.Length != n * 2)
            {
                throw new InvalidOperationException("unexpected Laya output shapes");
            }
            foreach (var v in logits)
            {
                if (!float.IsFinite(v))
                {
                    throw new InvalidOperationException("nonfinite laya output head 0");
                }
            }
            foreach (var v in act)
            {
                if (!float.IsFinite(v))
                {
                    throw new InvalidOperationException("nonfinite laya output head 1");
                }
            }
            for (int i = 0; i < n; i++)
            {
                var (row, enc) = encoded[start + i];
                var result = Probabilities(logits.Slice(i * k, enc.Markers.Length).ToArray(), rows[row].Type);
                result.ActLogits = act.Slice(i * 2, 2).ToArray();
                result.Tokens = enc.Ids.Length;
                results[row] = result;
            }
            start = end;
        }
        return (results, errors);
    }

    /// <summary>probabilities: softmax at temperature 1, with score and noul.</summary>
    public static DecisionResult Probabilities(float[] logits, string kind)
    {
        if (logits.Length < 2)
        {
            throw new InvalidOperationException("laya returned too few logits");
        }
        var r = new DecisionResult { Logits = logits, Probabilities = new double[logits.Length] };
        double peak = double.NegativeInfinity;
        for (int i = 0; i < logits.Length; i++)
        {
            double v = logits[i];
            if (!double.IsFinite(v))
            {
                throw new InvalidOperationException("laya returned nonfinite logits");
            }
            if (v > peak)
            {
                peak = v;
                r.Index = i;
            }
        }
        double total = 0;
        for (int i = 0; i < logits.Length; i++)
        {
            r.Probabilities[i] = Math.Exp(logits[i] - peak);
            total += r.Probabilities[i];
        }
        for (int i = 0; i < r.Probabilities.Length; i++)
        {
            r.Probabilities[i] /= total;
        }
        if (kind == "noul")
        {
            r.Noul = r.Probabilities[1];
        }
        if (kind == "score")
        {
            double v = 0;
            for (int i = 0; i < r.Probabilities.Length; i++)
            {
                v += i * r.Probabilities[i];
            }
            r.Score = v;
        }
        return r;
    }
}
