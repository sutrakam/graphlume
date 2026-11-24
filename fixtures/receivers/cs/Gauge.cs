namespace Fixture.Gauges;

// A constructor chains to another of its class, never to itself.
public class Gauge // def cs.Gauge
{
    public Gauge() : this(0) { } // def cs.Gauge.zero; call Gauge -> cs.Gauge.one

    public Gauge(int a) : this(a, 1) { } // def cs.Gauge.one; call Gauge -> cs.Gauge.two

    public Gauge(int a, int b) { } // def cs.Gauge.two
}
