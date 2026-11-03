namespace PaperGo.Engine.Tests;

/// <summary>Model directories for the native tests, overridable through the environment.</summary>
static class Models
{
    static readonly string Repo = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "../../../../../../.."));

    static string Find(string env, params string[] candidates) =>
        Environment.GetEnvironmentVariable(env) is { Length: > 0 } v ? v
        : candidates.FirstOrDefault(Directory.Exists) ?? candidates[0];

    public static string Ocr => Find("OCR_MODELS", Path.Combine(Repo, "models/paddleocr"), "/home/eduapaul/repos/PaperGo/models/paddleocr");

    public static string Assets => Find("PAPERGO_MODEL_ASSETS", Path.Combine(Repo, "bin/assets/models"), "/home/eduapaul/repos/PaperGo/bin/assets/models");

    public static string Laya => Path.Combine(Assets, "laya");

    public static string Embedding(string model) => Path.Combine(Assets, "embedding", model);
}
