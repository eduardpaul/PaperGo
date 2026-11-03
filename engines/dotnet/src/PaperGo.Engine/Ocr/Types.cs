using System.Text.Json.Serialization;

namespace PaperGo.Engine.Ocr;

// The JSON shapes of internal/ocr. A quad is four image.Point values, which
// Go marshals with their field names X and Y.

public readonly record struct Point(int X, int Y);

public sealed record Region(
    [property: JsonPropertyName("quad")] Point[] Quad,
    [property: JsonPropertyName("score")] double Score);

public sealed record Detection(
    [property: JsonPropertyName("regions")] List<Region> Regions,
    [property: JsonPropertyName("width")] int Width,
    [property: JsonPropertyName("height")] int Height);

public sealed record Word(
    [property: JsonPropertyName("text")] string Text,
    [property: JsonPropertyName("quad")] Point[] Quad);

public sealed record Line(
    [property: JsonPropertyName("text")] string Text,
    [property: JsonPropertyName("confidence")] double Confidence,
    [property: JsonPropertyName("quad")] Point[] Quad,
    [property: JsonPropertyName("words")] List<Word>? Words);

public sealed record Page(
    [property: JsonPropertyName("width")] int Width,
    [property: JsonPropertyName("height")] int Height,
    [property: JsonPropertyName("lines")] List<Line> Lines);

public sealed record ModelFile(
    [property: JsonPropertyName("name")] string Name,
    [property: JsonPropertyName("sha256")] string Sha256);

public sealed record OcrInfo(
    [property: JsonPropertyName("engine")] string Engine,
    [property: JsonPropertyName("models")] List<ModelFile> Models);

public static class Limits
{
    /// <summary>ocr.MaxPixels.</summary>
    public const long MaxPixels = 64_000_000;

    /// <summary>ocr.MaxDetectionDimension.</summary>
    public const int MaxDetectionDimension = 2600;
}

/// <summary>An image or text line over the engine's memory bounds.</summary>
public sealed class TooLargeException(string message) : Exception(message);
