package demo

import demo.util.Helper

typealias Names = List<String>

interface Shape {
    fun area(): Double
}

enum class Suit {
    HEARTS,
    SPADES
}

data class Point(val x: Int, val y: Int)

open class Circle(val r: Double) : Shape {
    override fun area(): Double = r * r * 3.14

    companion object {
        fun unit(): Circle = Circle(1.0)
    }
}

object Registry {
    fun lookup(id: Int): Circle? = null
}

fun String.shout(): String = this.uppercase()

fun main() {
    val c = Circle(2.0)
    println(c.area())
}

const val MAX_RETRIES = 3
val NO_ANNOTATIONS = emptySet<String>()
internal var counter: Int = 0
val (first, second) = Pair(1, 2)

internal class `-Reader`(private val source: String) {
    fun peek(): Int = 1
}

fun adapter(): Runnable {
    val tmp = 1
    return object : Runnable {
        override fun run() {}
    }
}
