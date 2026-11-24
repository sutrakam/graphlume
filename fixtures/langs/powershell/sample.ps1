#requires -Version 5.0
using namespace System.Collections.Generic

$script:Counter = 0
$Settings = @{ Name = 'demo' }

function Get-Thing {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)] [string] $Name
    )
    $local = "$Name-x"
    return $local
}

function Set-Thing($value) {
    Write-Output $value
}

function script:Private-Helper {
    param($x)
    $x + 1
}

function global:Publish-Thing {
    Get-Thing -Name 'a'
}

function local:Scoped {
    1
}

function private:Hidden-One {
    2
}

function plain {
    3
}

function script:noArgs() { 4 }

filter script:Only-Even {
    if ($_ % 2 -eq 0) { $_ }
}

filter Skip-Blank {
    if ($_) { $_ }
}

workflow Invoke-Flow {
    Get-Thing -Name 'w'
}

function Outer-Func {
    function Inner-Func {
        5
    }
    Inner-Func
}

enum Color {
    Red
    Green
}

class Animal {
    [string] $Name
    static [int] $Count = 0

    Animal([string] $name) {
        $this.Name = $name
    }

    [string] Speak() {
        return "..."
    }

    static [Animal] Create([string] $name) {
        return [Animal]::new($name)
    }

    hidden [void] Reset() {
        $this.Name = ''
    }
}

class Dog : Animal {
    [string] Speak() {
        return "Woof"
    }
}

Get-Thing -Name 'x'
Set-Thing 1
$result = Get-Thing -Name 'y'
