<?php
namespace App\Util;

class Helper // def php.Helper
{
    public static function make(int $n): Helper // def php.Helper.make
    {
        return new Helper(); // call Helper -> new php.Helper
    }

    public function save(): void // def php.Helper.save
    {
    }
}

function helper_fn($x) // def php.helper_fn
{
    return $x;
}
