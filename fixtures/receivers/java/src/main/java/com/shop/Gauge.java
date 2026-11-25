package com.shop;

// A constructor chains to another of its class, never to itself.
public class Gauge { // def java.Gauge
  public Gauge() { // def java.Gauge.zero
    this(0); // call Gauge -> java.Gauge.one
  }

  public Gauge(int a) { // def java.Gauge.one
    this(a, 1); // call Gauge -> java.Gauge.two
  }

  public Gauge(int a, int b) { // def java.Gauge.two
  }
}
