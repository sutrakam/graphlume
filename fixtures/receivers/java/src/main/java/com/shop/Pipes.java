package com.shop;

// A supertype written with a qualifier is the type that qualifier names, and
// not a type of the same name declared here (os-lib's SubProcess.scala, in Java).
public class Pipes { // def Pipes
  public static class OutputStream { // def Pipes.OutputStream
  }

  public static class Plain extends OutputStream { // super OutputStream -> Pipes.OutputStream
  }

  public static class Logged extends java.io.OutputStream { // super OutputStream -> none
    public void write(int b) {
    }
  }

  public static class Owned extends Pipes.OutputStream { // super OutputStream -> Pipes.OutputStream
  }

  public interface Closeable { // def Pipes.Closeable
  }

  public static class Both implements java.io.Closeable { // super Closeable -> none
    public void close() {
    }
  }
}
