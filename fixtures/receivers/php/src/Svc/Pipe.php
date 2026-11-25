<?php
namespace App\Svc;

// A member is looked up on the receiver's own class first, then up its
// ancestry, nearest first. monolog bound `$handler->close()` to Handler::close
// at high although SocketHandler, which extends Handler, declares its own.
class Pipe // def php.Pipe
{
    public function close() // def php.Pipe.close
    {
    }
}

class SocketPipe extends Pipe // def php.SocketPipe
{
    public function close() // def php.SocketPipe.close
    {
    }
}

class LazyPipe extends SocketPipe // def php.LazyPipe
{
}

class PipeUser // def php.PipeUser
{
    public function named($pipe) // def php.PipeUser.named
    {
        $pipe->close(); // call close -> php.Pipe.close low
    }

    public function typed(SocketPipe $sp, LazyPipe $lp, Pipe $p) // def php.PipeUser.typed
    {
        $sp->close(); // call close -> php.SocketPipe.close high
        $lp->close(); // call close -> php.SocketPipe.close
        $p->close(); // call close -> php.Pipe.close
    }
}
