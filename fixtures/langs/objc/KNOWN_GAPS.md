# Objective-C: known gaps

## `NS_ENUM` and `NS_OPTIONS` typedefs

```objc
typedef NS_ENUM(NSInteger, ShapeKind) { ShapeKindCircle, ShapeKindSquare };
```

The grammar does not know the macro and reads the declaration as a broken
typedef:

```
(type_definition (type_identifier) (parenthesized_declarator (type_identifier))
  (ERROR (type_identifier)) (type_identifier) (type_identifier) (ERROR))
```

The enum's name is one of several bare `type_identifier` nodes, with nothing to
tell it from the macro name or the base type, so it is not extracted, and the
file is reported as partial. Plain `typedef enum { ... } Name;` is extracted
(as `class`). `Shape.h` in this fixture contains an `NS_ENUM` for that reason.

## Method names are the first selector part

`- (double)scaleBy:(double)factor animated:(BOOL)animated` is recorded as
`scaleBy`. The full selector `scaleBy:animated:` is not a node; only its parts
are, and a pattern can name one capture.

## Methods are recorded in the interface and in the implementation

A method declared in `@interface` and defined in `@implementation` is two
symbols, one per place. Declarations inside `@protocol` are recorded too; there
is no implementation to find them in.

## Categories and extensions

A category (`@interface Shape (Printing)`) is recorded as a `class` named by
the category (`Printing`), not `Shape (Printing)`. A class extension
(`@interface Shape ()`) has no name and is not recorded; its properties and
methods are. A category and a class with a superclass have the same tree, so
they are told apart by matching the start of the declaration's text.

## Functions behind an unknown macro

`JK_STATIC_INLINE size_t jk_min(size_t a, size_t b) { ... }`: when the grammar
does not know the leading macro it reads the macro as the return type and the
real type as a stray identifier:

```
(declaration (type_identifier) (identifier))   ; `JK_STATIC_INLINE int`
```

Whether such a function is still read as a `function_definition` depends on
what surrounds it. In a small file it is; in Linguist's `JSONKit.m` eleven of
them are lost. A declaration that is only `Foo bar;`, with no storage class or
built-in type, is not recorded as a variable for the same reason: it cannot be
told from the broken form above.

## Macros the grammar does not know

Unknown macros are read as types or storage classes. Most positions survive
(`NS_DESIGNATED_INITIALIZER`, `NS_UNAVAILABLE`, `NS_SWIFT_NAME(...)`,
`API_AVAILABLE(...)`, `DEPRECATED_ATTRIBUTE`, `__attribute__((...))` after a
method, and nullability words inside types are all read correctly: see
`Session.h`). One position is not: a macro alone on its line in front of
`@interface`, `@protocol` or `@implementation`, which is how every
`NS_ASSUME_NONNULL_BEGIN` header starts. The grammar reads the macro as a type
and the `@` after it as an error and loses the whole declaration, without an
error node touching the macro:

```
(declaration (type_identifier) (ERROR "@") (identifier) (MISSING ";"))
(labeled_statement (statement_identifier) ...)   ; ": NSObject" of the superclass
```

The macro mask (`cmacro.go`) now reads such a macro as a candidate that must be
followed by a definition at the name after `@interface`, so a plain parse that
lost the class is retried with the macro blanked. The mask's guard is
unchanged: the masked reading is kept only when it has no more errors and no
fewer definitions, and keeps every definition the plain parse had. A header
that is wrapped whole in `#if !TARGET_OS_WATCH ... #endif` and also uses
`NS_ENUM` (AFNetworking's `AFNetworkReachabilityManager.h`) still yields
nothing: the plain parse has 10 error nodes and 11 missing tokens, the
macro-masked text 3 and 1 and 16 definitions, and the result is the plain one. Why it is not taken was not
investigated.

## `extern` declarations are not definitions

`FOUNDATION_EXPORT NSString * const Key;` and `UIKIT_EXTERN const double V;`
declare a name defined elsewhere. A storage class spelled as a macro in capitals
is treated as `extern`, and a declaration that starts with anything but a
storage class, a qualifier or a built-in type (a macro read as its type) is not
a variable. Neither is recorded, so notification-name and key constants in
public headers cannot be looked up by name.

## Not extracted

Function prototypes (`double ShapeArea(Shape *shape);`), `extern` variable
declarations, forward declarations (`@class Layer;`, `@protocol X;`),
`#define` macros, `@synthesize`, `@dynamic`, enum constants, struct members and
named `struct` / `enum` declarations outside a typedef.

## Detection

`.m` files are routed to the MATLAB grammar, `.mm` to XML and `.h` to C, so
Objective-C files do not reach this grammar by extension. This tree does not
change detection.
