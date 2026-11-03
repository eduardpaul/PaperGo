namespace PaperGo.Engine.Runtime;

/// <summary>The model's queue is full.</summary>
public sealed class BusyException() : Exception("the model is busy; retry later");

/// <summary>
/// Serializes one model's loading, inference and unloading, bounds the
/// requests waiting for it and unloads it after an idle period, as the Go
/// engines' gates and idle timers do.
/// </summary>
public sealed class ModelGate : IDisposable
{
    readonly SemaphoreSlim semaphore = new(1, 1);
    readonly int queue;
    readonly Action unload;
    readonly Timer timer;
    int pending;
    long generation;
    bool loaded;

    public ModelGate(int queue, TimeSpan idle, Action unload)
    {
        this.queue = queue;
        this.unload = unload;
        Idle = idle;
        timer = new Timer(_ => Evict(), null, Timeout.Infinite, Timeout.Infinite);
    }

    public TimeSpan Idle { get; set; }

    /// <summary>Waits for the model; throws BusyException when the queue is full.</summary>
    public async Task<Lease> EnterAsync(CancellationToken cancel)
    {
        if (Interlocked.Increment(ref pending) > queue + 1)
        {
            Interlocked.Decrement(ref pending);
            throw new BusyException();
        }
        try
        {
            await semaphore.WaitAsync(cancel).ConfigureAwait(false);
        }
        catch
        {
            Interlocked.Decrement(ref pending);
            throw;
        }
        generation++;
        timer.Change(Timeout.Infinite, Timeout.Infinite);
        return new Lease(this);
    }

    /// <summary>Records that the holder loaded the model, so it unloads when idle.</summary>
    public void MarkLoaded() => loaded = true;

    void Exit()
    {
        if (loaded)
        {
            timer.Change(Idle, Timeout.InfiniteTimeSpan);
        }
        Interlocked.Decrement(ref pending);
        semaphore.Release();
    }

    void Evict()
    {
        if (!semaphore.Wait(0))
        {
            // In use: the holder rearms the timer when it is done.
            return;
        }
        try
        {
            if (loaded)
            {
                loaded = false;
                unload();
                NativeHeap.Release();
            }
        }
        finally
        {
            semaphore.Release();
        }
    }

    /// <summary>Unloads now; for shutdown and tests.</summary>
    public void Unload()
    {
        semaphore.Wait();
        try
        {
            timer.Change(Timeout.Infinite, Timeout.Infinite);
            if (loaded)
            {
                loaded = false;
                unload();
                NativeHeap.Release();
            }
        }
        finally
        {
            semaphore.Release();
        }
    }

    public void Dispose()
    {
        Unload();
        timer.Dispose();
    }

    public readonly struct Lease(ModelGate gate) : IDisposable
    {
        public void Dispose() => gate.Exit();
    }
}
