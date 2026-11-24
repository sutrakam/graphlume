#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

/**
 Returns an escaped string.
 */
FOUNDATION_EXPORT NSString * EscapedString(NSString *string);

FOUNDATION_EXPORT NSString * const KeysDidChangeNotification;

FOUNDATION_EXPORT NSString * const KeysDidResetNotification;

UIKIT_EXTERN const double KeysVersionNumber;

typedef struct {
    int major;
    int minor;
} KeysVersion;

static int keysLoaded;

NS_ASSUME_NONNULL_END
