package store

import store.Tools
import store.{Item => Product, Basket}
import scala.collection.mutable.ListBuffer

class Checkout(cart: Cart) extends Cart("shop") with Pricing { // def sc.Checkout
  def pay(item: Item, basket: Basket): Int = { // def sc.Checkout.pay
    val a = new Cart("a") // call Cart -> new sc.Cart
    val b = Cart("b") // call Cart -> new sc.Cart
    val i = Item("pen", 3) // call Item -> new sc.Item
    val p = Product("pen", 3) // call Product -> none
    val q = new Basket() // call Basket -> new sc.Basket
    val l = new ListBuffer[Int]() // call ListBuffer -> external
    val r = new Runnable { def run(): Unit = () } // call Runnable -> none
    total() // call total -> sc.Cart.total high
    this.total() // call total -> sc.Cart.total high
    discount() // call discount -> sc.Pricing.discount high
    super.total() // call total -> sc.Cart.total high
    Tools.round(3) // call round -> sc.Tools.round
    basket.add(item) // call add -> sc.Basket.add
    cart.add(item) // call add -> sc.Cart.add
    List(1, 2).map(_ + 1) // call List -> none; call map -> none
    println("x") // call println -> none
    Math.max(1, 2) // call max -> external
    item.name.trim() // call trim -> none
    cart add item // call add -> none
    0
  }
}
