#import "Shape.h"

static NSInteger shapeCounter = 0;
const NSInteger ShapeDefaultSize = 10;

static void logShape(Shape *shape) {
    NSLog(@"%@", shape.name);
}

@interface Shape ()
@property (nonatomic, strong) NSMutableArray *history;
- (void)recordChange;
@end

@implementation Shape

@synthesize name = _name;

+ (instancetype)shapeWithName:(NSString *)name {
    Shape *shape = [[self alloc] initWithName:name kind:ShapeKindCircle];
    return shape;
}

- (instancetype)initWithName:(NSString *)name kind:(ShapeKind)kind {
    self = [super init];
    if (self) {
        _name = [name copy];
        shapeCounter++;
    }
    return self;
}

- (double)scaleBy:(double)factor
         animated:(BOOL)animated {
    [self recordChange];
    return self.area * factor;
}

- (void)reset {
    logShape(self);
}

- (void)draw {
    NSLog(@"draw %@", self.name);
}

- (void)recordChange {
    [self.history addObject:@"change"];
}

@end

@implementation Shape (Printing)

- (void)printSummary {
    NSLog(@"%@: %f", self.name, self.area);
}

@end

@implementation Circle

- (double)area {
    return 3.14 * _radius * _radius;
}

@end

double ShapeArea(Shape *shape) {
    return shape.area;
}

int main(int argc, char *argv[]) {
    @autoreleasepool {
        Shape *s = [Shape shapeWithName:@"a"];
        NSLog(@"%f", ShapeArea(s));
    }
    return 0;
}

static NSString *const kDefaultName = @"shape";

typedef struct Cell {
    int row;
} Cell;

NSString *ShapeName(ShapeKind kind) {
    return kind == ShapeKindCircle ? @"circle" : @"square";
}

@implementation Registry

- (void)addObject:(id)object {
    logShape(object);
}

@end
