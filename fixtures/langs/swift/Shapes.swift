import Foundation

public let defaultScale = 2
var counter: Int = 0

typealias Handler = (Int) -> Void

protocol Drawable {
    associatedtype Canvas
    var area: Double { get }
    func draw(on canvas: Canvas)
    static func make() -> Self
    init(side: Double)
    subscript(index: Int) -> Int { get }
}

struct Square: Drawable {
    typealias Canvas = String
    let side: Double
    var label = "sq"
    static let unit = 1.0
    static var created = 0
    lazy var cache: [Int] = []

    var area: Double { side * side }
    var doubled: Double {
        get { side * 2 }
        set { _ = newValue }
    }

    init(side: Double) { self.side = side }
    init?(text: String) { return nil }
    init(from decoder: Decoder) throws { side = 0 }

    func draw(on canvas: String) {
        let local = 3
        print(local, canvas)
    }

    mutating func grow(by amount: Double) {}
    static func make() -> Square { Square(side: 1) }

    subscript(index: Int) -> Int { index }
    subscript(row: Int, column: Int) -> Int {
        get { row + column }
    }

    static func == (lhs: Square, rhs: Square) -> Bool { lhs.side == rhs.side }
    static func + (lhs: Square, rhs: Square) -> Square { lhs }
    static prefix func - (value: Square) -> Square { value }

    struct Inner {
        var depth = 0
        func nested() {}
    }
}

class Base {
    var name: String
    init(name: String) { self.name = name }
    deinit { print("bye") }
    class func shared() -> Base { Base(name: "b") }
    final func lock() {}
    override func description() -> String { name }
    required init() { name = "" }
    convenience init(id: Int) { self.init(name: "\(id)") }
}

final class Derived: Base {
    @Published var state = 0
    @discardableResult
    public func run<T>(_ value: T) -> T where T: Equatable { value }
    func outer() {
        func innerHelper() {}
        innerHelper()
    }
}

enum Direction: Int {
    case north, south
    case east = 3
    case west(Int)

    var opposite: Direction { .north }
    func turn() -> Direction { self }
    static func random() -> Direction { .north }
}

indirect enum Tree {
    case leaf
    case node(Tree, Tree)
}

actor Counter {
    var value = 0
    func increment() { value += 1 }
    nonisolated func peek() {}
}

extension Square {
    func perimeter() -> Double { side * 4 }
    var diagonal: Double { side }
    static let zero = Square(side: 0)
}

extension Array where Element == Int {
    func total() -> Int { reduce(0, +) }
}

prefix operator +++
infix operator **: MultiplicationPrecedence

func topLevel(_ a: Int, label b: Int) -> Int {
    let result = a + b
    return result
}

func generic<Element: Comparable>(items: [Element]) -> Element? { items.first }

@MainActor
func isolated() async throws {}

open class Opened {
    public var items: [String] = []
    private(set) var count = 0
    weak var owner: Base?
    open func hook() {}
}
