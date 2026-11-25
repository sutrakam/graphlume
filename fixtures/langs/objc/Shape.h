#import <Foundation/Foundation.h>

#define MAX_SHAPES 16

typedef NS_ENUM(NSInteger, ShapeKind) {
    ShapeKindCircle,
    ShapeKindSquare,
};

typedef struct {
    double x;
    double y;
} Point;

typedef void (^ShapeHandler)(NSString *name);

typedef NSUInteger ShapeId;

extern const NSInteger ShapeDefaultSize;

@protocol Drawable <NSObject>
- (void)draw;
@optional
- (NSString *)caption;
+ (instancetype)defaultDrawable;
@end

@interface Shape : NSObject <Drawable>

@property (nonatomic, copy) NSString *name;
@property (nonatomic, assign) double area;
@property (nonatomic, readonly) ShapeKind kind;

+ (instancetype)shapeWithName:(NSString *)name;
- (instancetype)initWithName:(NSString *)name kind:(ShapeKind)kind;
- (double)scaleBy:(double)factor
         animated:(BOOL)animated;
- (void)reset;

@end

@interface Shape (Printing)
- (void)printSummary;
@end

@interface Circle : Shape {
    double _radius;
}
@property (nonatomic) double radius;
@end

double ShapeArea(Shape *shape);

@class Layer;

@interface Registry<ObjectType> : NSObject
@property (nonatomic, readonly) NSArray<ObjectType> *items;
@property (nonatomic, copy) void (^onChange)(void);
- (void)addObject:(ObjectType)object;
@end

NSString *ShapeName(ShapeKind kind);
