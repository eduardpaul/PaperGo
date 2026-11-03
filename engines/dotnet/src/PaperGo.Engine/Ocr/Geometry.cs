namespace PaperGo.Engine.Ocr;

/// <summary>
/// DBNet post-processing, ported from paddle/geometry.go (which follows
/// RapidOcrNet's TextDetector): threshold, dilate, components, minimum-area
/// rectangles, mean scores and unclipping.
/// </summary>
public static class Geometry
{
    public readonly record struct PointF(double X, double Y);

    public sealed record DbBox(PointF[] Pts, double Score);

    const double MinBoxSide = 3.0, MinClippedSide = MinBoxSide + 2;

    /// <summary>Go's math.Round: half away from zero.</summary>
    public static double Round(double x) => Math.Round(x, MidpointRounding.AwayFromZero);

    /// <summary>Go's math.Hypot on amd64: max · sqrt(1 + (min/max)²).</summary>
    public static double Hypot(double p, double q)
    {
        p = Math.Abs(p);
        q = Math.Abs(q);
        if (double.IsPositiveInfinity(p) || double.IsPositiveInfinity(q))
        {
            return double.PositiveInfinity;
        }
        if (double.IsNaN(p) || double.IsNaN(q))
        {
            return double.NaN;
        }
        if (p < q)
        {
            (p, q) = (q, p);
        }
        if (p == 0)
        {
            return 0;
        }
        q /= p;
        return p * Math.Sqrt(1 + q * q);
    }

    /// <summary>dbBoxes: post-processes a rows×cols probability map.</summary>
    public static List<DbBox> DbBoxes(ReadOnlySpan<float> pred, int rows, int cols, double minScore, double thresh, double unclipRatio)
    {
        int n = rows * cols;
        var scores = new byte[n];
        var text = new bool[n];
        for (int i = 0; i < n; i++)
        {
            double p = pred[i];
            scores[i] = (byte)Round(Math.Max(0, Math.Min(1, p)) * 255);
            text[i] = p > thresh;
        }
        var mask = Dilate(text, rows, cols);
        var boxes = new List<DbBox>();
        foreach (var r in Components(mask, rows, cols))
        {
            var hull = ConvexHull(r.Extremes());
            if (hull.Count < 3)
            {
                continue;
            }
            var (rect, w, h) = MinAreaRect(hull);
            if (Math.Min(w, h) < MinBoxSide)
            {
                continue;
            }
            double score = r.MeanScore(scores, cols);
            if (score < minScore)
            {
                continue;
            }
            if (!Unclip(rect, w, h, unclipRatio, out var clipped))
            {
                continue;
            }
            var (final, w2, h2) = MinAreaRect(clipped);
            if (Math.Min(w2, h2) < MinClippedSide)
            {
                continue;
            }
            boxes.Add(new DbBox(MiniBoxOrder(final), score));
        }
        return boxes;
    }

    /// <summary>dilate: grows the mask by one pixel in all eight directions.</summary>
    public static bool[] Dilate(bool[] mask, int rows, int cols)
    {
        var out_ = new bool[mask.Length];
        for (int y = 0; y < rows; y++)
        {
            for (int x = 0; x < cols; x++)
            {
                if (!mask[y * cols + x])
                {
                    continue;
                }
                for (int dy = -1; dy <= 1; dy++)
                {
                    for (int dx = -1; dx <= 1; dx++)
                    {
                        int yy = y + dy, xx = x + dx;
                        if (yy >= 0 && yy < rows && xx >= 0 && xx < cols)
                        {
                            out_[yy * cols + xx] = true;
                        }
                    }
                }
            }
        }
        return out_;
    }

    /// <summary>An 8-connected component as each row's leftmost and rightmost pixel.</summary>
    public sealed class Region(int top, int[] lo, int[] hi)
    {
        public int Top { get; } = top;
        public int[] Lo { get; } = lo;
        public int[] Hi { get; } = hi;

        public List<PointF> Extremes()
        {
            var pts = new List<PointF>(2 * Lo.Length);
            for (int i = 0; i < Lo.Length; i++)
            {
                double y = Top + i;
                pts.Add(new PointF(Lo[i], y));
                pts.Add(new PointF(Hi[i], y));
            }
            return pts;
        }

        public double MeanScore(byte[] scores, int cols)
        {
            long sum = 0, count = 0;
            for (int i = 0; i < Lo.Length; i++)
            {
                int row = (Top + i) * cols;
                for (int x = Lo[i]; x <= Hi[i]; x++)
                {
                    sum += scores[row + x];
                }
                count += Hi[i] - Lo[i] + 1;
            }
            return count == 0 ? 0 : (double)sum / count / 255;
        }
    }

    public static List<Region> Components(bool[] mask, int rows, int cols)
    {
        var seen = new bool[mask.Length];
        var out_ = new List<Region>();
        var stack = new Stack<int>();
        var pixels = new List<int>();
        for (int start = 0; start < mask.Length; start++)
        {
            if (!mask[start] || seen[start])
            {
                continue;
            }
            seen[start] = true;
            stack.Clear();
            stack.Push(start);
            pixels.Clear();
            int top = rows, bottom = -1;
            while (stack.Count > 0)
            {
                int p = stack.Pop();
                pixels.Add(p);
                int y = p / cols, x = p % cols;
                top = Math.Min(top, y);
                bottom = Math.Max(bottom, y);
                for (int dy = -1; dy <= 1; dy++)
                {
                    for (int dx = -1; dx <= 1; dx++)
                    {
                        int yy = y + dy, xx = x + dx;
                        if (yy < 0 || yy >= rows || xx < 0 || xx >= cols)
                        {
                            continue;
                        }
                        int q = yy * cols + xx;
                        if (mask[q] && !seen[q])
                        {
                            seen[q] = true;
                            stack.Push(q);
                        }
                    }
                }
            }
            var lo = new int[bottom - top + 1];
            var hi = new int[bottom - top + 1];
            Array.Fill(lo, cols);
            Array.Fill(hi, -1);
            foreach (int p in pixels)
            {
                int i = p / cols - top, x = p % cols;
                lo[i] = Math.Min(lo[i], x);
                hi[i] = Math.Max(hi[i], x);
            }
            out_.Add(new Region(top, lo, hi));
        }
        return out_;
    }

    /// <summary>convexHull: counter-clockwise, Andrew's monotone chain, no collinear points.</summary>
    public static List<PointF> ConvexHull(List<PointF> input)
    {
        var sorted = new List<PointF>(input);
        sorted.Sort(static (a, b) => a.X != b.X ? a.X.CompareTo(b.X) : a.Y.CompareTo(b.Y));
        var pts = new List<PointF>(sorted.Count);
        foreach (var p in sorted)
        {
            if (pts.Count == 0 || pts[^1] != p)
            {
                pts.Add(p);
            }
        }
        if (pts.Count < 3)
        {
            return pts;
        }
        static double Cross(PointF o, PointF a, PointF b) => (a.X - o.X) * (b.Y - o.Y) - (a.Y - o.Y) * (b.X - o.X);
        var hull = new List<PointF>(2 * pts.Count);
        foreach (var p in pts)
        {
            while (hull.Count >= 2 && Cross(hull[^2], hull[^1], p) <= 0)
            {
                hull.RemoveAt(hull.Count - 1);
            }
            hull.Add(p);
        }
        for (int i = pts.Count - 2, lower = hull.Count + 1; i >= 0; i--)
        {
            while (hull.Count >= lower && Cross(hull[^2], hull[^1], pts[i]) <= 0)
            {
                hull.RemoveAt(hull.Count - 1);
            }
            hull.Add(pts[i]);
        }
        hull.RemoveAt(hull.Count - 1);
        return hull;
    }

    /// <summary>minAreaRect: rotating calipers over a convex polygon's edges.</summary>
    public static (PointF[] Rect, double W, double H) MinAreaRect(IReadOnlyList<PointF> hull)
    {
        if (hull.Count == 0)
        {
            return (new PointF[4], 0, 0);
        }
        double best = double.PositiveInfinity, bw = 0, bh = 0;
        var rect = new PointF[4];
        for (int i = 0; i < hull.Count; i++)
        {
            PointF a = hull[i], b = hull[(i + 1) % hull.Count];
            double ux = b.X - a.X, uy = b.Y - a.Y;
            double l = Hypot(ux, uy);
            if (l == 0)
            {
                if (hull.Count == 1)
                {
                    return ([a, a, a, a], 0, 0);
                }
                continue;
            }
            ux /= l;
            uy /= l;
            double vx = -uy, vy = ux;
            double minU = double.PositiveInfinity, maxU = double.NegativeInfinity, minV = double.PositiveInfinity, maxV = double.NegativeInfinity;
            foreach (var p in hull)
            {
                double u = p.X * ux + p.Y * uy, v = p.X * vx + p.Y * vy;
                minU = GoMin(minU, u);
                maxU = GoMax(maxU, u);
                minV = GoMin(minV, v);
                maxV = GoMax(maxV, v);
            }
            double area = (maxU - minU) * (maxV - minV);
            if (area < best)
            {
                best = area;
                bw = maxU - minU;
                bh = maxV - minV;
                PointF At(double u, double v) => new(u * ux + v * vx, u * uy + v * vy);
                rect = [At(minU, minV), At(maxU, minV), At(maxU, maxV), At(minU, maxV)];
            }
        }
        return (rect, bw, bh);
    }

    // Go's math.Min and math.Max: -0 is less than +0.
    static double GoMin(double a, double b) => Math.Min(a, b);
    static double GoMax(double a, double b) => Math.Max(a, b);

    /// <summary>unclip: expands a rectangle by area × ratio / perimeter on every side.</summary>
    public static bool Unclip(PointF[] rect, double w, double h, double ratio, out PointF[] result)
    {
        result = rect;
        if (w < 1.001 && h < 1.001)
        {
            return false;
        }
        double d = w * h * ratio / (2 * (w + h));
        static (double, double) Unit(PointF a, PointF b, double l) => l == 0 ? (0, 0) : ((b.X - a.X) / l, (b.Y - a.Y) / l);
        var (ux, uy) = Unit(rect[0], rect[1], w);
        var (vx, vy) = Unit(rect[0], rect[3], h);
        double cx = (rect[0].X + rect[2].X) / 2, cy = (rect[0].Y + rect[2].Y) / 2;
        var out_ = new PointF[4];
        for (int i = 0; i < 4; i++)
        {
            var p = rect[i];
            double dx = p.X - cx, dy = p.Y - cy;
            double su = Math.CopySign(d, dx * ux + dy * uy), sv = Math.CopySign(d, dx * vx + dy * vy);
            out_[i] = new PointF(Round(p.X + su * ux + sv * vx), Round(p.Y + su * uy + sv * vy));
        }
        result = out_;
        return true;
    }

    /// <summary>miniBoxOrder: RapidOcrNet's GetMiniBox corner order.</summary>
    public static PointF[] MiniBoxOrder(PointF[] rect)
    {
        // A stable insertion sort by x, as sort.SliceStable.
        var p = (PointF[])rect.Clone();
        for (int i = 1; i < p.Length; i++)
        {
            for (int j = i; j > 0 && p[j].X < p[j - 1].X; j--)
            {
                (p[j], p[j - 1]) = (p[j - 1], p[j]);
            }
        }
        PointF tl = p[0], bl = p[1];
        if (p[1].Y <= p[0].Y)
        {
            (tl, bl) = (p[1], p[0]);
        }
        PointF tr = p[2], br = p[3];
        if (p[3].Y <= p[2].Y)
        {
            (tr, br) = (p[3], p[2]);
        }
        return [tl, tr, br, bl];
    }
}
