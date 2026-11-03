using System.Runtime.InteropServices;
using System.Text;

namespace PaperGo.Engine.Runtime;

/// <summary>
/// A Hugging Face tokenizer through the C ABI of the daulet/tokenizers static
/// library, the same Rust code the Go engines link, so token IDs are identical.
/// Callers serialize use and disposal.
/// </summary>
public sealed unsafe partial class Tokenizer : IDisposable
{
    const string Library = "tokenizers";

    [StructLayout(LayoutKind.Sequential)]
    struct EncodeOptions
    {
        public byte AddSpecialTokens, ReturnTypeIds, ReturnTokens, ReturnSpecialTokensMask, ReturnAttentionMask, ReturnOffsets;
    }

    [StructLayout(LayoutKind.Sequential)]
    struct Buffer
    {
        public uint* Ids;
        public nint TypeIds, SpecialTokensMask, AttentionMask, Tokens, Offsets;
        public nuint Len;
    }

    [LibraryImport(Library, EntryPoint = "tokenizers_from_file", StringMarshalling = StringMarshalling.Utf8)]
    private static partial nint FromFile(string path, out nint error);

    [LibraryImport(Library, EntryPoint = "tokenizers_encode")]
    private static partial Buffer Encode(nint tokenizer, byte* message, EncodeOptions* options);

    [LibraryImport(Library, EntryPoint = "tokenizers_free_buffer")]
    private static partial void FreeBuffer(Buffer buffer);

    [LibraryImport(Library, EntryPoint = "tokenizers_free_tokenizer")]
    private static partial void FreeTokenizer(nint tokenizer);

    [LibraryImport(Library, EntryPoint = "tokenizers_free_string")]
    private static partial void FreeString(nint s);

    nint handle;

    Tokenizer(nint handle) => this.handle = handle;

    /// <summary>Loads a tokenizer.json file.</summary>
    public static Tokenizer Load(string path)
    {
        var h = FromFile(path, out var error);
        if (h == 0)
        {
            var message = error != 0 ? Marshal.PtrToStringUTF8(error) : null;
            if (error != 0)
            {
                FreeString(error);
            }
            throw new InvalidOperationException($"load tokenizer {path}: {message ?? "unknown error"}");
        }
        return new Tokenizer(h);
    }

    /// <summary>
    /// Encodes text as the Go wrapper does: a NUL-terminated UTF-8 string,
    /// optionally with the post-processor's special tokens.
    /// </summary>
    public uint[] EncodeIds(string text, bool addSpecialTokens)
    {
        ObjectDisposedException.ThrowIf(handle == 0, this);
        // Invalid UTF-16 becomes U+FFFD, as Go's JSON decoder makes it.
        int n = Encoding.UTF8.GetByteCount(text);
        var bytes = new byte[n + 1];
        Encoding.UTF8.GetBytes(text, bytes);
        var options = new EncodeOptions { AddSpecialTokens = addSpecialTokens ? (byte)1 : (byte)0 };
        Buffer result;
        fixed (byte* p = bytes)
        {
            result = Encode(handle, p, &options);
        }
        int len = checked((int)result.Len);
        if (len == 0)
        {
            if (text.Length != 0 && result.Ids == null)
            {
                throw new InvalidOperationException("failed to encode input");
            }
            return [];
        }
        try
        {
            return new ReadOnlySpan<uint>(result.Ids, len).ToArray();
        }
        finally
        {
            FreeBuffer(result);
        }
    }

    public void Dispose()
    {
        if (handle != 0)
        {
            FreeTokenizer(handle);
            handle = 0;
        }
    }
}
