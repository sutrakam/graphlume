package com.shop;

// Overloads: the type is known, the declaration is a choice the argument count
// may make. javapoet's ClassName.get("pkg", "Name") was bound to get(Class<?>).
public class Names {
  public static Names get(Class<?> type) { // def Names.getClass
    return null;
  }

  public static Names get(String pkg, String simple, String... rest) { // def Names.getParts
    return null;
  }

  public Names put(String key) { // def Names.putString
    return this;
  }

  public Names put(Integer key) { // def Names.putInteger
    return this;
  }

  public Names pair(String a, String b) { // def Names.pairTwo
    return this;
  }

  public Names pair(String a) { // def Names.pairOne
    return this;
  }

  public static void use(Names names, String key, Integer id) { // def Names.use
    Names a = Names.get("com.shop", "Names"); // call get -> Names.getParts high
    Names b = Names.get(String.class); // call get -> Names.getClass low
    names.put(key); // call put -> Names.putString low
    names.pair(key, key); // call pair -> Names.pairTwo high
    names.pair(key); // call pair -> Names.pairOne high
  }
}
