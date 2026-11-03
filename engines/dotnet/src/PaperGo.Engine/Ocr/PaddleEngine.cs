using System.Security.Cryptography;
using Microsoft.ML.OnnxRuntime;
using PaperGo.Engine.Runtime;

namespace PaperGo.Engine.Ocr;

/// <summary>
/// The PaddleOCR engine of internal/ocr/paddle: PP-OCRv6 small detection and
/// recognition with the PP-LCNet text-line orientation classifier. Models
/// load on first use and unload after five idle minutes.
/// </summary>
public sealed class PaddleEngine : IDisposable
{
    public const string DetectorModel = "PP-OCRv6_small_det.onnx";
    public const string RecognizerModel = "PP-OCRv6_small_rec.onnx";
    public const string ClassifierModel = "ch_PP-LCNet_x0_25_textline_ori_cls_mobile.onnx";
    public const string Dictionary = "ppocrv6_small_dict.txt";

    const double DetThreshold = 0.2, UnclipRatio = 1.4, RecognizeMinScore = 0.5, MinLineConfidence = 0.5, FlipConfidence = 0.9;
    const int RecHeight = 48;
    const double MaxDetectionPixels = 1 << 20;

    // The detector uses ImageNet normalization (0.485·255 …); recognition and
    // classification scale pixels to [-1, 1].
    static readonly float[] DetMean = [123.675f, 116.28f, 103.53f];
    static readonly float[] DetStd = [58.395f, 57.12f, 57.375f];
    static readonly float[] LineMean = [127.5f, 127.5f, 127.5f];
    static readonly float[] LineStd = [127.5f, 127.5f, 127.5f];

    readonly string dir;
    readonly int threads, maxDimension;
    Models? models;

    public ModelGate Gate { get; }
    public OcrInfo Info { get; }

    sealed class Models : IDisposable
    {
        public required InferenceSession Det, Cls, Rec;
        public required string[] Keys;
        public int ClsW, ClsH;

        public void Dispose()
        {
            Det.Dispose();
            Cls.Dispose();
            Rec.Dispose();
        }
    }

    /// <summary>Checks the configuration and hashes the model files, as paddle.New.</summary>
    public PaddleEngine(string dir, int threads, int queue, int maxDetectionDimension = 1536)
    {
        if (threads is < 1 or > 32)
        {
            throw new ArgumentException("OCR threads must be between 1 and 32");
        }
        if (maxDetectionDimension is < 256 or > 2600 || maxDetectionDimension % 32 != 0)
        {
            throw new ArgumentException("the maximum detection dimension must be a multiple of 32 between 256 and 2600");
        }
        this.dir = dir;
        this.threads = threads;
        maxDimension = maxDetectionDimension;
        var files = new List<ModelFile>();
        foreach (var name in new[] { DetectorModel, RecognizerModel, ClassifierModel, Dictionary })
        {
            using var f = File.OpenRead(Path.Combine(dir, name));
            files.Add(new ModelFile(name, Convert.ToHexStringLower(SHA256.HashData(f))));
        }
        Info = new OcrInfo("paddleocr", files);
        Gate = new ModelGate(queue, TimeSpan.FromMinutes(5), Unload);
    }

    void Unload()
    {
        models?.Dispose();
        models = null;
    }

    public void Dispose() => Gate.Dispose();

    Models Load()
    {
        if (models != null)
        {
            return models;
        }
        InferenceSession? det = null, cls = null, rec = null;
        try
        {
            det = Onnx.Open(Path.Combine(dir, DetectorModel), threads);
            cls = Onnx.Open(Path.Combine(dir, ClassifierModel), threads);
            rec = Onnx.Open(Path.Combine(dir, RecognizerModel), threads);
            var keys = ReadKeys(Path.Combine(dir, Dictionary));
            var recShape = rec.OutputMetadata.Values.First().Dimensions;
            if (recShape.Length != 3 || recShape[2] != keys.Length)
            {
                throw new InvalidOperationException($"the recognition model's output does not match the dictionary's {keys.Length - 2} characters");
            }
            var clsShape = cls.InputMetadata.Values.First().Dimensions;
            if (clsShape.Length != 4 || clsShape[2] <= 0 || clsShape[3] <= 0)
            {
                throw new InvalidOperationException("the classifier input has no fixed size");
            }
            models = new Models { Det = det, Cls = cls, Rec = rec, Keys = keys, ClsH = clsShape[2], ClsW = clsShape[3] };
            Gate.MarkLoaded();
            return models;
        }
        catch
        {
            det?.Dispose();
            cls?.Dispose();
            rec?.Dispose();
            throw;
        }
    }

    /// <summary>readKeys: the CTC blank, one class per dictionary line and a final space.</summary>
    public static string[] ReadKeys(string path)
    {
        var text = File.ReadAllText(path);
        if (text.EndsWith('\n'))
        {
            text = text[..^1];
        }
        var keys = new List<string> { "#" };
        foreach (var l in text.Split('\n'))
        {
            keys.Add(l.EndsWith('\r') ? l[..^1] : l);
        }
        keys.Add(" ");
        return [.. keys];
    }

    static void CheckSize(int w, int h)
    {
        if (w <= 0 || h <= 0)
        {
            throw new ArgumentException("the image is empty");
        }
        if ((long)w * h > Limits.MaxPixels)
        {
            throw new TooLargeException("the image exceeds the OCR limit of 64 million pixels");
        }
    }

    /// <summary>Detect: finds text regions.</summary>
    public async Task<Detection> DetectAsync(Raster img, double minScore, int maxDimension, CancellationToken cancel)
    {
        CheckSize(img.Width, img.Height);
        if (maxDimension != 0 && (maxDimension < 256 || maxDimension > Limits.MaxDetectionDimension))
        {
            throw new ArgumentException($"the detection dimension must be between 256 and {Limits.MaxDetectionDimension}");
        }
        using var lease = await Gate.EnterAsync(cancel).ConfigureAwait(false);
        var m = Load();
        return Detect(m, img, maxDimension, minScore, cancel);
    }

    /// <summary>Recognize: reads the image's text lines.</summary>
    public async Task<Page> RecognizeAsync(Raster img, CancellationToken cancel)
    {
        CheckSize(img.Width, img.Height);
        using var lease = await Gate.EnterAsync(cancel).ConfigureAwait(false);
        var m = Load();
        var src = Imaging.Opaque(img);
        var page = new Page(src.Width, src.Height, []);
        var det = Detect(m, src, 0, RecognizeMinScore, cancel);
        foreach (var region in det.Regions)
        {
            var (line, quad) = Crop.Rectify(src, region.Quad);
            if (UpsideDown(m, line, cancel))
            {
                line = Imaging.Rotate180(line);
                quad = [quad[2], quad[3], quad[0], quad[1]];
            }
            var (chars, scores, columns, count) = Read(m, line, cancel);
            if (scores.Count == 0)
            {
                continue;
            }
            double sum = 0;
            foreach (var s in scores)
            {
                sum += s;
            }
            double confidence = sum / scores.Count;
            var text = string.Concat(chars);
            if (confidence < MinLineConfidence || Crop.Blank(text))
            {
                continue;
            }
            page.Lines.Add(new Line(text, confidence, quad, Crop.Words(chars, columns, count, quad)));
        }
        return page;
    }

    /// <summary>detectionDimension: a smaller preview for squarer images.</summary>
    public static int DetectionDimension(int w, int h, int maximum)
    {
        double longSide = Math.Max(w, h), shortSide = Math.Min(w, h);
        return Math.Min(maximum, Math.Max(32, (int)Math.Sqrt(MaxDetectionPixels * longSide / shortSide) / 32 * 32));
    }

    /// <summary>networkDimension: the nearest multiple of 32.</summary>
    public static int NetworkDimension(int pixels) => Math.Max(32, (int)Math.Round(pixels / 32.0, MidpointRounding.ToEven) * 32);

    Detection Detect(Models m, Raster img, int dimension, double minScore, CancellationToken cancel)
    {
        int w = img.Width, h = img.Height;
        if (dimension == 0)
        {
            dimension = DetectionDimension(w, h, maxDimension);
        }
        double k = Math.Min(1, (double)dimension / Math.Max(w, h));
        int pw = Math.Max(1, (int)Math.Round(w * k, MidpointRounding.ToEven));
        int ph = Math.Max(1, (int)Math.Round(h * k, MidpointRounding.ToEven));
        // The cubic filter is not interpolating: an image already small
        // enough is analyzed as it is.
        var preview = pw != w || ph != h ? Imaging.Resize(img, pw, ph, 255, 255, 255) : Imaging.Opaque(img);
        int nw = NetworkDimension(pw), nh = NetworkDimension(ph);
        var input = Imaging.Tensor(Imaging.Resize(preview, nw, nh, 255, 255, 255), DetMean, DetStd);
        preview = null;
        List<Geometry.DbBox> found;
        using (var value = OrtValue.CreateTensorValueFromMemory(input, [1, 3, nh, nw]))
        using (var outputs = Onnx.Run(m.Det, [m.Det.InputNames[0]], [value], [m.Det.OutputNames[0]], cancel))
        {
            var dims = Onnx.Shape(outputs[0]);
            if (dims.Length != 4 || dims[2] != nh || dims[3] != nw)
            {
                throw new InvalidOperationException($"unexpected detector output [{string.Join(' ', dims)}]");
            }
            found = Geometry.DbBoxes(outputs[0].GetTensorDataAsSpan<float>(), nh, nw, minScore, DetThreshold, UnclipRatio);
        }
        // Boxes go from the network size to the preview, then to the source.
        float sx = (float)nw / pw, sy = (float)nh / ph;
        var boxes = new List<(Point[] Pts, double Score)>(found.Count);
        foreach (var d in found)
        {
            var pts = new Point[4];
            for (int i = 0; i < 4; i++)
            {
                pts[i] = new Point(
                    Math.Max(0, Math.Min((int)((float)d.Pts[i].X / sx), pw)),
                    Math.Max(0, Math.Min((int)((float)d.Pts[i].Y / sy), ph)));
            }
            boxes.Add((pts, d.Score));
        }
        // Reading order, as PP-OCR sorts boxes: by top; boxes whose tops are
        // within 10 preview pixels of the previous one share a line, and each
        // line goes left to right. OrderBy is a stable sort.
        boxes = [.. boxes.OrderBy(b => b.Pts[0].Y)];
        var lines = new int[boxes.Count];
        for (int i = 1; i < boxes.Count; i++)
        {
            lines[i] = lines[i - 1];
            if (boxes[i].Pts[0].Y - boxes[i - 1].Pts[0].Y >= 10)
            {
                lines[i]++;
            }
        }
        var order = Enumerable.Range(0, boxes.Count).OrderBy(i => lines[i]).ThenBy(i => boxes[i].Pts[0].X);
        var regions = new List<Region>(boxes.Count);
        foreach (int i in order)
        {
            var q = new Point[4];
            for (int c = 0; c < 4; c++)
            {
                var p = boxes[i].Pts[c];
                q[c] = new Point(
                    Math.Max(0, Math.Min((int)Math.Round((double)p.X * w / pw, MidpointRounding.ToEven), w - 1)),
                    Math.Max(0, Math.Min((int)Math.Round((double)p.Y * h / ph, MidpointRounding.ToEven), h - 1)));
            }
            regions.Add(new Region(q, boxes[i].Score));
        }
        return new Detection(regions, pw, ph);
    }

    /// <summary>upsideDown: the line keeps its aspect ratio on a mid-gray canvas.</summary>
    bool UpsideDown(Models m, Raster line, CancellationToken cancel)
    {
        int w = Math.Min(m.ClsW, Math.Max(1, (int)Math.Ceiling((double)m.ClsH * line.Width / line.Height)));
        var canvas = Imaging.Filled(m.ClsW, m.ClsH, 128, 128, 128, 255);
        Imaging.MitchellScale(canvas, w, m.ClsH, line, over: false);
        var input = Imaging.Tensor(canvas, LineMean, LineStd);
        using var value = OrtValue.CreateTensorValueFromMemory(input, [1, 3, m.ClsH, m.ClsW]);
        using var outputs = Onnx.Run(m.Cls, [m.Cls.InputNames[0]], [value], [m.Cls.OutputNames[0]], cancel);
        var out_ = outputs[0].GetTensorDataAsSpan<float>();
        if (out_.Length < 2)
        {
            throw new InvalidOperationException($"unexpected classifier output of {out_.Length} values");
        }
        return out_[1] > out_[0] && out_[1] >= FlipConfidence;
    }

    /// <summary>read: a line's characters, their confidences, CTC columns and column count.</summary>
    (List<string> Chars, List<float> Scores, List<int> Columns, int Count) Read(Models m, Raster line, CancellationToken cancel)
    {
        float scale = (float)RecHeight / line.Height;
        int w = Math.Max(1, (int)(line.Width * scale));
        var input = Imaging.Tensor(Imaging.Resize(line, w, RecHeight, 255, 255, 255), LineMean, LineStd);
        using var value = OrtValue.CreateTensorValueFromMemory(input, [1, 3, RecHeight, w]);
        using var outputs = Onnx.Run(m.Rec, [m.Rec.InputNames[0]], [value], [m.Rec.OutputNames[0]], cancel);
        var dims = Onnx.Shape(outputs[0]);
        if (dims.Length != 3 || dims[2] != m.Keys.Length)
        {
            throw new InvalidOperationException("unexpected recognizer output");
        }
        var (chars, scores, columns) = CtcDecode(outputs[0].GetTensorDataAsSpan<float>(), (int)dims[1], m.Keys);
        return (chars, scores, columns, (int)dims[1]);
    }

    /// <summary>
    /// ctcDecode: greedy CTC decoding, the best class of each column without
    /// blanks (class 0) and without repeats of the previous column's class.
    /// </summary>
    public static (List<string> Chars, List<float> Scores, List<int> Columns) CtcDecode(ReadOnlySpan<float> probs, int columns, string[] keys)
    {
        int classes = keys.Length;
        var chars = new List<string>();
        var scores = new List<float>();
        var cols = new List<int>();
        int last = 0;
        for (int t = 0; t < columns; t++)
        {
            var row = probs.Slice(t * classes, classes);
            int best = 0;
            float value = float.NegativeInfinity;
            for (int c = 0; c < row.Length; c++)
            {
                if (row[c] > value)
                {
                    best = c;
                    value = row[c];
                }
            }
            if (best > 0 && !(t > 0 && best == last))
            {
                chars.Add(keys[best]);
                scores.Add(value);
                cols.Add(t);
            }
            last = best;
        }
        return (chars, scores, cols);
    }
}
