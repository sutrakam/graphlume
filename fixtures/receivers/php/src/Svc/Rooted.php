<?php
namespace App\Svc;

// A name written with a leading backslash is fully qualified from the global
// namespace: `\Base` is a class declared in no namespace, which is not the
// App\Svc\Base that sits in this directory.
class GlobalChild extends \Base // def php.GlobalChild; super Base -> none
{
}

class NativeChild extends \DateTimeImmutable // def php.NativeChild; super DateTimeImmutable -> none
{
}
