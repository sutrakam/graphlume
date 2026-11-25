package store

class Cart(val owner: String) { // def sc.Cart
  def add(item: Item): Unit = { // def sc.Cart.add
  }

  def total(): Int = 0 // def sc.Cart.total
}

object Cart { // def sc.Cart.object
  def apply(owner: String): Cart = new Cart(owner) // def sc.Cart.apply; call Cart -> new sc.Cart

  def empty(): Cart = apply("nobody") // call apply -> sc.Cart.apply high
}

case class Item(name: String, price: Int) // def sc.Item

trait Pricing { // def sc.Pricing
  def discount(): Int = 0 // def sc.Pricing.discount
}

class Basket { // def sc.Basket
  def add(item: Item): Unit = { // def sc.Basket.add
  }
}

object Tools { // def sc.Tools
  def round(n: Int): Int = n // def sc.Tools.round
}
