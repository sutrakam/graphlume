import Combine

public struct Publisher2<Value: Sendable>: Publisher {
    public typealias Output = Response<Value, Failure>
    public typealias Failure = Never

    private typealias Handler = (@escaping @Sendable (_ response: Response<Value, Failure>) -> Void) -> Request

    private let request: Request
    private let responseHandler: Handler

    public init(_ request: Request, handler: @escaping Handler) {
        self.request = request
        responseHandler = handler
    }

    public func receive<S: Subscriber>(subscriber: S) where S.Input == Output {
        subscriber.receive(subscription: Inner(request: request,
                                               responseHandler: responseHandler,
                                               downstream: subscriber))
    }

    private final class Inner<Downstream: Subscriber & Sendable>: Subscription
        where Downstream.Input == Output {
        typealias Failure = Downstream.Failure

        private let downstream: Protected<Downstream?>
        private let request: Request
        private let responseHandler: Handler

        init(request: Request, responseHandler: @escaping Handler, downstream: Downstream) {
            self.request = request
            self.responseHandler = responseHandler
            self.downstream = Protected(downstream)
        }

        func request(_ demand: Subscribers.Demand) {
            assert(demand > 0)
        }

        func cancel() {
            request.cancel()
        }
    }
}

extension [Publisher2<Int>] {
    func firstValue() -> Int? { nil }
}

extension [String: Int] {
    func total() -> Int { 0 }
}

extension Dictionary<String, Int> {
    func keysSorted() -> [String] { [] }
}

extension (Int, Int) {
    func sum() -> Int { 0 }
}
