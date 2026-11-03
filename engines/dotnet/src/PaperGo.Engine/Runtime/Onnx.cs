using System.Runtime.InteropServices;
using Microsoft.ML.OnnxRuntime;

namespace PaperGo.Engine.Runtime;

/// <summary>
/// ONNX Runtime sessions configured as internal/onnx configures them.
/// </summary>
public static class Onnx
{
    static readonly Lock initLock = new();
    static bool initialized;

    static void Initialize()
    {
        lock (initLock)
        {
            if (!initialized)
            {
                var envOptions = new EnvironmentCreationOptions
                {
                    logId = "papergo-engine",
                    logLevel = OrtLoggingLevel.ORT_LOGGING_LEVEL_ERROR,
                };
                OrtEnv.CreateInstanceWithOptions(ref envOptions);
                initialized = true;
            }
        }
    }

    /// <summary>The ONNX Runtime version string.</summary>
    public static string Version
    {
        get
        {
            Initialize();
            return OrtEnv.Instance().GetVersionString();
        }
    }

    /// <summary>
    /// Opens a model: intra-op threads as configured, one inter-op thread,
    /// sequential execution, all graph optimizations, no CPU arena and no
    /// memory pattern.
    /// </summary>
    public static InferenceSession Open(string path, int threads)
    {
        if (threads is < 1 or > 32)
        {
            throw new ArgumentOutOfRangeException(nameof(threads), "ONNX threads must be between 1 and 32");
        }
        Initialize();
        using var options = new Microsoft.ML.OnnxRuntime.SessionOptions
        {
            IntraOpNumThreads = threads,
            InterOpNumThreads = 1,
            ExecutionMode = ExecutionMode.ORT_SEQUENTIAL,
            GraphOptimizationLevel = GraphOptimizationLevel.ORT_ENABLE_ALL,
            EnableCpuMemArena = false,
            EnableMemoryPattern = false,
            LogSeverityLevel = OrtLoggingLevel.ORT_LOGGING_LEVEL_ERROR,
        };
        return new InferenceSession(path, options);
    }

    /// <summary>
    /// Runs a session. Cancellation terminates this run only, through
    /// RunOptions.Terminate, and throws OperationCanceledException.
    /// </summary>
    public static IDisposableReadOnlyCollection<OrtValue> Run(InferenceSession session, IReadOnlyCollection<string> inputNames,
        IReadOnlyCollection<OrtValue> inputs, IReadOnlyCollection<string> outputNames, CancellationToken cancel)
    {
        cancel.ThrowIfCancellationRequested();
        using var options = new RunOptions();
        IDisposableReadOnlyCollection<OrtValue> outputs;
        using (cancel.Register(static o => ((RunOptions)o!).Terminate = true, options))
        {
            try
            {
                outputs = session.Run(options, inputNames, inputs, outputNames);
            }
            catch (OnnxRuntimeException) when (cancel.IsCancellationRequested)
            {
                throw new OperationCanceledException(cancel);
            }
        }
        if (cancel.IsCancellationRequested)
        {
            outputs.Dispose();
            throw new OperationCanceledException(cancel);
        }
        return outputs;
    }

    public static long[] Shape(OrtValue value) => value.GetTensorTypeAndShape().Shape;
}

/// <summary>Bounds glibc's heap as internal/nativeheap does.</summary>
public static partial class NativeHeap
{
    const int M_TRIM_THRESHOLD = -1, M_ARENA_MAX = -8;

    [LibraryImport("libc", EntryPoint = "mallopt")]
    private static partial int Mallopt(int param, int value);

    [LibraryImport("libc", EntryPoint = "malloc_trim")]
    private static partial int MallocTrim(nuint pad);

    [LibraryImport("libc", EntryPoint = "umask")]
    internal static partial uint Umask(uint mask);

    // musl has neither call; the engine then skips them.
    static bool glibc = OperatingSystem.IsLinux();

    static void Call(Action action)
    {
        if (!glibc)
        {
            return;
        }
        try
        {
            action();
        }
        catch (Exception e) when (e is EntryPointNotFoundException or DllNotFoundException)
        {
            glibc = false;
        }
    }

    /// <summary>Bounds per-thread arenas and keeps large buffers eligible for munmap.</summary>
    public static void Configure()
    {
        Call(static () =>
        {
            Mallopt(M_ARENA_MAX, 2);
            Mallopt(M_TRIM_THRESHOLD, 128 * 1024);
        });
    }

    /// <summary>Returns free heap memory to the OS after a model unloads.</summary>
    public static void Release()
    {
        GC.Collect();
        GC.WaitForPendingFinalizers();
        Call(static () => MallocTrim(0));
    }
}
