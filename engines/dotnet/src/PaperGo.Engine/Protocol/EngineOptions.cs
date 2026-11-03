using System.Globalization;

namespace PaperGo.Engine.Protocol;

/// <summary>Command-line flags with the core's environment variables as fallbacks.</summary>
public sealed class EngineOptions
{
    public string Listen { get; set; } = "";
    public string? TokenFile { get; set; }
    public bool ExitOnStdinEof { get; set; }
    public int Queue { get; set; } = 16;
    public string? OcrModels { get; set; }
    public int OcrThreads { get; set; } = 1;
    public List<(string Model, string Dir)> EmbeddingModels { get; } = [];
    public int EmbeddingsThreads { get; set; } = 1;
    public int EmbeddingsMaxTokens { get; set; } = 512;
    public string? DecisionModels { get; set; }
    public int DecisionThreads { get; set; } = 1;
    public int DecisionMaxTokens { get; set; } = 1024;
    public int DecisionHeadTokens { get; set; } = 256;
    /// <summary>Idle time before models unload; --idle-seconds exists for tests and benchmarks.</summary>
    public TimeSpan Idle { get; set; } = TimeSpan.FromMinutes(5);

    public const string Usage = """
        usage: papergo-engine --listen unix:/path.sock|tcp:127.0.0.1:PORT [flags]
          --token-file FILE            bearer token for TCP (required with tcp:)
          --exit-on-stdin-eof          exit when standard input closes
          --queue N                    requests waiting per model (default 16)
          --ocr-models DIR             PaddleOCR models (OCR_MODELS)
          --ocr-threads N              (OCR_THREADS, default 1)
          --embeddings-models M=DIR[,M=DIR]
                                       embedding models (AI_EMBEDDINGS_MODEL + AI_EMBEDDINGS_MODELS_PATH)
          --embeddings-threads N       (AI_EMBEDDINGS_THREADS, default 1)
          --embeddings-max-tokens N    (AI_EMBEDDINGS_MAX_TOKENS, default 512)
          --decision-models DIR        Laya bundle (AI_DECISION_MODELS_PATH)
          --decision-threads N         (AI_DECISION_THREADS, default 1)
          --decision-max-tokens N      (AI_DECISION_MAX_TOKENS, default 1024)
          --decision-head-tokens N     (AI_DECISION_HEAD_TOKENS, default 256)
          --idle-seconds N             unload idle models after N seconds (default 300)
        """;

    public static EngineOptions Parse(string[] args, Func<string, string?> env)
    {
        var o = new EngineOptions();
        static int Int(string name, string value) => int.TryParse(value, NumberStyles.Integer, CultureInfo.InvariantCulture, out var n)
            ? n : throw new ArgumentException($"{name} must be an integer");
        void EnvInt(string key, Action<int> set)
        {
            if (env(key) is { Length: > 0 } v)
            {
                set(Int(key, v));
            }
        }
        o.OcrModels = env("OCR_MODELS") is { Length: > 0 } ocr ? ocr : null;
        EnvInt("OCR_THREADS", v => o.OcrThreads = v);
        if (env("AI_EMBEDDINGS_MODELS_PATH") is { Length: > 0 } embeddingDir)
        {
            o.EmbeddingModels.Add((env("AI_EMBEDDINGS_MODEL") is { Length: > 0 } m ? m : "embeddinggemma-2", embeddingDir));
        }
        EnvInt("AI_EMBEDDINGS_THREADS", v => o.EmbeddingsThreads = v);
        EnvInt("AI_EMBEDDINGS_MAX_TOKENS", v => o.EmbeddingsMaxTokens = v);
        o.DecisionModels = env("AI_DECISION_MODELS_PATH") is { Length: > 0 } laya ? laya : null;
        EnvInt("AI_DECISION_THREADS", v => o.DecisionThreads = v);
        EnvInt("AI_DECISION_MAX_TOKENS", v => o.DecisionMaxTokens = v);
        EnvInt("AI_DECISION_HEAD_TOKENS", v => o.DecisionHeadTokens = v);
        bool embeddingsFlag = false;
        for (int i = 0; i < args.Length; i++)
        {
            var arg = args[i];
            string? inline = null;
            if (arg.StartsWith("--", StringComparison.Ordinal) && arg.IndexOf('=') is > 0 and var eq)
            {
                inline = arg[(eq + 1)..];
                arg = arg[..eq];
            }
            string Value()
            {
                if (inline != null)
                {
                    return inline;
                }
                if (++i >= args.Length)
                {
                    throw new ArgumentException($"{arg} needs a value");
                }
                return args[i];
            }
            switch (arg)
            {
                case "--listen": o.Listen = Value(); break;
                case "--token-file": o.TokenFile = Value(); break;
                case "--exit-on-stdin-eof": o.ExitOnStdinEof = true; break;
                case "--queue": o.Queue = Int(arg, Value()); break;
                case "--ocr-models": o.OcrModels = Value(); break;
                case "--ocr-threads": o.OcrThreads = Int(arg, Value()); break;
                case "--embeddings-models":
                    if (!embeddingsFlag)
                    {
                        o.EmbeddingModels.Clear();
                        embeddingsFlag = true;
                    }
                    foreach (var pair in Value().Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
                    {
                        var parts = pair.Split('=', 2);
                        if (parts.Length != 2 || parts[0] == "" || parts[1] == "")
                        {
                            throw new ArgumentException("--embeddings-models takes MODEL=DIR pairs");
                        }
                        o.EmbeddingModels.Add((parts[0], parts[1]));
                    }
                    break;
                case "--embeddings-threads": o.EmbeddingsThreads = Int(arg, Value()); break;
                case "--embeddings-max-tokens": o.EmbeddingsMaxTokens = Int(arg, Value()); break;
                case "--decision-models": o.DecisionModels = Value(); break;
                case "--decision-threads": o.DecisionThreads = Int(arg, Value()); break;
                case "--decision-max-tokens": o.DecisionMaxTokens = Int(arg, Value()); break;
                case "--decision-head-tokens": o.DecisionHeadTokens = Int(arg, Value()); break;
                case "--idle-seconds": o.Idle = TimeSpan.FromSeconds(Int(arg, Value())); break;
                default: throw new ArgumentException($"unknown flag {arg}");
            }
        }
        if (!o.Listen.StartsWith("unix:", StringComparison.Ordinal) && !o.Listen.StartsWith("tcp:", StringComparison.Ordinal))
        {
            throw new ArgumentException("--listen must be unix:/path or tcp:127.0.0.1:PORT");
        }
        if (o.Listen.StartsWith("tcp:", StringComparison.Ordinal) && string.IsNullOrEmpty(o.TokenFile))
        {
            throw new ArgumentException("--listen tcp: requires --token-file");
        }
        if (o.Queue is < 0 or > 1024)
        {
            throw new ArgumentException("--queue must be between 0 and 1024");
        }
        if (o.EmbeddingModels.Select(m => m.Model).Distinct().Count() != o.EmbeddingModels.Count)
        {
            throw new ArgumentException("an embedding model is configured twice");
        }
        return o;
    }
}
