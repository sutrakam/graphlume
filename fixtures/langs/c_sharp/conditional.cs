namespace Fixture.Conditional;

public interface IGreeter
{
#if FEATURE_DEFAULT_INTERFACE
    private static readonly string Prefix = "hello";
#endif

    int Count()
#if FEATURE_DEFAULT_INTERFACE
        => 1
#endif
    ;

    int Total(int[] items)
#if FEATURE_DEFAULT_INTERFACE
    {
        return items.Length;
    }
#else
        ;
#endif

#if FEATURE_DEFAULT_INTERFACE
    [DefaultImplementation]
#endif
    bool Matches(string name, bool strict = false)
#if FEATURE_DEFAULT_INTERFACE
        => strict
#endif
    ;

    void Greet(string name);
}

public class Greeter : IGreeter
{
    public int Count() => 0;

    public int Total(int[] items) => 0;

    public bool Matches(string name, bool strict = false) => strict;

    public void Greet(string name)
    {
    }
}
