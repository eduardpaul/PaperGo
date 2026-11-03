using System.Globalization;
using System.Reflection;
using System.Security.Cryptography;
using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.Json.Serialization.Metadata;
using Microsoft.AspNetCore.Http.Features;
using PaperGo.Engine.Decisions;
using PaperGo.Engine.Embeddings;
using PaperGo.Engine.Ocr;
using PaperGo.Engine.Runtime;

namespace PaperGo.Engine.Protocol;

/// <summary>The configured models.</summary>
public sealed class Engines : IDisposable
{
    public PaddleEngine? Ocr { get; init; }
    public Dictionary<string, EmbeddingEngine> Embeddings { get; } = [];
    public LayaEngine? Decisions { get; init; }

    /// <summary>Creates and load-checks every configured capability.</summary>
    public static Engines Create(EngineOptions o)
    {
        var e = new Engines
        {
            Ocr = o.OcrModels is { } ocr ? new PaddleEngine(ocr, o.OcrThreads, o.Queue) : null,
            Decisions = o.DecisionModels is { } laya
                ? new LayaEngine(laya, o.DecisionThreads, o.DecisionMaxTokens, o.DecisionHeadTokens, o.Queue)
                : null,
        };
        foreach (var (model, dir) in o.EmbeddingModels)
        {
            e.Embeddings[model] = new EmbeddingEngine(model, dir, o.EmbeddingsThreads, o.EmbeddingsMaxTokens, o.Queue);
        }
        foreach (var gate in e.Gates)
        {
            gate.Idle = o.Idle;
        }
        return e;
    }

    public IEnumerable<ModelGate> Gates => new[] { Ocr?.Gate, Decisions?.Gate }.Concat(Embeddings.Values.Select(m => m.Gate)).OfType<ModelGate>();

    public void Dispose()
    {
        Ocr?.Dispose();
        Decisions?.Dispose();
        foreach (var m in Embeddings.Values)
        {
            m.Dispose();
        }
    }
}

/// <summary>A request the protocol rejects.</summary>
public sealed class ProtocolException(int status, string code, string message) : Exception(message)
{
    public int Status { get; } = status;
    public string Code { get; } = code;
}

/// <summary>The HTTP protocol of docs/plans/engine-protocol.md.</summary>
public static class Server
{
    public const int ProtocolVersion = 1;
    const long JsonBodyLimit = 64L << 20;

    public static readonly EngineJson Json = new(new JsonSerializerOptions
    {
        Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Disallow,
    });

    public static string Version { get; } = (typeof(Server).Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion ?? "0.0.0").Split('+')[0];

    /// <summary>Maps the endpoints and, for TCP, the bearer token check.</summary>
    public static void Map(WebApplication app, Engines engines, byte[]? token)
    {
        if (token != null)
        {
            var want = SHA256.HashData(token);
            app.Use(async (ctx, next) =>
            {
                var header = ctx.Request.Headers.Authorization.ToString();
                var got = header.StartsWith("Bearer ", StringComparison.Ordinal) ? Encoding.UTF8.GetBytes(header[7..]) : [];
                // Hashing first makes the comparison constant-time in the length too.
                if (!CryptographicOperations.FixedTimeEquals(SHA256.HashData(got), want))
                {
                    await Error(ctx, 401, "unauthorized", "a valid bearer token is required");
                    return;
                }
                await next(ctx);
            });
        }
        app.MapGet("/v1/health", ctx => ctx.Response.WriteAsJsonAsync(new Health("ok"), Json.Health));
        app.MapGet("/v1/info", ctx => ctx.Response.WriteAsJsonAsync(GetInfo(engines), Json.Info));
        app.MapPost("/v1/ocr/detect", ctx => Handle(ctx, () => Ocr(ctx, engines, recognize: false)));
        app.MapPost("/v1/ocr/recognize", ctx => Handle(ctx, () => Ocr(ctx, engines, recognize: true)));
        app.MapPost("/v1/embeddings", ctx => Handle(ctx, () => Embed(ctx, engines)));
        app.MapPost("/v1/decisions", ctx => Handle(ctx, () => Decide(ctx, engines)));
    }

    public static Info GetInfo(Engines e) => new(
        ProtocolVersion,
        Version,
        e.Ocr?.Info,
        [.. e.Embeddings.Values.OrderBy(m => m.Model, StringComparer.Ordinal).Select(m => new EmbeddingInfo(m.Id(m.Dimensions), m.Model, m.Dimensions, m.MaxTokens))],
        e.Decisions is { } d ? new DecisionInfo(d.Id) : null);

    static Task Error(HttpContext ctx, int status, string code, string message)
    {
        ctx.Response.StatusCode = status;
        return ctx.Response.WriteAsJsonAsync(new ErrorBody(code, message), Json.ErrorBody);
    }

    static async Task Handle(HttpContext ctx, Func<Task> body)
    {
        try
        {
            await body();
        }
        catch (OperationCanceledException) when (ctx.RequestAborted.IsCancellationRequested)
        {
            // The client is gone; there is nobody to answer.
        }
        catch (Exception e) when (!ctx.Response.HasStarted)
        {
            var (status, code) = e switch
            {
                ProtocolException p => (p.Status, p.Code),
                BusyException => (503, "busy"),
                TooLargeException => (413, "too_large"),
                BadHttpRequestException { StatusCode: 413 } => (413, "too_large"),
                BadHttpRequestException or JsonException or ArgumentException => (400, "invalid_input"),
                _ => (500, "internal"),
            };
            if (status == 503)
            {
                ctx.Response.Headers.RetryAfter = "1";
            }
            await Error(ctx, status, code, e.Message);
        }
    }

    static void LimitBody(HttpContext ctx, long limit)
    {
        var feature = ctx.Features.Get<IHttpMaxRequestBodySizeFeature>();
        if (feature is { IsReadOnly: false })
        {
            feature.MaxRequestBodySize = limit;
        }
        if (ctx.Request.ContentLength > limit)
        {
            throw new ProtocolException(413, "too_large", $"the body exceeds {limit} bytes");
        }
    }

    static async Task<T> ReadJson<T>(HttpContext ctx, JsonTypeInfo<T> type)
    {
        LimitBody(ctx, JsonBodyLimit);
        return await JsonSerializer.DeserializeAsync(ctx.Request.Body, type, ctx.RequestAborted)
            ?? throw new ProtocolException(400, "invalid_input", "the body is null");
    }

    static int HeaderInt(HttpContext ctx, string name)
    {
        var v = ctx.Request.Headers[name].ToString();
        if (!int.TryParse(v, NumberStyles.None, CultureInfo.InvariantCulture, out var n) || n <= 0)
        {
            throw new ProtocolException(400, "invalid_input", $"{name} must be a positive integer");
        }
        return n;
    }

    static async Task Ocr(HttpContext ctx, Engines engines, bool recognize)
    {
        var ocr = engines.Ocr ?? throw new ProtocolException(404, "unavailable", "OCR is not configured");
        int w = HeaderInt(ctx, "X-Image-Width"), h = HeaderInt(ctx, "X-Image-Height");
        var format = ctx.Request.Headers["X-Pixel-Format"].ToString();
        int bpp = Raster.BytesPerPixel(format);
        if (bpp == 0)
        {
            throw new ProtocolException(400, "invalid_input", "X-Pixel-Format must be rgba8, rgb8 or gray8");
        }
        if ((long)w * h > Limits.MaxPixels)
        {
            throw new ProtocolException(413, "too_large", "the image exceeds the OCR limit of 64 million pixels");
        }
        double minScore = 0;
        int maxDimension = 0;
        if (!recognize)
        {
            var q = ctx.Request.Query;
            if (q["min_score"].ToString() is { Length: > 0 } s
                && (!double.TryParse(s, NumberStyles.Float, CultureInfo.InvariantCulture, out minScore) || !(minScore >= 0 && minScore <= 1)))
            {
                throw new ProtocolException(400, "invalid_input", "min_score must be between 0 and 1");
            }
            if (q["max_dimension"].ToString() is { Length: > 0 } d
                && (!int.TryParse(d, NumberStyles.None, CultureInfo.InvariantCulture, out maxDimension) || maxDimension != 0 && (maxDimension < 256 || maxDimension > Limits.MaxDetectionDimension)))
            {
                throw new ProtocolException(400, "invalid_input", $"max_dimension must be 0 or between 256 and {Limits.MaxDetectionDimension}");
            }
        }
        long expected = (long)w * h * bpp;
        LimitBody(ctx, expected);
        if (ctx.Request.ContentLength is { } length && length != expected)
        {
            throw new ProtocolException(400, "invalid_input", $"the body must hold {expected} bytes of {format} pixels");
        }
        var pixels = GC.AllocateUninitializedArray<byte>(checked((int)expected));
        try
        {
            await ctx.Request.Body.ReadExactlyAsync(pixels, ctx.RequestAborted);
        }
        catch (EndOfStreamException)
        {
            throw new ProtocolException(400, "invalid_input", $"the body must hold {expected} bytes of {format} pixels");
        }
        var img = Raster.FromPixels(format, w, h, pixels);
        if (recognize)
        {
            var page = await ocr.RecognizeAsync(img, ctx.RequestAborted);
            await ctx.Response.WriteAsJsonAsync(page, Json.Page, cancellationToken: ctx.RequestAborted);
        }
        else
        {
            var det = await ocr.DetectAsync(img, minScore, maxDimension, ctx.RequestAborted);
            await ctx.Response.WriteAsJsonAsync(det, Json.Detection, cancellationToken: ctx.RequestAborted);
        }
    }

    static async Task Embed(HttpContext ctx, Engines engines)
    {
        var req = await ReadJson(ctx, Json.EmbeddingRequest);
        if (!engines.Embeddings.TryGetValue(req.Model, out var engine))
        {
            throw new ProtocolException(404, "unavailable", $"embedding model \"{req.Model}\" is not configured");
        }
        if (req.Kind is not ("query" or "document"))
        {
            throw new ProtocolException(400, "invalid_input", "kind must be query or document");
        }
        if (req.Texts.Count is < 1 or > 2000)
        {
            throw new ProtocolException(400, "invalid_input", "texts needs 1–2000 entries");
        }
        int dimensions = req.Dimensions == 0 ? engine.Dimensions : req.Dimensions;
        if (!engine.SupportsDimensions(dimensions))
        {
            throw new ProtocolException(400, "invalid_input", "invalid embedding dimensions for the model");
        }
        var prompts = new string[req.Texts.Count];
        for (int i = 0; i < prompts.Length; i++)
        {
            if (EmbeddingEngine.Validate(req.Texts[i] ?? "") is { } error)
            {
                throw new ProtocolException(400, "invalid_input", $"text {i}: {error}");
            }
            prompts[i] = engine.Prompt(req.Texts[i], req.Kind == "query");
        }
        var (vectors, tooMany) = await engine.EmbedAsync(prompts, dimensions, ctx.RequestAborted);
        var errors = tooMany.Select(t => new IndexError(t.Index, "too_many_tokens", t.Message)).ToList();
        await ctx.Response.WriteAsJsonAsync(new EmbeddingResponse(vectors, errors), Json.EmbeddingResponse, cancellationToken: ctx.RequestAborted);
    }

    static async Task Decide(HttpContext ctx, Engines engines)
    {
        var laya = engines.Decisions ?? throw new ProtocolException(404, "unavailable", "decisions are not configured");
        var req = await ReadJson(ctx, Json.DecisionBatch);
        if (req.Rows.Count is < 1 or > 50)
        {
            throw new ProtocolException(400, "invalid_input", "rows needs 1–50 entries");
        }
        var (results, errors) = await laya.DecideAsync(req.Rows, ctx.RequestAborted);
        var body = new DecisionResponse(results, [.. errors.Select(e => new IndexError(e.Index, e.Code, e.Message))]);
        await ctx.Response.WriteAsJsonAsync(body, Json.DecisionResponse, cancellationToken: ctx.RequestAborted);
    }
}
