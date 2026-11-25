<?php
namespace App\Svc;

use PHPUnit\Framework\TestCase;

// Library calls on a value of unknown type. The repository declares exactly one
// method of each name (on Stub, below), which says nothing about what a call
// on a PSR-7 message or a PHPUnit TestCase reaches.
class WidgetTest extends TestCase // def php.WidgetTest
{
    public function testLibraryCalls($response, $stream) // def php.WidgetTest.testLibraryCalls
    {
        self::fail('expected an exception'); // call fail -> none
        $this->assertSame(1, $response->getStatusCode()); // call assertSame -> none; call getStatusCode -> none
        $body = $response->getBody(); // call getBody -> none
        $text = $stream->getContents(); // call getContents -> none
    }
}

class Stub // def php.Stub
{
    private function fail(string $message): void // def php.Stub.fail
    {
    }

    public function assertSame($a, $b): void // def php.Stub.assertSame
    {
    }

    public function getBody(): string // def php.Stub.getBody
    {
        return '';
    }

    public function getContents(): string // def php.Stub.getContents
    {
        return '';
    }

    public function getStatusCode(): int // def php.Stub.getStatusCode
    {
        return 0;
    }
}
