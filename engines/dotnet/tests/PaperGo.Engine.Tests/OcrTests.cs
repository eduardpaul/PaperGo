using PaperGo.Engine.Ocr;

namespace PaperGo.Engine.Tests;

// Ports of internal/ocr/paddle's unit tests.
public class OcrTests
{
    [Fact]
    public void Dictionary()
    {
        var path = Path.Combine(Models.Ocr, PaddleEngine.Dictionary);
        Assert.SkipWhen(!File.Exists(path), "PaddleOCR models not installed");
        var keys = PaddleEngine.ReadKeys(path);
        // The recognizer's 18,710 classes: blank, 18,708 characters, space.
        Assert.Equal(18710, keys.Length);
        Assert.Equal("#", keys[0]);
        Assert.Equal(" ", keys[^1]);
    }

    [Fact]
    public void CtcDecode()
    {
        string[] keys = ["#", "a", "b", " "];
        // Columns: a, a (repeat), blank, a (new), b, space.
        int[] best = [1, 1, 0, 1, 2, 3];
        var probs = new float[best.Length * keys.Length];
        for (int t = 0; t < best.Length; t++)
        {
            probs[t * keys.Length + best[t]] = 0.9f;
        }
        var (chars, scores, cols) = PaddleEngine.CtcDecode(probs, best.Length, keys);
        Assert.Equal("aab ", string.Concat(chars));
        Assert.Equal(4, scores.Count);
        Assert.Equal([0, 3, 4, 5], cols);
    }

    [Fact]
    public void Words()
    {
        Point[] q = [new(0, 0), new(100, 0), new(100, 20), new(0, 20)];
        var got = Crop.Words(["a", "b", " ", "c", "中", "文"], [0, 1, 3, 5, 7, 8], 10, q)!;
        Assert.Equal(["ab", "c", "中", "文"], got.Select(w => w.Text));
        // "ab" spans columns 0-1: from 0 (clamped) to (1+1.5)/10 of the width.
        Assert.Equal(new Point(0, 0), got[0].Quad[0]);
        Assert.Equal(25, got[0].Quad[1].X);
        Assert.Equal(20, got[0].Quad[2].Y);
    }

    [Theory]
    [InlineData(10, 32)]
    [InlineData(47, 32)]
    [InlineData(48, 64)]
    [InlineData(80, 64)]
    [InlineData(1240, 1248)]
    public void NetworkDimension(int input, int want) => Assert.Equal(want, PaddleEngine.NetworkDimension(input));

    [Fact]
    public void DetectionDimension()
    {
        // A square photo gets about a megapixel; a long receipt keeps the bound.
        Assert.Equal(1024, PaddleEngine.DetectionDimension(4000, 4000, 1536));
        Assert.Equal(1536, PaddleEngine.DetectionDimension(1000, 6000, 1536));
    }

    [Fact]
    public void BoxGeometry()
    {
        // A rectangle of probability 0.9 from x 20-79 and y 10-29.
        int rows = 60, cols = 120;
        var pred = new float[rows * cols];
        for (int y = 10; y < 30; y++)
        {
            for (int x = 20; x < 80; x++)
            {
                pred[y * cols + x] = 0.9f;
            }
        }
        var boxes = Geometry.DbBoxes(pred, rows, cols, 0.5, 0.2, 1.4);
        var b = Assert.Single(boxes);
        // The mean covers the dilated region: 60×20 pixels at 0.9 in 62×22.
        Assert.InRange(b.Score, 0.9 * 60 * 20 / (62 * 22) - 0.01, 0.9 * 60 * 20 / (62 * 22) + 0.01);
        // Dilated to x 19-80, y 9-30 (61×21), then unclipped by 61·21·1.4/164.
        double d = 61.0 * 21 * 1.4 / 164;
        (double X, double Y)[] want = [(19 - d, 9 - d), (80 + d, 9 - d), (80 + d, 30 + d), (19 - d, 30 + d)];
        for (int i = 0; i < 4; i++)
        {
            Assert.InRange(b.Pts[i].X, want[i].X - 1, want[i].X + 1);
            Assert.InRange(b.Pts[i].Y, want[i].Y - 1, want[i].Y + 1);
        }
        Assert.Empty(Geometry.DbBoxes(pred, rows, cols, 0.85, 0.2, 1.4));
        // A line one pixel high is too thin even after dilation.
        var thin = new float[rows * cols];
        for (int x = 20; x < 80; x++)
        {
            thin[40 * cols + x] = 0.9f;
        }
        Assert.Empty(Geometry.DbBoxes(thin, rows, cols, 0.5, 0.2, 1.4));
    }

    [Fact]
    public void MinAreaRectRotated()
    {
        // A 40×10 rectangle turned 30 degrees.
        double a = Math.PI / 6;
        var pts = new List<Geometry.PointF>();
        foreach (var (cx, cy) in new[] { (0.0, 0.0), (40.0, 0.0), (40.0, 10.0), (0.0, 10.0) })
        {
            pts.Add(new(100 + cx * Math.Cos(a) - cy * Math.Sin(a), 100 + cx * Math.Sin(a) + cy * Math.Cos(a)));
        }
        var (rect, w, h) = Geometry.MinAreaRect(Geometry.ConvexHull(pts));
        Assert.InRange(Math.Max(w, h), 40 - 1e-6, 40 + 1e-6);
        Assert.InRange(Math.Min(w, h), 10 - 1e-6, 10 + 1e-6);
        var o = Geometry.MiniBoxOrder(rect);
        Assert.True(o[0].X <= o[1].X && o[3].X <= o[2].X && o[0].Y <= o[3].Y);
    }

    [Fact]
    public void CropRectifies()
    {
        var src = Imaging.White(50, 50);
        for (int y = 20; y < 30; y++)
        {
            for (int x = 10; x < 40; x++)
            {
                src.Pix.AsSpan((y * 50 + x) * 4, 3).Clear();
            }
        }
        var (line, q) = Crop.Rectify(src, [new(10, 20), new(40, 20), new(40, 30), new(10, 30)]);
        Assert.Equal((30, 10), (line.Width, line.Height));
        Assert.Equal(new Point(10, 20), q[0]);
        Assert.Equal(0, line.Pix[(5 * 30 + 15) * 4]);
        // A tall region is vertical text, turned to read left to right.
        var (tall, tq) = Crop.Rectify(src, [new(20, 5), new(30, 5), new(30, 45), new(20, 45)]);
        Assert.Equal((40, 10), (tall.Width, tall.Height));
        Assert.Equal(new Point(20, 45), tq[0]);
        Assert.Throws<TooLargeException>(() => Crop.Rectify(src, [new(0, 0), new(200, 0), new(200, 1), new(0, 1)]));
    }

    [Fact]
    public void OpaqueFlattensStraightAlphaOntoWhite()
    {
        // Half-transparent black over white is mid-gray, as image/draw gives.
        var img = Raster.FromPixels("rgba8", 1, 1, [0, 0, 0, 128]);
        var flat = Imaging.Opaque(img);
        Assert.Equal([127, 127, 127, 255], flat.Pix);
        Assert.False(img.Opaque());
        Assert.True(Raster.FromPixels("gray8", 1, 1, [9]).Opaque());
    }

    [Fact]
    public void MitchellResizeKeepsUniformColor()
    {
        var img = Raster.FromPixels("rgb8", 3, 3, Enumerable.Repeat((byte)200, 27).ToArray());
        var small = Imaging.Resize(img, 2, 2, 255, 255, 255);
        Assert.All(Enumerable.Range(0, 4), i => Assert.Equal(200, small.Pix[i * 4]));
    }
}
