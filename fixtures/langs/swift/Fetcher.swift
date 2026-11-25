import Foundation

/// A request subclass. The two `cancel` methods below make the grammar give up
/// on the class: its header and members come out as loose nodes under an ERROR.
public final class Fetcher: Base, @unchecked Sendable {
    public struct Options: OptionSet, Sendable {
        public let rawValue: Int

        public init(rawValue: Int) {
            self.rawValue = rawValue
        }
    }

    static let defaultDestination: Destination = { url, _ in
        (url, [])
    }

    private let state = Protected(0)

    public var resumeData: Data? { nil }

    override func reset() {
        let local = 1
        print(local)
    }

    func didFinish(with result: Int) {
        print(result)
    }

    @preconcurrency
    @discardableResult
    public func cancel(byProducing completionHandler: @escaping @Sendable (_ data: Data?) -> Void) -> Self {
        cancel(optionallyProducing: completionHandler)
    }

    private func cancel(optionallyProducing completionHandler: (@Sendable (_ data: Data?) -> Void)?) -> Self {
        mutableState.write { mutableState in
            guard mutableState.state.canTransitionTo(.cancelled) else { return }

            mutableState.state = .cancelled
        }
        return self
    }

    public func validate(_ validation: @escaping Validation) -> Self {
        self
    }

    public func response(queue: DispatchQueue = .main,
                         completionHandler: @escaping @Sendable (Int) -> Void) -> Self {
        self
    }
}
