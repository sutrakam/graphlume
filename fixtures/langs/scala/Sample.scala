package demo

import demo.util.Helper
import demo.util.{Other => Oth, Third}

trait Shape {
  def area(): Double
}

class Circle(val r: Double) extends Shape with Serializable {
  def area(): Double = r * r * 3.14

  def grow(by: Double): Circle = new Circle(r + by)
}

case class Point(x: Int, y: Int)

object Circle {
  def apply(r: Double): Circle = new Circle(r)

  def unit: Circle = Circle(1.0)
}

enum Suit {
  case Hearts, Spades
}

object Main {
  def main(args: Array[String]): Unit = {
    val c = Circle(2.0)
    println(c.area())
  }
}

class Path(val s: String) {
  type Segment = String
  def /(that: String): Path = new Path(s)
  def ++(that: Path): Path = this

  def run(a: Int): Int = {
    def helper(b: Int): Int = b + 1
    val task = new Runnable { def run(): Unit = () }
    helper(a)
  }
}

type Table = Map[String, Int]
