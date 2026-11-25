#import <Foundation/Foundation.h>

/**
 A manager for sessions.
 */

@class Task;

NS_ASSUME_NONNULL_BEGIN

@protocol SessionDelegate <NSObject>

- (void)session:(id)session didFinish:(Task *)task;

@optional
- (BOOL)session:(id)session shouldRetry:(Task *)task NS_SWIFT_NAME(shouldRetry(_:));

@end

@interface Session : NSObject <SessionDelegate, NSCopying>

@property (readonly, nonatomic, strong) NSURLSession *session;
@property (nonatomic, copy, nullable) NSString *name;
@property (nonatomic, copy) void (^ _Nullable completion)(NSError * _Nullable error);

- (instancetype)initWithName:(NSString *)name NS_DESIGNATED_INITIALIZER;

- (instancetype)init NS_UNAVAILABLE;

+ (instancetype)new NS_UNAVAILABLE;

- (void)invalidateAndCancel:(BOOL)cancel API_AVAILABLE(ios(10.0), macos(10.12));

- (nullable Task *)taskWithRequest:(NSURLRequest *)request
                        completion:(nullable void (^)(NSURLResponse *response, NSError * _Nullable error))completion;

- (void)legacyStart DEPRECATED_ATTRIBUTE;

- (void)oldStart __attribute__((deprecated("use start")));

- (void)start NS_SWIFT_NAME(begin());

@end

@interface Session (Helpers)

- (NSString *)describeSession;

@end

FOUNDATION_EXPORT NSString * const SessionDidStartNotification;

FOUNDATION_EXPORT NSString * const SessionDidFinishNotification;

FOUNDATION_EXPORT double SessionVersionNumber;

FOUNDATION_EXPORT NSString * SessionLabel(NSString *prefix);

NS_ASSUME_NONNULL_END
