using System;
using System.Collections.Generic;

namespace Shop.Orders;

public interface IRepository<T>
{
    T Find(int id);
    IEnumerable<T> All();
}

public interface ILogger
{
    ILogger ForContext(string name);

    ILogger Enrich(Action<string> configure)
    {
        return ForContext("x");
    }
}

public class Order : IEquatable<Order>
{
    public int Id { get; set; }
    public string Name => "order";
    public decimal Total { get; init; } = 0m;
    public event EventHandler Changed;
    public event EventHandler Closed { add { } remove { } }
    private int count;

    public Order() : this(0) { }
    public Order(int id) { Id = id; }

    public ILogger Logger() => new Log();
    public static Order Create(int id) => new Order(id);
    public int this[int index] => index;
    public bool Equals(Order other) { return other != null; }

    private void Run()
    {
        var a = new Order();
        var b = new Shop.Orders.Order(1);
        var c = new List<int>();
        Order d = new();
        if (a == null) { return; }
    }
}

public struct Point { public int X; public int Y; }
public enum Mode { Fast, Slow }
public record Pair(int A, int B);
public delegate void Handler(object sender);
