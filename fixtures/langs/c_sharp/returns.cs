namespace Fixture.Returns;

public interface ILogger
{
    ILogger ForContext(ILogEventEnricher enricher);
    ILogger ForContext<TSource>();
    ILogger? Maybe(int level);
    Task<ILogger> LaterAsync(int level);
    IEnumerable<ILogger> All();

#if FEATURE_DEFAULT_INTERFACE
    [DefaultImplementation]
#endif
    ILogger Named(string name)
#if FEATURE_DEFAULT_INTERFACE
        => ForContext<ILogger>()
#endif
    ;

    ILogger Scoped(string name);
}

public class Factory : ILogger
{
    public ILogger ForContext(ILogEventEnricher enricher) { return null; }

    public ILogger ForContext<TSource>() => null;

    public ILogger? Maybe(int level) => null;

    public Task<ILogger> LaterAsync(int level) => null;

    public IEnumerable<ILogger> All() => null;

    public Factory Self(Factory other) { return other; }
}
