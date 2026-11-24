# Site manifest: nodes and a few small definitions.
$default_dns = ['10.0.0.2', '10.0.0.3']

node /^db\d+\.example\.com$/ {
  include profile::database
}

node 'app01.example.com', 'app02.example.com' {
  include profile::app
  class { 'profile::monitoring':
    interval => 30,
  }
}

class profile::database(Integer $max_connections = 100) {
  package { 'postgresql-server':
    ensure => present,
  }
}

class profile::app inherits profile::base {
  Package <| tag == 'app' |>
}

define profile::user_account($shell = '/bin/bash') {
  user { $title:
    shell => $shell,
  }
}

function profile::dns_servers() {
  $default_dns
}

type Profile::Mode = Enum['active', 'passive']

type Profile::Tag = String[1]

class profile::monitoring($interval = 60) {
  notify { "polling every ${interval}": }
}
