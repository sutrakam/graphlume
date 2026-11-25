package com.shop;

import com.shop.util.Tally;

// A class of the same name sits in this file's own package: the single-type
// import outranks it, so `new Tally()` is the util one.
public class Counter { // def Counter
  Object make() {
    return new Tally(); // call Tally -> new util.Tally
  }
}
