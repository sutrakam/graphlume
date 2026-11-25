# Manages the web server.
class webserver (
  String $package_name = 'nginx',
  Integer $port = 80,
  Optional[String] $docroot = undef,
) inherits webserver::params {
  $service_name = "${package_name}-svc"

  package { $package_name:
    ensure => installed,
  }

  file { '/etc/nginx/nginx.conf':
    ensure  => file,
    content => template('webserver/nginx.conf.erb'),
    require => Package[$package_name],
  }

  service { $service_name:
    ensure => running,
    enable => true,
  }

  include webserver::firewall
  class { 'webserver::tuning':
    workers => 4,
  }
}

class webserver::firewall {
  exec { 'open-port':
    command => '/sbin/iptables -A INPUT -p tcp --dport 80 -j ACCEPT',
  }
}

define webserver::vhost (
  String $docroot,
  Boolean $ssl = false,
) {
  file { "/etc/nginx/conf.d/${title}.conf":
    ensure  => file,
    content => epp('webserver/vhost.epp', { 'docroot' => $docroot }),
    notify  => Service['nginx'],
  }
}

node 'web01.example.com' {
  include webserver
  webserver::vhost { 'shop':
    docroot => '/srv/shop',
  }
}

node default {
  notify { 'unmanaged host': }
}

function webserver::port_for(String $env) >> Integer {
  $ports = { 'dev' => 8080, 'prod' => 80 }
  $ports[$env]
}

type Webserver::Port = Integer[1, 65535]

type Webserver::Env = Enum['dev', 'stage', 'prod']

plan webserver::rollout(TargetSpec $targets) {
  $results = run_command('systemctl restart nginx', $targets)
  return $results
}
