// Package schema describes a small service catalogue.
package schema

import (
	"strings"
	"list"
)

// Definitions are closed schemas.
#Port: int & >=1 & <=65535

#Service: {
	name:     string
	port:     #Port
	replicas: *1 | int
	labels?: [string]: string
	env: {
		LOG_LEVEL: *"info" | "debug"
	}
}

#Environment: "dev" | "staging" | "prod"

_defaultLabels: {
	team: "platform"
}

// Regular top-level fields.
catalogue: {
	api: #Service & {
		name: "api"
		port: 8080
	}
	worker: #Service & {
		name: "worker"
		port: 9090
	}
}

environment: #Environment | *"dev"

limits: {
	cpu:    "500m"
	memory: "256Mi"
}

names: [for s in catalogue {s.name}]

total: len(names)

upper: strings.ToUpper("x")

let base = 10

doubled: base * 2

"quoted-field": true

[string]: _

if environment == "prod" {
	alerts: true
}
