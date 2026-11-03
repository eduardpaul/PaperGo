using System.Net;
using System.Net.Http.Headers;
using System.Net.Sockets;
using System.Text;
using System.Text.Json;
using PaperGo.Engine.Protocol;

namespace PaperGo.Engine.Tests;

public sealed class ProtocolTests : IAsyncLifetime
{
    readonly string dir = Directory.CreateTempSubdirectory("engine-test").FullName;
    WebApplication? app;
    Engines? engines;
    HttpClient client = new();

    string Socket => Path.Combine(dir, "e.sock");

    async Task Start(params string[] args)
    {
        var options = EngineOptions.Parse(["--listen", "unix:" + Socket, .. args], _ => null);
        engines = Engines.Create(options);
        app = EngineHost.Build([], options, engines);
        await app.StartAsync(TestContext.Current.CancellationToken);
        EngineHost.AfterStart(options);
        var handler = new SocketsHttpHandler
        {
            ConnectCallback = async (_, cancel) =>
            {
                var s = new System.Net.Sockets.Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
                await s.ConnectAsync(new UnixDomainSocketEndPoint(Socket), cancel);
                return new NetworkStream(s, ownsSocket: true);
            },
        };
        client = new HttpClient(handler) { BaseAddress = new Uri("http://engine") };
    }

    public ValueTask InitializeAsync() => ValueTask.CompletedTask;

    public async ValueTask DisposeAsync()
    {
        client.Dispose();
        if (app != null)
        {
            await app.StopAsync();
            await app.DisposeAsync();
        }
        engines?.Dispose();
        Directory.Delete(dir, true);
    }

    static async Task<(HttpStatusCode, JsonElement)> Json(HttpResponseMessage r) =>
        (r.StatusCode, JsonDocument.Parse(await r.Content.ReadAsStringAsync()).RootElement.Clone());

    static StringContent Body(string json) => new(json, Encoding.UTF8, "application/json");

    [Fact]
    public async Task HealthInfoAndSocketMode()
    {
        await Start();
        var (status, health) = await Json(await client.GetAsync("/v1/health", TestContext.Current.CancellationToken));
        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal("ok", health.GetProperty("status").GetString());
        var (_, info) = await Json(await client.GetAsync("/v1/info", TestContext.Current.CancellationToken));
        Assert.Equal(1, info.GetProperty("protocol").GetInt32());
        Assert.Equal(JsonValueKind.Null, info.GetProperty("ocr").ValueKind);
        Assert.Equal(JsonValueKind.Null, info.GetProperty("decisions").ValueKind);
        Assert.Equal(0, info.GetProperty("embeddings").GetArrayLength());
        if (!OperatingSystem.IsWindows())
        {
            Assert.Equal(UnixFileMode.UserRead | UnixFileMode.UserWrite, File.GetUnixFileMode(Socket));
        }
    }

    [Fact]
    public async Task UnconfiguredCapabilitiesAreUnavailable()
    {
        await Start();
        var ct = TestContext.Current.CancellationToken;
        var (s1, e1) = await Json(await client.PostAsync("/v1/embeddings", Body("""{"model":"embeddinggemma-2","kind":"query","texts":["a"]}"""), ct));
        Assert.Equal(HttpStatusCode.NotFound, s1);
        Assert.Equal("unavailable", e1.GetProperty("code").GetString());
        var (s2, e2) = await Json(await client.PostAsync("/v1/decisions", Body("""{"rows":[]}"""), ct));
        Assert.Equal(HttpStatusCode.NotFound, s2);
        Assert.Equal("unavailable", e2.GetProperty("code").GetString());
        var (s3, _) = await Json(await client.PostAsync("/v1/ocr/detect", new ByteArrayContent([0]), ct));
        Assert.Equal(HttpStatusCode.NotFound, s3);
    }

    HttpRequestMessage Pixels(string path, int w, int h, string format, byte[] body)
    {
        var req = new HttpRequestMessage(HttpMethod.Post, path) { Content = new ByteArrayContent(body) };
        req.Content.Headers.ContentType = new MediaTypeHeaderValue("application/octet-stream");
        req.Headers.Add("X-Image-Width", w.ToString());
        req.Headers.Add("X-Image-Height", h.ToString());
        req.Headers.Add("X-Pixel-Format", format);
        return req;
    }

    [Fact]
    public async Task OcrValidatesPixels()
    {
        Assert.SkipWhen(!Directory.Exists(Models.Ocr), "PaddleOCR models not installed");
        await Start("--ocr-models", Models.Ocr);
        var ct = TestContext.Current.CancellationToken;
        var (s1, e1) = await Json(await client.SendAsync(Pixels("/v1/ocr/detect", 2, 2, "cmyk", new byte[16]), ct));
        Assert.Equal(HttpStatusCode.BadRequest, s1);
        Assert.Equal("invalid_input", e1.GetProperty("code").GetString());
        var (s2, _) = await Json(await client.SendAsync(Pixels("/v1/ocr/detect", 2, 2, "rgb8", new byte[11]), ct));
        Assert.Equal(HttpStatusCode.BadRequest, s2);
        var (s3, e3) = await Json(await client.SendAsync(Pixels("/v1/ocr/recognize", 10000, 6401, "gray8", [0]), ct));
        Assert.Equal(HttpStatusCode.RequestEntityTooLarge, s3);
        Assert.Equal("too_large", e3.GetProperty("code").GetString());
        var (s4, _) = await Json(await client.SendAsync(Pixels("/v1/ocr/detect?max_dimension=100", 2, 2, "gray8", new byte[4]), ct));
        Assert.Equal(HttpStatusCode.BadRequest, s4);
        // A blank page has no regions and no lines.
        var white = Enumerable.Repeat((byte)255, 64 * 48).ToArray();
        var (s5, det) = await Json(await client.SendAsync(Pixels("/v1/ocr/detect?min_score=0.5", 64, 48, "gray8", white), ct));
        Assert.Equal(HttpStatusCode.OK, s5);
        Assert.Equal(0, det.GetProperty("regions").GetArrayLength());
        Assert.Equal(64, det.GetProperty("width").GetInt32());
        var (_, page) = await Json(await client.SendAsync(Pixels("/v1/ocr/recognize", 64, 48, "gray8", white), ct));
        Assert.Equal(0, page.GetProperty("lines").GetArrayLength());
        var (_, info) = await Json(await client.GetAsync("/v1/info", ct));
        Assert.Equal("paddleocr", info.GetProperty("ocr").GetProperty("engine").GetString());
        Assert.Equal(4, info.GetProperty("ocr").GetProperty("models").GetArrayLength());
    }

    [Fact]
    public async Task MalformedJsonIsInvalidInput()
    {
        Assert.SkipWhen(!Directory.Exists(Models.Embedding("embeddinggemma-2")), "embedding models not installed");
        await Start("--embeddings-models", "embeddinggemma-2=" + Models.Embedding("embeddinggemma-2"));
        var ct = TestContext.Current.CancellationToken;
        foreach (var body in new[]
        {
            "{", """{"model":"embeddinggemma-2","kind":"query","texts":["a"],"extra":1}""",
            """{"model":"embeddinggemma-2","kind":"other","texts":["a"]}""",
            """{"model":"embeddinggemma-2","kind":"query","texts":[" "]}""",
            """{"model":"embeddinggemma-2","kind":"query","texts":["a"],"dimensions":100}""",
            """{"model":"embeddinggemma-2","kind":"query","texts":[]}""",
        })
        {
            var (status, e) = await Json(await client.PostAsync("/v1/embeddings", Body(body), ct));
            Assert.Equal(HttpStatusCode.BadRequest, status);
            Assert.Equal("invalid_input", e.GetProperty("code").GetString());
        }
        var (_, info) = await Json(await client.GetAsync("/v1/info", ct));
        var model = info.GetProperty("embeddings")[0];
        Assert.Equal("embeddinggemma-2:daa72c51243991dfcaf9f9137d2c573d8f7790c0:q4:d768:t512", model.GetProperty("id").GetString());
        Assert.Equal(512, model.GetProperty("max_tokens").GetInt32());
    }

    [Fact]
    public async Task TcpRequiresTheBearerToken()
    {
        var tokenFile = Path.Combine(dir, "token");
        await File.WriteAllTextAsync(tokenFile, "s3cret\n", TestContext.Current.CancellationToken);
        int port;
        using (var probe = new TcpListener(IPAddress.Loopback, 0))
        {
            probe.Start();
            port = ((IPEndPoint)probe.LocalEndpoint).Port;
        }
        var options = EngineOptions.Parse(["--listen", $"tcp:127.0.0.1:{port}", "--token-file", tokenFile], _ => null);
        engines = Engines.Create(options);
        app = EngineHost.Build([], options, engines);
        await app.StartAsync(TestContext.Current.CancellationToken);
        using var tcp = new HttpClient { BaseAddress = new Uri($"http://127.0.0.1:{port}") };
        var (s1, e1) = await Json(await tcp.GetAsync("/v1/health", TestContext.Current.CancellationToken));
        Assert.Equal(HttpStatusCode.Unauthorized, s1);
        Assert.Equal("unauthorized", e1.GetProperty("code").GetString());
        tcp.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", "wrong");
        Assert.Equal(HttpStatusCode.Unauthorized, (await tcp.GetAsync("/v1/health", TestContext.Current.CancellationToken)).StatusCode);
        tcp.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", "s3cret");
        Assert.Equal(HttpStatusCode.OK, (await tcp.GetAsync("/v1/health", TestContext.Current.CancellationToken)).StatusCode);
    }

    [Fact]
    public void OptionsMirrorTheCoreEnvironment()
    {
        var env = new Dictionary<string, string>
        {
            ["OCR_MODELS"] = "/ocr", ["OCR_THREADS"] = "2", ["AI_EMBEDDINGS_MODEL"] = "harrier-oss-v1-0.6b",
            ["AI_EMBEDDINGS_MODELS_PATH"] = "/h", ["AI_DECISION_MODELS_PATH"] = "/laya", ["AI_DECISION_HEAD_TOKENS"] = "128",
        };
        var o = EngineOptions.Parse(["--listen", "unix:/s", "--ocr-threads=3", "--embeddings-models", "embeddinggemma-2=/g,harrier-oss-v1-0.6b=/h2"], k => env.GetValueOrDefault(k));
        Assert.Equal("/ocr", o.OcrModels);
        Assert.Equal(3, o.OcrThreads);
        Assert.Equal([("embeddinggemma-2", "/g"), ("harrier-oss-v1-0.6b", "/h2")], o.EmbeddingModels);
        Assert.Equal("/laya", o.DecisionModels);
        Assert.Equal(128, o.DecisionHeadTokens);
        Assert.Throws<ArgumentException>(() => EngineOptions.Parse(["--listen", "tcp:127.0.0.1:1"], _ => null));
        Assert.Throws<ArgumentException>(() => EngineOptions.Parse(["--listen", "unix:/s", "--bogus"], _ => null));
    }

    [Fact]
    public async Task FullQueueIsBusy()
    {
        var gate = new Runtime.ModelGate(0, TimeSpan.FromMinutes(5), () => { });
        using var held = await gate.EnterAsync(TestContext.Current.CancellationToken);
        await Assert.ThrowsAsync<Runtime.BusyException>(() => gate.EnterAsync(TestContext.Current.CancellationToken));
    }

    [Fact]
    public async Task IdleModelsUnload()
    {
        int unloads = 0;
        var gate = new Runtime.ModelGate(4, TimeSpan.FromMilliseconds(50), () => unloads++);
        using (await gate.EnterAsync(TestContext.Current.CancellationToken))
        {
            gate.MarkLoaded();
        }
        await Task.Delay(500, TestContext.Current.CancellationToken);
        Assert.Equal(1, unloads);
    }
}
