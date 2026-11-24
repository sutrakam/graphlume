<?php
namespace App\Svc;

use App\Util\Helper;
use App\Util\Other as Oth;
use function App\Util\helper_fn;

class Widget extends Base // def php.Widget
{
    public function save() // def php.Widget.save
    {
    }

    public function go($n) // def php.Widget.go
    {
    }

    public function run($x) // def php.Widget.run
    {
        $this->save(); // call save -> php.Widget.save high
        $this->inherited(); // call inherited -> php.Base.inherited high
        parent::run($x); // call run -> php.Base.run high
        self::boot(); // call boot -> php.Base.boot high
        static::boot(); // call boot -> php.Base.boot high
        Helper::make(2); // call make -> php.Helper.make high
        $h = new Helper(); // call Helper -> new php.Helper
        $w = new Widget(3); // call Widget -> new php.Widget
        helper_fn($x); // call helper_fn -> php.helper_fn
        $this->client->save(); // call save -> none
        $x->go(1); // call go -> php.Widget.go low
        strlen($x); // call strlen -> none
        $q = new \Psr\Q(); // call Q -> none
        $o = new Oth(); // call Oth -> none
    }

    public function typed(Helper $h, Widget $w) // def php.Widget.typed
    {
        $h->save(); // call save -> php.Helper.save
        $w->save(); // call save -> php.Widget.save
        $h?->save(); // call save -> none
        (new Helper())->save(); // call Helper -> new php.Helper; call save -> none
        $unknown->save(); // call save -> none
        $this->name->trim(); // call trim -> none
    }
}
