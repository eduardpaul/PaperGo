using System.Text.Json;
using PaperGo.Engine.Decisions;
using PaperGo.Engine.Embeddings;
using PaperGo.Engine.Runtime;

namespace PaperGo.Engine.Tests;

// Goldens shared with the Go tests: internal/embedding/testdata/reference.json,
// internal/decision/testdata/typed-references.json and
// docs/performance-artifacts/laya/references.json. The Go engines are tested
// against the same IDs, so equal IDs here mean equal IDs to Go.
public class ModelTests
{
    static readonly string Repo = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "../../../../../../.."));

    static JsonElement Read(string path) => JsonDocument.Parse(File.ReadAllBytes(Path.Combine(Repo, path))).RootElement;

    [Fact]
    public async Task LayaEncodingMatchesTheReferences()
    {
        Assert.SkipWhen(!File.Exists(Path.Combine(Models.Laya, "model.onnx")), "Laya bundle not installed");
        using var laya = new LayaEngine(Models.Laya, 1, 1024, 256, 0);
        int rows = 0;
        foreach (var file in new[] { "internal/decision/testdata/typed-references.json", "docs/performance-artifacts/laya/references.json" })
        {
            foreach (var group in Read(file).GetProperty("groups").EnumerateArray())
            {
                foreach (var row in group.GetProperty("rows").EnumerateArray())
                {
                    var req = row.GetProperty("request");
                    var r = new DecisionRequest
                    {
                        State = req.GetProperty("state").GetString()!,
                        Question = req.GetProperty("question").GetString()!,
                        Options = [.. req.GetProperty("options").EnumerateArray().Select(o => o.GetString()!)],
                        Type = req.TryGetProperty("type", out var t) ? t.GetString()! : "",
                    };
                    var enc = await laya.EncodeAsync(r, TestContext.Current.CancellationToken);
                    Assert.Equal(row.GetProperty("ids").EnumerateArray().Select(v => v.GetInt64()), enc.Ids);
                    Assert.Equal(row.GetProperty("markers").EnumerateArray().Select(v => v.GetInt64()), enc.Markers);
                    Assert.Equal(row.GetProperty("qtype").GetInt64(), enc.QType);
                    rows++;
                }
            }
        }
        Assert.Equal(483, rows);
    }

    [Fact]
    public async Task LayaRejectsLossyRequests()
    {
        Assert.SkipWhen(!File.Exists(Path.Combine(Models.Laya, "model.onnx")), "Laya bundle not installed");
        using var laya = new LayaEngine(Models.Laya, 1, 1024, 256, 0);
        var ct = TestContext.Current.CancellationToken;
        var tooLong = new DecisionRequest { State = string.Concat(Enumerable.Repeat("word ", 2000)), Question = "q?", Options = ["a", "b"] };
        var e = await Assert.ThrowsAsync<RowException>(() => laya.EncodeAsync(tooLong, ct));
        Assert.Equal("too_many_tokens", e.Code);
        var bad = new DecisionRequest { State = "s", Question = "q", Options = ["<mask>", "b"] };
        Assert.Equal("invalid_input", (await Assert.ThrowsAsync<RowException>(() => laya.EncodeAsync(bad, ct))).Code);
        Assert.Equal("laya needs 2–20 options", LayaEngine.Validate(new DecisionRequest { State = "s", Question = "q", Options = ["a"] }));
        Assert.Equal("noul needs false then true options", LayaEngine.Validate(new DecisionRequest { State = "s", Question = "q", Options = ["a", "b", "c"], Type = "noul" }));
    }

    [Fact]
    public void LayaProbabilities()
    {
        var r = LayaEngine.Probabilities([0f, MathF.Log(3)], "noul");
        Assert.Equal(1, r.Index);
        Assert.Equal(0.75, r.Probabilities[1], 6);
        Assert.Equal(0.75, r.Noul!.Value, 6);
        Assert.Null(r.Score);
        var s = LayaEngine.Probabilities([1f, 1f, 1f, 1f], "score");
        Assert.Equal(1.5, s.Score!.Value, 9);
        Assert.Equal(0, s.Index);
    }

    [Theory]
    [InlineData(EmbeddingEngine.Gemma2)]
    [InlineData(EmbeddingEngine.Harrier)]
    public async Task EmbeddingMatchesTheReference(string model)
    {
        var dir = Models.Embedding(model);
        Assert.SkipWhen(!Directory.Exists(dir), "embedding models not installed");
        var reference = Read("internal/embedding/testdata/reference.json").EnumerateArray().First(r => r.GetProperty("model").GetString() == model);
        using var engine = new EmbeddingEngine(model, dir, 1, 512, 0);
        var query = reference.GetProperty("query").GetString()!;
        var doc = reference.GetProperty("document");
        var title = doc.GetProperty("Title").GetString()!;
        var text = doc.GetProperty("Text").GetString()!;
        // The protocol's documents have no title; the reference's prompts do.
        string[] prompts = [engine.Prompt(query, true), model == EmbeddingEngine.Gemma2 ? $"title: {title} | text: {text}" : $"{title}\n{text}"];
        Assert.Equal(reference.GetProperty("prompts")[0].GetString(), prompts[0]);
        using (var tk = Tokenizer.Load(Path.Combine(dir, "tokenizer.json")))
        {
            for (int i = 0; i < 2; i++)
            {
                Assert.Equal(reference.GetProperty("ids")[i].EnumerateArray().Select(v => v.GetUInt32()), tk.EncodeIds(prompts[i], true));
            }
        }
        var (vectors, tooMany) = await engine.EmbedAsync(prompts, engine.Dimensions, TestContext.Current.CancellationToken);
        Assert.Empty(tooMany);
        for (int i = 0; i < 2; i++)
        {
            var want = reference.GetProperty("vectors")[i].EnumerateArray().Select(v => v.GetSingle()).ToArray();
            Assert.Equal(want.Length, vectors[i]!.Length);
            Assert.All(want.Zip(vectors[i]!), p => Assert.InRange(p.Second - p.First, -1e-5f, 1e-5f));
        }
        var (_, over) = await engine.EmbedAsync([string.Concat(Enumerable.Repeat("invoice ", 600))], engine.Dimensions, TestContext.Current.CancellationToken);
        Assert.Equal(0, Assert.Single(over).Index);
        Assert.Equal(engine.Id(engine.Dimensions), $"{model}:{(model == EmbeddingEngine.Harrier ? "e4daffa011e666dcd2ff2a3c6c05084090ac314d" : "daa72c51243991dfcaf9f9137d2c573d8f7790c0")}:q4:d{engine.Dimensions}:t512");
        engine.Gate.Unload();
    }

    [Fact]
    public void EmbeddingNormalizes()
    {
        var v = EmbeddingEngine.Normalize([3f, 4f]);
        Assert.Equal([0.6f, 0.8f], v);
        Assert.Throws<InvalidOperationException>(() => EmbeddingEngine.Normalize([0f, 0f]));
        Assert.NotNull(EmbeddingEngine.Validate(" \n"));
        Assert.NotNull(EmbeddingEngine.Validate("a\0b"));
        Assert.Null(EmbeddingEngine.Validate("ok"));
    }
}
