package demo;

import java.util.ArrayList;
import java.util.List;

public class Sample extends Base implements Runnable {
  private final List<String> names = new ArrayList<String>();

  public Sample(int n) {
  }

  public void run() {
    Sample copy = new Sample(1);
    Runnable r = new Runnable() {
      public void run() {
      }
    };
    names.add(copy.toString());
  }

  static class Inner {
    int depth() {
      return 1;
    }
  }

  interface Visitor {
    void visit(Object node);
  }

  enum Mode {
    FAST,
    SLOW
  }

  record Point(int x, int y) {
  }

  @interface Marker {
  }
}
