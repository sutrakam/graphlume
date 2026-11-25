<?php
namespace Demo;

use Demo\Util\Helper;

function greet(string $who): string
{
    return strtoupper($who);
}

interface Shape
{
    public function area(): float;
}

trait Loud
{
    public function shout(): void
    {
    }
}

enum Suit
{
    case Hearts;
    case Spades;
}

class Circle implements Shape
{
    use Loud;

    public function __construct(private float $r)
    {
    }

    public function area(): float
    {
        return $this->scale(3.14) * new Circle(1.0)->r;
    }

    private static function scale(float $k): float
    {
        return Helper::twice($k);
    }
}
