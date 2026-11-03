using System.Text;

namespace PaperGo.Engine.Ocr;

/// <summary>
/// Text line rectification and word placement, ported from paddle/crop.go.
/// </summary>
public static class Crop
{
    const long MaxCropPixels = 16_000_000;
    const int MaxCropAspect = 128;

    /// <summary>The projective rectangle-to-quad mapping (Heckbert).</summary>
    public readonly record struct Homography(double A, double B, double C, double D, double E, double F, double G, double H)
    {
        public (double X, double Y) Apply(double u, double v)
        {
            double w = G * u + H * v + 1;
            return ((A * u + B * v + C) / w, (D * u + E * v + F) / w);
        }
    }

    public static bool RectangleToQuad(Point[] q, double width, double height, out Homography m)
    {
        double x0 = q[0].X, y0 = q[0].Y, x1 = q[1].X, y1 = q[1].Y, x2 = q[2].X, y2 = q[2].Y, x3 = q[3].X, y3 = q[3].Y;
        double dx1 = x1 - x2, dx2 = x3 - x2, dx3 = x0 - x1 + x2 - x3;
        double dy1 = y1 - y2, dy2 = y3 - y2, dy3 = y0 - y1 + y2 - y3;
        double g = 0, h = 0;
        if (dx3 != 0 || dy3 != 0)
        {
            double den = dx1 * dy2 - dx2 * dy1;
            if (Math.Abs(den) < 1e-9)
            {
                m = default;
                return false;
            }
            g = (dx3 * dy2 - dx2 * dy3) / den;
            h = (dx1 * dy3 - dx3 * dy1) / den;
        }
        m = new Homography(
            (x1 - x0 + g * x1) / width, (x3 - x0 + h * x3) / height, x0,
            (y1 - y0 + g * y1) / width, (y3 - y0 + h * y3) / height, y0,
            g / width, h / height);
        return true;
    }

    /// <summary>
    /// crop: rectifies the text region q of src (an opaque RGBA image) into an
    /// upright line image, and returns the region's corners in the line's
    /// reading order. Regions 1.5 times taller than wide turn a quarter turn.
    /// </summary>
    public static (Raster Line, Point[] Quad) Rectify(Raster src, Point[] q)
    {
        double Edge(int a, int b) => Geometry.Hypot(q[a].X - q[b].X, q[a].Y - q[b].Y);
        int w = Math.Max(1, (int)Geometry.Round(Math.Max(Edge(0, 1), Edge(2, 3))));
        int h = Math.Max(1, (int)Geometry.Round(Math.Max(Edge(0, 3), Edge(1, 2))));
        if ((long)w * h > MaxCropPixels || (double)Math.Max(w, h) / Math.Min(w, h) > MaxCropAspect)
        {
            throw new TooLargeException("a text line exceeds the OCR recognition memory bound; crop or split the page");
        }
        if (!RectangleToQuad(q, w, h, out var m))
        {
            throw new InvalidOperationException("a detected text region is degenerate");
        }
        var out_ = Raster.NewRGBA(w, h);
        int sw = src.Width, sh = src.Height;
        Span<byte> px = stackalloc byte[4];
        for (int y = 0; y < h; y++)
        {
            for (int x = 0; x < w; x++)
            {
                var (sx, sy) = m.Apply(x + 0.5, y + 0.5);
                int o = (y * w + x) * 4;
                if (sx < 0 || sy < 0 || sx > sw || sy > sh)
                {
                    out_.Pix[o] = out_.Pix[o + 1] = out_.Pix[o + 2] = out_.Pix[o + 3] = 255;
                    continue;
                }
                Bilinear(src, sx - 0.5, sy - 0.5, out_.Pix.AsSpan(o, 4));
            }
        }
        if (h < w * 1.5)
        {
            return (out_, q);
        }
        return (Imaging.Rotate90(out_), [q[3], q[0], q[1], q[2]]);
    }

    /// <summary>bilinear: samples src at pixel coordinates, clamping at the edges.</summary>
    static void Bilinear(Raster src, double x, double y, Span<byte> out_)
    {
        int w = src.Width, h = src.Height;
        int x0 = (int)Math.Floor(x), y0 = (int)Math.Floor(y);
        double fx = x - x0, fy = y - y0;
        static int Clamp(int v, int hi) => Math.Max(0, Math.Min(v, hi - 1));
        int xa = Clamp(x0, w), xb = Clamp(x0 + 1, w), ya = Clamp(y0, h), yb = Clamp(y0 + 1, h);
        var pix = src.Pix;
        int stride = src.Stride;
        for (int c = 0; c < 4; c++)
        {
            double top = pix[ya * stride + xa * 4 + c] * (1 - fx) + pix[ya * stride + xb * 4 + c] * fx;
            double bottom = pix[yb * stride + xa * 4 + c] * (1 - fx) + pix[yb * stride + xb * 4 + c] * fx;
            out_[c] = unchecked((byte)Geometry.Round(top * (1 - fy) + bottom * fy));
        }
    }

    /// <summary>Go's strings.TrimSpace(s) == "".</summary>
    public static bool Blank(string s)
    {
        foreach (var r in s.EnumerateRunes())
        {
            if (!IsGoSpace(r.Value))
            {
                return false;
            }
        }
        return true;
    }

    /// <summary>Go's unicode.IsSpace.</summary>
    static bool IsGoSpace(int r) => r switch
    {
        '\t' or '\n' or '\v' or '\f' or '\r' or ' ' or 0x85 or 0xA0 => true,
        < 0x100 => false,
        0x1680 or (>= 0x2000 and <= 0x200a) or 0x2028 or 0x2029 or 0x202f or 0x205f or 0x3000 => true,
        _ => false,
    };

    /// <summary>
    /// words: places the words of a recognized line on its region. Each
    /// character carries its CTC column; a word spans from half a column
    /// before its first character to one and a half after its last.
    /// Characters of scripts without spaces are words of their own.
    /// </summary>
    public static List<Word>? Words(IReadOnlyList<string> chars, IReadOnlyList<int> columns, int count, Point[] q)
    {
        if (chars.Count == 0 || columns.Count != chars.Count || count <= 0)
        {
            return null;
        }
        if (!RectangleToQuad(q, 1, 1, out var m))
        {
            return [new Word(string.Concat(chars), q)];
        }
        Point At(double u, double v)
        {
            var (x, y) = m.Apply(u, v);
            return new Point((int)Geometry.Round(x), (int)Geometry.Round(y));
        }
        var out_ = new List<Word>();
        for (int i = 0; i < chars.Count;)
        {
            if (Blank(chars[i]))
            {
                i++;
                continue;
            }
            int start = i;
            i++;
            if (!Ideographic(chars[start]))
            {
                while (i < chars.Count && !Blank(chars[i]) && !Ideographic(chars[i]))
                {
                    i++;
                }
            }
            double left = Math.Max(0, Math.Min(1, (columns[start] - 0.5) / count));
            double right = Math.Max(left, Math.Min(1, (columns[i - 1] + 1.5) / count));
            var text = new StringBuilder();
            for (int k = start; k < i; k++)
            {
                text.Append(chars[k]);
            }
            out_.Add(new Word(text.ToString(), [At(left, 0), At(right, 0), At(right, 1), At(left, 1)]));
        }
        return out_;
    }

    /// <summary>ideographic: CJK and Hangul, written without spaces between words.</summary>
    public static bool Ideographic(string s)
    {
        foreach (var rune in s.EnumerateRunes())
        {
            int r = rune.Value;
            return r is >= 0x2E80 and <= 0x9FFF or >= 0xAC00 and <= 0xD7AF or >= 0xF900 and <= 0xFAFF or >= 0x20000 and <= 0x3134F;
        }
        return false;
    }
}
