using PaperGo.Engine.Protocol;
using PaperGo.Engine.Runtime;

namespace PaperGo.Engine;

public static class Program
{
    public static async Task<int> Main(string[] args)
    {
        if (args is ["-h" or "--help"])
        {
            Console.WriteLine(EngineOptions.Usage);
            Console.WriteLine(Diagnostics.Usage);
            return 0;
        }
        if (args is ["tokenize" or "laya-encode", _, ..])
        {
            return await Diagnostics.Run(args);
        }
        EngineOptions options;
        Engines engines;
        try
        {
            options = EngineOptions.Parse(args, Environment.GetEnvironmentVariable);
            NativeHeap.Configure();
            engines = Engines.Create(options);
        }
        catch (Exception e) when (e is ArgumentException or IOException or InvalidOperationException or UnauthorizedAccessException)
        {
            await Console.Error.WriteLineAsync("papergo-engine: " + e.Message);
            return 2;
        }
        using (engines)
        {
            await using var app = EngineHost.Build(args, options, engines);
            await app.StartAsync();
            EngineHost.AfterStart(options);
            if (options.ExitOnStdinEof)
            {
                EngineHost.ExitOnStdinEof(app);
            }
            await app.WaitForShutdownAsync();
        }
        return 0;
    }
}

/// <summary>Kestrel on a Unix socket or loopback TCP.</summary>
public static class EngineHost
{
    public static WebApplication Build(string[] args, EngineOptions options, Engines engines)
    {
        var builder = WebApplication.CreateSlimBuilder(new WebApplicationOptions { Args = [] });
        builder.Logging.SetMinimumLevel(LogLevel.Warning);
        builder.WebHost.ConfigureKestrel(k =>
        {
            k.AddServerHeader = false;
            k.Limits.MaxRequestBodySize = 64L << 20;
            if (options.Listen.StartsWith("unix:", StringComparison.Ordinal))
            {
                var path = options.Listen["unix:".Length..];
                if (File.Exists(path))
                {
                    File.Delete(path);
                }
                k.ListenUnixSocket(path);
            }
            else
            {
                var endpoint = System.Net.IPEndPoint.Parse(options.Listen["tcp:".Length..]);
                if (!System.Net.IPAddress.IsLoopback(endpoint.Address))
                {
                    throw new ArgumentException("--listen tcp: must be a loopback address");
                }
                k.Listen(endpoint);
            }
        });
        // Sockets are created owner-only; AfterStart restores the umask.
        if (options.Listen.StartsWith("unix:", StringComparison.Ordinal) && !OperatingSystem.IsWindows())
        {
            previousUmask = NativeHeap.Umask(0x3F) /* 077 */;
        }
        var app = builder.Build();
        byte[]? token = null;
        if (options.TokenFile is { } file)
        {
            token = System.Text.Encoding.UTF8.GetBytes(File.ReadAllText(file).Trim());
            if (token.Length == 0)
            {
                throw new ArgumentException("the token file is empty");
            }
        }
        Server.Map(app, engines, token);
        return app;
    }

    static uint? previousUmask;

    public static void AfterStart(EngineOptions options)
    {
        if (options.Listen.StartsWith("unix:", StringComparison.Ordinal) && !OperatingSystem.IsWindows())
        {
            File.SetUnixFileMode(options.Listen["unix:".Length..], UnixFileMode.UserRead | UnixFileMode.UserWrite);
            if (previousUmask is { } mask)
            {
                NativeHeap.Umask(mask);
                previousUmask = null;
            }
        }
    }

    /// <summary>Stops the engine when standard input reaches end of file.</summary>
    public static void ExitOnStdinEof(WebApplication app)
    {
        var thread = new Thread(() =>
        {
            using var stdin = Console.OpenStandardInput();
            var buffer = new byte[256];
            try
            {
                while (stdin.Read(buffer) > 0)
                {
                }
            }
            catch (IOException)
            {
            }
            app.Lifetime.StopApplication();
        })
        { IsBackground = true, Name = "stdin-eof" };
        thread.Start();
    }
}
