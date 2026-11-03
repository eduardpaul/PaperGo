namespace PaperGo.Engine.Ocr;

/// <summary>The pixel layouts of Go's image.Gray, image.NRGBA and image.RGBA.</summary>
public enum PixelKind { Gray, NRGBA, RGBA }

/// <summary>
/// An image with Go's in-memory layout: rows of Stride bytes from the top
/// left. RGBA is alpha-premultiplied, NRGBA is not, as in Go.
/// </summary>
public sealed class Raster(PixelKind kind, int width, int height, byte[] pix)
{
    public PixelKind Kind { get; } = kind;
    public int Width { get; } = width;
    public int Height { get; } = height;
    public byte[] Pix { get; } = pix;
    public int Stride => Kind == PixelKind.Gray ? Width : 4 * Width;

    public static Raster NewRGBA(int width, int height) => new(PixelKind.RGBA, width, height, new byte[checked(4 * width * height)]);

    /// <summary>Builds a raster from the protocol's pixel formats.</summary>
    public static Raster FromPixels(string format, int width, int height, byte[] data) => format switch
    {
        "gray8" => new Raster(PixelKind.Gray, width, height, data),
        "rgba8" => new Raster(PixelKind.NRGBA, width, height, data),
        "rgb8" => FromRgb(width, height, data),
        _ => throw new ArgumentException("the pixel format must be rgba8, rgb8 or gray8"),
    };

    public static int BytesPerPixel(string format) => format switch
    {
        "gray8" => 1,
        "rgba8" => 4,
        "rgb8" => 3,
        _ => 0,
    };

    static Raster FromRgb(int width, int height, byte[] rgb)
    {
        var out_ = NewRGBA(width, height);
        var d = out_.Pix;
        for (int i = 0, j = 0; j < d.Length; i += 3, j += 4)
        {
            d[j] = rgb[i];
            d[j + 1] = rgb[i + 1];
            d[j + 2] = rgb[i + 2];
            d[j + 3] = 255;
        }
        return out_;
    }

    /// <summary>Go's Opaque: every alpha is 0xff.</summary>
    public bool Opaque()
    {
        if (Kind == PixelKind.Gray)
        {
            return true;
        }
        for (int i = 3; i < Pix.Length; i += 4)
        {
            if (Pix[i] != 0xff)
            {
                return false;
            }
        }
        return true;
    }
}

/// <summary>Ports of the image/draw and golang.org/x/image/draw paths paddle uses.</summary>
public static class Imaging
{
    const uint M = 0xffff;

    /// <summary>filled: a w×h RGBA image of one opaque or translucent color.</summary>
    public static Raster Filled(int w, int h, byte r, byte g, byte b, byte a)
    {
        var img = Raster.NewRGBA(w, h);
        var p = img.Pix;
        for (int i = 0; i < p.Length; i += 4)
        {
            p[i] = r;
            p[i + 1] = g;
            p[i + 2] = b;
            p[i + 3] = a;
        }
        return img;
    }

    public static Raster White(int w, int h) => Filled(w, h, 255, 255, 255, 255);

    /// <summary>opaque: the image flattened onto white (draw.Draw with draw.Over).</summary>
    public static Raster Opaque(Raster src)
    {
        var dst = White(src.Width, src.Height);
        DrawOver(dst, src);
        return dst;
    }

    // drawCopyOver, drawNRGBAOver and drawGray of image/draw, over the whole image.
    static void DrawOver(Raster dst, Raster src)
    {
        var d = dst.Pix;
        var s = src.Pix;
        int n = src.Width * src.Height;
        switch (src.Kind)
        {
            case PixelKind.Gray:
                for (int i = 0; i < n; i++)
                {
                    byte v = s[i];
                    d[4 * i] = v;
                    d[4 * i + 1] = v;
                    d[4 * i + 2] = v;
                    d[4 * i + 3] = 255;
                }
                break;
            case PixelKind.RGBA:
                for (int i = 0; i < 4 * n; i += 4)
                {
                    uint sr = s[i] * 0x101u, sg = s[i + 1] * 0x101u, sb = s[i + 2] * 0x101u, sa = s[i + 3] * 0x101u;
                    uint a = (M - sa) * 0x101;
                    d[i] = unchecked((byte)((d[i] * a / M + sr) >> 8));
                    d[i + 1] = unchecked((byte)((d[i + 1] * a / M + sg) >> 8));
                    d[i + 2] = unchecked((byte)((d[i + 2] * a / M + sb) >> 8));
                    d[i + 3] = unchecked((byte)((d[i + 3] * a / M + sa) >> 8));
                }
                break;
            case PixelKind.NRGBA:
                for (int i = 0; i < 4 * n; i += 4)
                {
                    uint sa = s[i + 3] * 0x101u;
                    uint sr = s[i] * sa / 0xff, sg = s[i + 1] * sa / 0xff, sb = s[i + 2] * sa / 0xff;
                    uint a = (M - sa) * 0x101;
                    d[i] = unchecked((byte)((d[i] * a / M + sr) >> 8));
                    d[i + 1] = unchecked((byte)((d[i + 1] * a / M + sg) >> 8));
                    d[i + 2] = unchecked((byte)((d[i + 2] * a / M + sb) >> 8));
                    d[i + 3] = unchecked((byte)((d[i + 3] * a / M + sa) >> 8));
                }
                break;
        }
    }

    /// <summary>resize: src scaled to w×h with the Mitchell filter, over background.</summary>
    public static Raster Resize(Raster src, int w, int h, byte r, byte g, byte b)
    {
        var dst = Filled(w, h, r, g, b, 255);
        MitchellScale(dst, w, h, src, over: true);
        return dst;
    }

    /// <summary>
    /// The Mitchell–Netravali cubic (B = C = 1/3) of paddle/image.go. Go
    /// folds its untyped constant coefficients exactly, so they are written
    /// here as their exact values: 12-9B-6C = 7, -18+12B+6C = -12,
    /// 6-2B = 16/3, -B-6C = -7/3, 6B+30C = 12, -12B-48C = -20, 8B+24C = 32/3.
    /// </summary>
    static double Mitchell(double t)
    {
        t = Math.Abs(t);
        if (t < 1)
        {
            return (7 * t * t * t + -12 * t * t + 16.0 / 3) / 6;
        }
        if (t < 2)
        {
            return (-7.0 / 3 * t * t * t + 12 * t * t + -20 * t + 32.0 / 3) / 6;
        }
        return 0;
    }

    const double Support = 2;

    sealed class Distrib
    {
        public required int[] I, J;
        public required double[] InvTotalWeight, InvTotalWeightFFFF;
        public required int[] Coord;
        public required double[] Weight;
    }

    // newDistrib of x/image/draw.
    static Distrib NewDistrib(int dw, int sw)
    {
        double scale = (double)sw / dw;
        double halfWidth = Support, kernelArgScale = 1.0;
        if (scale > 1)
        {
            halfWidth *= scale;
            kernelArgScale = 1 / scale;
        }
        var si = new int[dw];
        var sj = new int[dw];
        var centers = new double[dw];
        long n = 0;
        for (int x = 0; x < dw; x++)
        {
            double center = (x + 0.5) * scale - 0.5;
            int i = (int)Math.Floor(center - halfWidth);
            if (i < 0)
            {
                i = 0;
            }
            int j = (int)Math.Ceiling(center + halfWidth);
            if (j > sw)
            {
                j = sw;
                if (j < i)
                {
                    j = i;
                }
            }
            si[x] = i;
            sj[x] = j;
            centers[x] = center;
            n += j - i;
        }
        var coords = new List<int>((int)n);
        var weights = new List<double>((int)n);
        var itw = new double[dw];
        var itwf = new double[dw];
        for (int k = 0; k < dw; k++)
        {
            double totalWeight = 0.0;
            int l = coords.Count;
            for (int coord = si[k]; coord < sj[k]; coord++)
            {
                double t = (centers[k] - coord) * kernelArgScale;
                if (t < 0)
                {
                    t = -t;
                }
                if (t >= Support)
                {
                    continue;
                }
                double weight = Mitchell(t);
                if (weight == 0)
                {
                    continue;
                }
                totalWeight += weight;
                coords.Add(coord);
                weights.Add(weight);
            }
            totalWeight = 1 / totalWeight;
            si[k] = l;
            sj[k] = coords.Count;
            itw[k] = totalWeight;
            itwf[k] = totalWeight / 0xffff;
        }
        return new Distrib { I = si, J = sj, InvTotalWeight = itw, InvTotalWeightFFFF = itwf, Coord = [.. coords], Weight = [.. weights] };
    }

    static ushort Ftou(double f)
    {
        int i = (int)(0xffff * f + 0.5);
        if (i > 0xffff)
        {
            return 0xffff;
        }
        return i > 0 ? (ushort)i : (ushort)0;
    }

    /// <summary>
    /// Kernel.Scale of x/image/draw onto the dw×dh top-left rectangle of an
    /// RGBA dst, with draw.Over (over) or draw.Src.
    /// </summary>
    public static void MitchellScale(Raster dst, int dw, int dh, Raster src, bool over)
    {
        int sw = src.Width, sh = src.Height;
        if (dw <= 0 || dh <= 0 || sw <= 0 || sh <= 0)
        {
            return;
        }
        if (over && src.Opaque())
        {
            over = false;
        }
        var horizontal = NewDistrib(dw, sw);
        var vertical = NewDistrib(dh, sh);
        var tmp = new double[(long)dw * sh * 4];
        ScaleX(tmp, src, horizontal, sh);
        ScaleY(dst, dw, dh, tmp, vertical, over);
    }

    static void ScaleX(double[] tmp, Raster src, Distrib h, int sh)
    {
        var pix = src.Pix;
        int stride = src.Stride;
        int dw = h.I.Length;
        int t = 0;
        for (int y = 0; y < sh; y++)
        {
            int row = y * stride;
            for (int s = 0; s < dw; s++)
            {
                double pr = 0, pg = 0, pb = 0, pa = 0;
                switch (src.Kind)
                {
                    case PixelKind.Gray:
                        for (int c = h.I[s]; c < h.J[s]; c++)
                        {
                            uint pru = pix[row + h.Coord[c]] * 0x101u;
                            pr += pru * h.Weight[c];
                        }
                        pr *= h.InvTotalWeightFFFF[s];
                        tmp[t] = pr;
                        tmp[t + 1] = pr;
                        tmp[t + 2] = pr;
                        tmp[t + 3] = 1;
                        t += 4;
                        continue;
                    case PixelKind.NRGBA:
                        for (int c = h.I[s]; c < h.J[s]; c++)
                        {
                            int pi = row + h.Coord[c] * 4;
                            uint pau = pix[pi + 3] * 0x101u;
                            uint pru = pix[pi] * pau / 0xff;
                            uint pgu = pix[pi + 1] * pau / 0xff;
                            uint pbu = pix[pi + 2] * pau / 0xff;
                            double w = h.Weight[c];
                            pr += pru * w;
                            pg += pgu * w;
                            pb += pbu * w;
                            pa += pau * w;
                        }
                        break;
                    default:
                        for (int c = h.I[s]; c < h.J[s]; c++)
                        {
                            int pi = row + h.Coord[c] * 4;
                            double w = h.Weight[c];
                            pr += pix[pi] * 0x101u * w;
                            pg += pix[pi + 1] * 0x101u * w;
                            pb += pix[pi + 2] * 0x101u * w;
                            pa += pix[pi + 3] * 0x101u * w;
                        }
                        break;
                }
                double f = h.InvTotalWeightFFFF[s];
                tmp[t] = pr * f;
                tmp[t + 1] = pg * f;
                tmp[t + 2] = pb * f;
                tmp[t + 3] = pa * f;
                t += 4;
            }
        }
    }

    static void ScaleY(Raster dst, int dw, int dh, double[] tmp, Distrib v, bool over)
    {
        var d = dst.Pix;
        int stride = dst.Stride;
        for (int dx = 0; dx < dw; dx++)
        {
            int di = dx * 4;
            for (int s = 0; s < dh; s++)
            {
                double pr = 0, pg = 0, pb = 0, pa = 0;
                for (int c = v.I[s]; c < v.J[s]; c++)
                {
                    long p = ((long)v.Coord[c] * dw + dx) * 4;
                    double w = v.Weight[c];
                    pr += tmp[p] * w;
                    pg += tmp[p + 1] * w;
                    pb += tmp[p + 2] * w;
                    pa += tmp[p + 3] * w;
                }
                if (pr > pa)
                {
                    pr = pa;
                }
                if (pg > pa)
                {
                    pg = pa;
                }
                if (pb > pa)
                {
                    pb = pa;
                }
                double itw = v.InvTotalWeight[s];
                if (over)
                {
                    uint pr0 = Ftou(pr * itw), pg0 = Ftou(pg * itw), pb0 = Ftou(pb * itw), pa0 = Ftou(pa * itw);
                    uint pa1 = (0xffff - pa0) * 0x101;
                    d[di] = unchecked((byte)((d[di] * pa1 / 0xffff + pr0) >> 8));
                    d[di + 1] = unchecked((byte)((d[di + 1] * pa1 / 0xffff + pg0) >> 8));
                    d[di + 2] = unchecked((byte)((d[di + 2] * pa1 / 0xffff + pb0) >> 8));
                    d[di + 3] = unchecked((byte)((d[di + 3] * pa1 / 0xffff + pa0) >> 8));
                }
                else
                {
                    d[di] = unchecked((byte)(Ftou(pr * itw) >> 8));
                    d[di + 1] = unchecked((byte)(Ftou(pg * itw) >> 8));
                    d[di + 2] = unchecked((byte)(Ftou(pb * itw) >> 8));
                    d[di + 3] = unchecked((byte)(Ftou(pa * itw) >> 8));
                }
                di += stride;
            }
        }
    }

    /// <summary>
    /// tensor: a 1×3×H×W tensor in BGR order, (value - mean[c]) / std[c].
    /// </summary>
    public static float[] Tensor(Raster img, ReadOnlySpan<float> mean, ReadOnlySpan<float> std)
    {
        int w = img.Width, h = img.Height, plane = w * h;
        var out_ = new float[3 * plane];
        var p = img.Pix;
        for (int i = 0; i < plane; i++)
        {
            int q = 4 * i;
            out_[i] = (p[q + 2] - mean[0]) / std[0];
            out_[plane + i] = (p[q + 1] - mean[1]) / std[1];
            out_[2 * plane + i] = (p[q] - mean[2]) / std[2];
        }
        return out_;
    }

    /// <summary>rotate90: a quarter turn clockwise.</summary>
    public static Raster Rotate90(Raster img)
    {
        int w = img.Width, h = img.Height;
        var out_ = Raster.NewRGBA(h, w);
        for (int y = 0; y < h; y++)
        {
            for (int x = 0; x < w; x++)
            {
                Array.Copy(img.Pix, (y * w + x) * 4, out_.Pix, (x * h + (h - 1 - y)) * 4, 4);
            }
        }
        return out_;
    }

    /// <summary>rotate180: upside down.</summary>
    public static Raster Rotate180(Raster img)
    {
        int w = img.Width, h = img.Height;
        var out_ = Raster.NewRGBA(w, h);
        for (int y = 0; y < h; y++)
        {
            for (int x = 0; x < w; x++)
            {
                Array.Copy(img.Pix, (y * w + x) * 4, out_.Pix, ((h - 1 - y) * w + (w - 1 - x)) * 4, 4);
            }
        }
        return out_;
    }
}
