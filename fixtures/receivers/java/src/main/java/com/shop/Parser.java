package com.shop;

import com.shop.io.JsonReader;
import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.List;

public class Parser extends Base {
  private JsonReader reader(String json) { // def Parser.reader
    return new JsonReader(); // call JsonReader -> new JsonReader
  }

  public int intValue() { // def Parser.intValue
    return asBigDecimal().intValue(); // call intValue -> none
  }

  private BigDecimal asBigDecimal() { // def Parser.asBigDecimal
    return BigDecimal.ONE;
  }

  public int parse(String json, List<String> strings) {
    JsonReader reader = reader(json); // call reader -> Parser.reader high
    strings.add(json); // call add -> external; nocall strings
    return reader.peek(); // call peek -> JsonReader.peek high; nocall reader
  }

  public void build(String json) { // def Parser.build
    JsonReader a = new com.shop.io.JsonReader(); // call JsonReader -> new JsonReader
    Object b = new com.other.JsonReader(); // call JsonReader -> none
    Object c = new java.awt.Dimension(); // call Dimension -> external
    List<String> d = new ArrayList<String>(); // call ArrayList -> external
    Box<String> e = new Box<String>(json); // call Box -> new Box
    Box<String> f = new Box<>(json); // call Box -> new Box
    Box.Slot g = new Box.Slot(); // call Slot -> new Box.Slot
    Runnable h = new Runnable() { // call Runnable -> none
      public void run() { // def Parser.build.run
      }
    };
    Base i = new Base() { // call Base -> new Base
      public String describe() { // def Parser.build.describe
        return "anon";
      }
    };
    Visitor j = new Visitor() { // call Visitor -> none
      public void visit(Object node) { // def Parser.build.visit
      }
    };
    new Box<String>(json).toString(); // call Box -> new Box; call toString -> none
    Object l = new int[3];
  }

  @Override
  public String describe() { // def Parser.describe
    return super.describe(); // call describe -> Base.describe high
  }

  @Override
  protected Object clone() throws CloneNotSupportedException { // def Parser.clone
    return super.clone(); // call clone -> none
  }
}
